package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrProjectNotFound reports a project ID with no row in projects.
var ErrProjectNotFound = errors.New("store: project not found")

// ProjectInfo is one row of projects with its aggregates (spec §18.7).
type ProjectInfo struct {
	ID         string
	RootPath   string
	Storage    string // "global" | "local"
	CreatedAt  int64
	LastSeenAt int64
	Sessions   int64
	// Bytes is the raw size of the distinct contents the project's messages
	// reference. Contents are shared across projects, so the sum over all
	// projects can exceed the database size.
	Bytes int64
}

const projectInfoSelect = `
	SELECT p.id, p.root_path, p.storage, p.created_at, p.last_seen_at,
	  (SELECT COUNT(*) FROM sessions s WHERE s.project_id = p.id),
	  (SELECT COALESCE(SUM(c.size_bytes), 0) FROM contents c
	     WHERE c.hash IN (SELECT m.content_hash FROM messages m
	       JOIN sessions s ON s.id = m.session_id WHERE s.project_id = p.id))
	FROM projects p`

// ListProjects returns every project, most recently seen first.
func (s *Store) ListProjects(ctx context.Context) ([]ProjectInfo, error) {
	return queryProjects(ctx, s.db, projectInfoSelect+" ORDER BY p.last_seen_at DESC, p.id")
}

// GetProject returns one project, or ErrProjectNotFound.
func (s *Store) GetProject(ctx context.Context, id string) (ProjectInfo, error) {
	list, err := queryProjects(ctx, s.db, projectInfoSelect+" WHERE p.id = ?1", id)
	if err != nil {
		return ProjectInfo{}, err
	}
	if len(list) == 0 {
		return ProjectInfo{}, fmt.Errorf("%w: %s", ErrProjectNotFound, id)
	}
	return list[0], nil
}

func queryProjects(ctx context.Context, q rowSource, query string, args ...any) ([]ProjectInfo, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()
	var out []ProjectInfo
	for rows.Next() {
		var p ProjectInfo
		if err := rows.Scan(&p.ID, &p.RootPath, &p.Storage, &p.CreatedAt, &p.LastSeenAt, &p.Sessions, &p.Bytes); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProjectIDByRoot returns the ID of the project registered at root, or
// ErrProjectNotFound. After MoveProject the ID no longer equals the hash of the
// root, so callers look it up here before deriving one.
func (s *Store) ProjectIDByRoot(ctx context.Context, root string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE root_path = ?1`, root).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: root %s", ErrProjectNotFound, root)
	}
	if err != nil {
		return "", fmt.Errorf("project by root %s: %w", root, err)
	}
	return id, nil
}

// MoveProject points an existing project at a new root path. Its ID does not
// change (spec §18.2). It fails with ErrProjectNotFound for an unknown ID and
// with a unique-index error when another project already owns newRoot.
func (s *Store) MoveProject(ctx context.Context, id, newRoot string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET root_path = ?1, last_seen_at = ?2 WHERE id = ?3`,
		newRoot, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("move project %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrProjectNotFound, id)
	}
	return nil
}

// purgeSteps lists the DELETE statements of PurgeProject in foreign-key-safe
// order. Every statement selects by the session IDs of the project, and
// sessions and projects go last. search_idx has no foreign key, so it is
// listed explicitly: skipping it would leave searchable copies behind.
var purgeSteps = []struct {
	table string
	query string
}{
	{"search_idx", `DELETE FROM search_idx WHERE session_id IN (` + projectSessions + `)`},
	{"events", `DELETE FROM events WHERE session_id IN (` + projectSessions + `)`},
	{"cache_ledger", `DELETE FROM cache_ledger WHERE session_id IN (` + projectSessions + `)`},
	{"intents", `DELETE FROM intents WHERE session_id IN (` + projectSessions + `)`},
	{"trigger_state", `DELETE FROM trigger_state WHERE session_id IN (` + projectSessions + `)`},
	{"block_sources", `DELETE FROM block_sources WHERE session_id IN (` + projectSessions + `)`},
	{"block_messages", `DELETE FROM block_messages WHERE session_id IN (` + projectSessions + `)`},
	{"blocks", `DELETE FROM blocks WHERE session_id IN (` + projectSessions + `)`},
	{"messages", `DELETE FROM messages WHERE session_id IN (` + projectSessions + `)`},
	{"sessions", `DELETE FROM sessions WHERE project_id = ?1`},
}

const projectSessions = `SELECT id FROM sessions WHERE project_id = ?1`

// PurgeProject deletes every row of one project in a single transaction and
// returns the number of rows removed per table. This is the only deletion in
// the store: the event log is otherwise append-only (spec §5.2). Contents are
// content-addressed and shared, so a content row goes only when no message
// refers to it any more.
func (s *Store) PurgeProject(ctx context.Context, id string) (map[string]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?1`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check project %s: %w", id, err)
	}
	if exists == 0 {
		return nil, fmt.Errorf("%w: %s", ErrProjectNotFound, id)
	}

	hashes, err := stringColumn(ctx, tx,
		`SELECT DISTINCT content_hash FROM messages WHERE session_id IN (`+projectSessions+`)`, id)
	if err != nil {
		return nil, fmt.Errorf("collect contents of %s: %w", id, err)
	}

	counts := make(map[string]int64, len(purgeSteps)+2)
	for _, st := range purgeSteps {
		n, err := execCount(ctx, tx, st.query, id)
		if err != nil {
			return nil, fmt.Errorf("purge %s of %s: %w", st.table, id, err)
		}
		counts[st.table] = n
	}

	// Content rows referenced by another project's messages stay.
	var orphans int64
	for _, h := range hashes {
		n, err := execCount(ctx, tx, `
			DELETE FROM contents WHERE hash = ?1
			  AND NOT EXISTS (SELECT 1 FROM messages WHERE content_hash = ?1)`, h)
		if err != nil {
			return nil, fmt.Errorf("purge content %s: %w", h, err)
		}
		orphans += n
	}
	counts["contents"] = orphans

	n, err := execCount(ctx, tx, `DELETE FROM projects WHERE id = ?1`, id)
	if err != nil {
		return nil, fmt.Errorf("purge project %s: %w", id, err)
	}
	counts["projects"] = n

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit purge of %s: %w", id, err)
	}
	return counts, nil
}

// rowSource is satisfied by *sql.DB and *sql.Tx.
type rowSource interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func execCount(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func stringColumn(ctx context.Context, q rowSource, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Row is one table row of a dump, keyed by column name. Values are the
// driver's own types (int64, string, []byte, nil), so BLOBs such as zstd
// contents are copied byte for byte.
type Row map[string]any

// TableRows holds the rows of one table in a dump.
type TableRows struct {
	Table string
	Rows  []Row
}

// ProjectDump is every row that belongs to one project, grouped by table in
// foreign-key order (see dumpTables).
type ProjectDump struct {
	ProjectID string
	Tables    []TableRows
}

// SessionIDs lists the sessions in the dump.
func (d ProjectDump) SessionIDs() []string {
	var out []string
	for _, t := range d.Tables {
		if t.Table != "sessions" {
			continue
		}
		for _, r := range t.Rows {
			if id, ok := r["id"].(string); ok {
				out = append(out, id)
			}
		}
	}
	return out
}

// dumpTables lists what ExportProject copies, parents before children. The
// filter uses ?1 for the project ID.
var dumpTables = []struct {
	table string
	where string
}{
	{"projects", `id = ?1`},
	{"sessions", `project_id = ?1`},
	{"contents", `hash IN (SELECT content_hash FROM messages WHERE session_id IN (` + projectSessions + `))`},
	{"messages", `session_id IN (` + projectSessions + `)`},
	{"blocks", `session_id IN (` + projectSessions + `)`},
	{"block_messages", `session_id IN (` + projectSessions + `)`},
	{"block_sources", `session_id IN (` + projectSessions + `)`},
	{"intents", `session_id IN (` + projectSessions + `)`},
	{"trigger_state", `session_id IN (` + projectSessions + `)`},
	{"events", `session_id IN (` + projectSessions + `)`},
	{"cache_ledger", `session_id IN (` + projectSessions + `)`},
	{"search_idx", `session_id IN (` + projectSessions + `)`},
}

// ListSessionIDs returns the session ids of a project without loading their
// rows, for read-only health checks.
func (s *Store) ListSessionIDs(ctx context.Context, projectID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions WHERE project_id = ?1 ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ExportProject reads every row of a project in one read transaction. It
// returns ErrProjectNotFound when the project does not exist.
func (s *Store) ExportProject(ctx context.Context, id string) (ProjectDump, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProjectDump{}, err
	}
	defer tx.Rollback()

	d := ProjectDump{ProjectID: id}
	for _, t := range dumpTables {
		rows, err := selectRows(ctx, tx, `SELECT * FROM `+t.table+` WHERE `+t.where, id)
		if err != nil {
			return ProjectDump{}, fmt.Errorf("export %s of %s: %w", t.table, id, err)
		}
		if t.table == "projects" && len(rows) == 0 {
			return ProjectDump{}, fmt.Errorf("%w: %s", ErrProjectNotFound, id)
		}
		d.Tables = append(d.Tables, TableRows{Table: t.table, Rows: rows})
	}
	return d, nil
}

// ImportTarget says how an imported project is registered in the destination.
type ImportTarget struct {
	ProjectID string // ID the sessions belong to at the destination
	RootPath  string
	Storage   string // "global" | "local"
}

// ImportProject copies a dump into s in one transaction. Session IDs and
// message refs are kept, so the copy is identical apart from the project ID
// and the storage mode. Every imported session must pass checkInvariants
// before commit; any failure rolls the whole copy back. A session ID already
// present at the destination is an error. It returns the rows written per
// table.
func (s *Store) ImportProject(ctx context.Context, d ProjectDump, dst ImportTarget) (map[string]int64, error) {
	if dst.ProjectID == "" || dst.RootPath == "" {
		return nil, errors.New("store: import target needs a project ID and root path")
	}
	if dst.Storage != "global" && dst.Storage != "local" {
		return nil, fmt.Errorf("store: import storage %q must be global or local", dst.Storage)
	}
	var srcProject Row
	for _, t := range d.Tables {
		if t.Table == "projects" && len(t.Rows) == 1 {
			srcProject = t.Rows[0]
		}
	}
	if srcProject == nil {
		return nil, errors.New("store: dump has no project row")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	created, ok := srcProject["created_at"].(int64)
	if !ok {
		created = now
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO projects(id, root_path, storage, created_at, last_seen_at)
		VALUES (?1, ?2, ?3, ?4, ?5)
		ON CONFLICT(id) DO UPDATE SET
			root_path = excluded.root_path,
			storage = excluded.storage,
			last_seen_at = excluded.last_seen_at`,
		dst.ProjectID, dst.RootPath, dst.Storage, created, now); err != nil {
		return nil, fmt.Errorf("register project %s: %w", dst.ProjectID, err)
	}

	counts := make(map[string]int64, len(dumpTables))
	counts["projects"] = 1
	for _, t := range d.Tables {
		if t.Table == "projects" {
			continue
		}
		for _, r := range t.Rows {
			if t.Table == "sessions" {
				r["project_id"] = dst.ProjectID
			}
			verb := "INSERT"
			if t.Table == "contents" {
				verb = "INSERT OR IGNORE" // shared by content hash
			}
			if err := insertRow(ctx, tx, verb, t.Table, r); err != nil {
				return nil, fmt.Errorf("import %s: %w", t.Table, err)
			}
			counts[t.Table]++
		}
	}

	for _, sid := range d.SessionIDs() {
		if err := checkInvariants(ctx, tx, sid); err != nil {
			return nil, fmt.Errorf("imported session %s: %w", sid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import of %s: %w", dst.ProjectID, err)
	}
	return counts, nil
}

func insertRow(ctx context.Context, tx *sql.Tx, verb, table string, r Row) error {
	cols := make([]string, 0, len(r))
	for c := range r {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	marks := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		marks[i] = "?"
		args[i] = r[c]
	}
	q := fmt.Sprintf("%s INTO %s (%s) VALUES (%s)", verb, table, strings.Join(cols, ", "), strings.Join(marks, ", "))
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

func selectRows(ctx context.Context, q rowSource, query string, args ...any) ([]Row, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(Row, len(cols))
		for i, c := range cols {
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
