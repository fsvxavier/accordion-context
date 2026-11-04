package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file is the only place in the codebase that deletes rows (spec §5.2,
// §18.7). The event log and the ledger are otherwise append-only. Every
// DELETE statement lives here, and TestNoDeleteOutsidePurge fails when one
// appears anywhere else. Backup restore (replace mode) and the schema
// migration reuse the functions below.
//
// Every purge selects its targets into temp tables and then runs one set of
// predicates. Dry-run counts with those same predicates and rolls back, so its
// Report equals the report of the real run. Each real purge also bumps
// store_epoch in the same transaction, so a running proxy or plugin drops its
// in-memory state for the affected sessions (see epoch.go in internal/proxy).

// ErrSessionNotFound reports a session ID with no row in sessions.
var ErrSessionNotFound = errors.New("store: session not found")

// Report counts what a purge removed, or would remove in a dry run. Contents
// are shared by content hash, so only contents that no remaining message
// refers to are counted. BytesFreed is the raw size of those contents.
type Report struct {
	Projects   int64
	Sessions   int64
	Messages   int64
	Blocks     int64
	Events     int64
	Contents   int64
	BytesFreed int64
}

// PurgeOpts selects a dry run. A dry run returns the Report of the purge that
// would happen and changes nothing.
type PurgeOpts struct {
	DryRun bool
}

// purgeResult is the per-table row counts of one purge plus the freed bytes.
// Rows uses the table names as keys, so the keys of a project purge are the
// ones PurgeProject has always returned.
type purgeResult struct {
	Rows       map[string]int64
	BytesFreed int64
}

// Report converts the per-table counts into the public Report.
func (r purgeResult) Report() Report {
	return Report{
		Projects:   r.Rows["projects"],
		Sessions:   r.Rows["sessions"],
		Messages:   r.Rows["messages"],
		Blocks:     r.Rows["blocks"],
		Events:     r.Rows["events"],
		Contents:   r.Rows["contents"],
		BytesFreed: r.BytesFreed,
	}
}

// purgeSessionTables lists the per-session tables in foreign-key-safe order.
// Each predicate selects target sessions from temp.purge_target. search_idx has
// no foreign key, so it is listed explicitly: skipping it would leave
// searchable copies behind.
var purgeSessionTables = []struct {
	table string
	where string
}{
	{"search_idx", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"events", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"cache_ledger", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"intents", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"trigger_state", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"block_sources", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"block_messages", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"blocks", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"messages", `session_id IN (SELECT sid FROM temp.purge_target)`},
	{"sessions", `id IN (SELECT sid FROM temp.purge_target)`},
}

// contentsWhere selects a content that a target message refers to (or any
// content, in a full purge) and that no message outside the target refers to.
// It runs after the messages are gone and before them, with the same result.
const contentsWhere = `hash IN (SELECT h FROM temp.purge_hashes)
  AND NOT EXISTS (SELECT 1 FROM messages m
    WHERE m.content_hash = contents.hash
      AND m.session_id NOT IN (SELECT sid FROM temp.purge_target))`

const projectsWhere = `id IN (SELECT pid FROM temp.purge_projects)`

// scopeSessionsSQL selects sessions by project ("" = every project) and by
// last activity: the newest of the session row, its messages, events and cache
// calls. A NULL cutoff applies no age filter. Purge and the CLI share it, so
// the sessions a dry run lists are the ones the real run removes.
const scopeSessionsSQL = `
	SELECT s.id FROM sessions s
	WHERE (?1 = '' OR s.project_id = ?1)
	  AND (?2 IS NULL OR MAX(s.created_at,
	      COALESCE((SELECT MAX(m.created_at) FROM messages m WHERE m.session_id = s.id), 0),
	      COALESCE((SELECT MAX(e.at) FROM events e WHERE e.session_id = s.id), 0),
	      COALESCE((SELECT MAX(c.at) FROM cache_ledger c WHERE c.session_id = s.id), 0)) < ?2)`

// selector resolves a selection inside the purge transaction.
type selector func(context.Context, *sql.Tx) (selection, error)

// selection says which sessions, projects and contents a purge removes. The
// SQL selects IDs and the arguments bind them.
type selection struct {
	sessionsSQL string
	sessionArgs []any
	projectsSQL string // empty: projects are kept
	projectArgs []any
	allContents bool // also remove every content row, even unreferenced ones
}

// PurgeProject deletes all data of one project in a single transaction and
// returns the number of rows removed per table. Contents are content-addressed
// and shared, so a content row goes only when no message refers to it any
// more.
func (s *Store) PurgeProject(ctx context.Context, id string) (map[string]int64, error) {
	res, err := s.runPurge(ctx, projectSelector(id), PurgeOpts{})
	if err != nil {
		return nil, err
	}
	return res.Rows, nil
}

// PurgeProjectReport is PurgeProject with a Report and the dry-run option.
func (s *Store) PurgeProjectReport(ctx context.Context, id string, o PurgeOpts) (Report, error) {
	res, err := s.runPurge(ctx, projectSelector(id), o)
	if err != nil {
		return Report{}, err
	}
	return res.Report(), nil
}

// PurgeSession removes one session completely: its messages, blocks and
// lineage, intents, trigger state, events, cache ledger and search rows, and
// the contents that only it referred to. The project stays. It returns
// ErrSessionNotFound for an unknown ID.
func (s *Store) PurgeSession(ctx context.Context, sid string, o PurgeOpts) (Report, error) {
	res, err := s.runPurge(ctx, sessionSelector(sid), o)
	if err != nil {
		return Report{}, err
	}
	return res.Report(), nil
}

// PurgeOlderThan removes whole sessions whose last activity is before cutoff.
// projectID "" covers every project. A session is never removed partly, so the
// sessions that remain keep their ledgers (invariants I1 to I7). Projects are
// kept, even when they lose all their sessions.
func (s *Store) PurgeOlderThan(ctx context.Context, projectID string, cutoff time.Time, o PurgeOpts) (Report, error) {
	if cutoff.IsZero() {
		return Report{}, errors.New("store: purge older-than needs a cutoff")
	}
	sel := selection{
		sessionsSQL: scopeSessionsSQL,
		sessionArgs: []any{projectID, cutoff.Unix()},
	}
	res, err := s.runPurge(ctx, func(context.Context, *sql.Tx) (selection, error) { return sel, nil }, o)
	if err != nil {
		return Report{}, err
	}
	return res.Report(), nil
}

// PurgeAll removes every project with all of its derived data, then every
// content row that no message refers to. The database is left empty of
// conversations, and the schema and the store epoch remain.
func (s *Store) PurgeAll(ctx context.Context, o PurgeOpts) (Report, error) {
	sel := selection{
		sessionsSQL: `SELECT id FROM sessions`,
		projectsSQL: `SELECT id FROM projects`,
		allContents: true,
	}
	res, err := s.runPurge(ctx, func(context.Context, *sql.Tx) (selection, error) { return sel, nil }, o)
	if err != nil {
		return Report{}, err
	}
	return res.Report(), nil
}

// ScopeSessionIDs lists the sessions that PurgeOlderThan with the same
// arguments would remove, in ID order. projectID "" covers every project, and
// a zero olderThan applies no age filter.
func (s *Store) ScopeSessionIDs(ctx context.Context, projectID string, olderThan time.Time) ([]string, error) {
	// NULL means no age filter. A cutoff of exactly Unix time 0 is a real
	// cutoff that matches nothing, never "every session".
	var cut any
	if !olderThan.IsZero() {
		cut = olderThan.Unix()
	}
	return stringColumn(ctx, s.db, scopeSessionsSQL+` ORDER BY s.id`, projectID, cut)
}

// Epoch returns the store epoch. Every purge increments it in its own
// transaction. A process that caches session state compares it on each request
// and drops that state when it has changed.
func (s *Store) Epoch(ctx context.Context) (int64, error) {
	var e int64
	if err := s.db.QueryRowContext(ctx, `SELECT epoch FROM store_epoch WHERE id = 1`).Scan(&e); err != nil {
		return 0, fmt.Errorf("read store epoch: %w", err)
	}
	return e, nil
}

// Vacuum folds the write-ahead log into the main file, rebuilds the database
// file to return free pages to the OS, and merges the full-text index. It is
// not part of a purge: call it after one.
func (s *Store) Vacuum(ctx context.Context) error {
	steps := []struct{ name, query string }{
		{"checkpoint", `PRAGMA wal_checkpoint(TRUNCATE)`},
		{"vacuum", `VACUUM`},
		{"fts5 optimize", `INSERT INTO search_idx(search_idx) VALUES ('optimize')`},
	}
	for _, st := range steps {
		if _, err := s.db.ExecContext(ctx, st.query); err != nil {
			return fmt.Errorf("vacuum %s: %w", st.name, err)
		}
	}
	return nil
}

// projectSelection targets one project and its sessions. It does not check
// that the project exists.
func projectSelection(id string) selection {
	return selection{
		sessionsSQL: `SELECT id FROM sessions WHERE project_id = ?1`,
		sessionArgs: []any{id},
		projectsSQL: `SELECT id FROM projects WHERE id = ?1`,
		projectArgs: []any{id},
	}
}

func projectSelector(id string) selector {
	return func(ctx context.Context, tx *sql.Tx) (selection, error) {
		n, err := rowCount(ctx, tx, `SELECT COUNT(*) FROM projects WHERE id = ?1`, id)
		if err != nil {
			return selection{}, fmt.Errorf("check project %s: %w", id, err)
		}
		if n == 0 {
			return selection{}, fmt.Errorf("%w: %s", ErrProjectNotFound, id)
		}
		return projectSelection(id), nil
	}
}

func sessionSelector(sid string) selector {
	return func(ctx context.Context, tx *sql.Tx) (selection, error) {
		n, err := rowCount(ctx, tx, `SELECT COUNT(*) FROM sessions WHERE id = ?1`, sid)
		if err != nil {
			return selection{}, fmt.Errorf("check session %s: %w", sid, err)
		}
		if n == 0 {
			return selection{}, fmt.Errorf("%w: %s", ErrSessionNotFound, sid)
		}
		return selection{sessionsSQL: `SELECT ?1`, sessionArgs: []any{sid}}, nil
	}
}

// runPurge opens a transaction, resolves the selection inside it and purges.
// A dry run rolls back; a real run commits only when every delete succeeded.
func (s *Store) runPurge(ctx context.Context, sel selector, o PurgeOpts) (purgeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return purgeResult{}, err
	}
	defer tx.Rollback()
	chosen, err := sel(ctx, tx)
	if err != nil {
		return purgeResult{}, err
	}
	res, err := purgeTx(ctx, tx, chosen, o.DryRun)
	if err != nil {
		return purgeResult{}, err
	}
	if o.DryRun {
		return res, nil
	}
	if err := tx.Commit(); err != nil {
		return purgeResult{}, fmt.Errorf("commit purge: %w", err)
	}
	return res, nil
}

// purgeTx is the purge core. It runs on an open transaction and does not
// commit. It counts every table first, then (unless dry) bumps the store
// epoch and deletes. Each delete must remove exactly the rows counted, or the
// purge fails and the caller rolls back. RestoreDumps calls it for its replace
// mode, so both paths share one set of statements.
func purgeTx(ctx context.Context, tx *sql.Tx, sel selection, dry bool) (purgeResult, error) {
	res := purgeResult{Rows: map[string]int64{}}
	if err := prepareTargets(ctx, tx, sel); err != nil {
		return res, err
	}
	defer dropTargets(tx)

	want := make(map[string]int64, len(purgeSessionTables)+2)
	for _, st := range purgeSessionTables {
		n, err := rowCount(ctx, tx, `SELECT COUNT(*) FROM `+st.table+` WHERE `+st.where)
		if err != nil {
			return res, fmt.Errorf("count %s: %w", st.table, err)
		}
		want[st.table] = n
	}
	var contentRows, contentBytes int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM contents WHERE `+contentsWhere,
	).Scan(&contentRows, &contentBytes); err != nil {
		return res, fmt.Errorf("count contents: %w", err)
	}
	want["contents"] = contentRows
	res.BytesFreed = contentBytes
	hasProjects := sel.projectsSQL != ""
	if hasProjects {
		n, err := rowCount(ctx, tx, `SELECT COUNT(*) FROM projects WHERE `+projectsWhere)
		if err != nil {
			return res, fmt.Errorf("count projects: %w", err)
		}
		want["projects"] = n
	}
	for k, v := range want {
		res.Rows[k] = v
	}
	if dry {
		return res, nil
	}

	if _, err := tx.ExecContext(ctx, `UPDATE store_epoch SET epoch = epoch + 1 WHERE id = 1`); err != nil {
		return res, fmt.Errorf("bump store epoch: %w", err)
	}
	for _, st := range purgeSessionTables {
		if err := deleteCounted(ctx, tx, st.table, `DELETE FROM `+st.table+` WHERE `+st.where, want[st.table]); err != nil {
			return res, err
		}
	}
	if err := deleteCounted(ctx, tx, "contents", `DELETE FROM contents WHERE `+contentsWhere, want["contents"]); err != nil {
		return res, err
	}
	if hasProjects {
		if err := deleteCounted(ctx, tx, "projects", `DELETE FROM projects WHERE `+projectsWhere, want["projects"]); err != nil {
			return res, err
		}
	}
	return res, nil
}

// prepareTargets fills the temp tables that every predicate reads. Message
// hashes are collected before any delete, so contents can be found after the
// messages are gone.
func prepareTargets(ctx context.Context, tx *sql.Tx, sel selection) error {
	dropTargets(tx)
	for _, q := range []string{
		`CREATE TEMP TABLE purge_target (sid TEXT PRIMARY KEY)`,
		`CREATE TEMP TABLE purge_projects (pid TEXT PRIMARY KEY)`,
		`CREATE TEMP TABLE purge_hashes (h TEXT PRIMARY KEY)`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("prepare purge: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO temp.purge_target `+sel.sessionsSQL, sel.sessionArgs...); err != nil {
		return fmt.Errorf("select sessions: %w", err)
	}
	if sel.projectsSQL != "" {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO temp.purge_projects `+sel.projectsSQL, sel.projectArgs...); err != nil {
			return fmt.Errorf("select projects: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO temp.purge_hashes
		SELECT DISTINCT content_hash FROM messages WHERE session_id IN (SELECT sid FROM temp.purge_target)`); err != nil {
		return fmt.Errorf("select contents: %w", err)
	}
	if sel.allContents {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO temp.purge_hashes SELECT hash FROM contents`); err != nil {
			return fmt.Errorf("select all contents: %w", err)
		}
	}
	return nil
}

// dropTargets removes the temp tables. It runs before the caller commits; on
// rollback the temp tables roll back with the transaction.
func dropTargets(tx *sql.Tx) {
	for _, t := range []string{"purge_target", "purge_projects", "purge_hashes"} {
		_, _ = tx.Exec(`DROP TABLE IF EXISTS temp.` + t)
	}
}

// deleteCounted runs one delete and checks that it removed the rows counted for it.
func deleteCounted(ctx context.Context, tx *sql.Tx, table, query string, want int64) error {
	res, err := tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("purge %s: %w", table, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("purge %s: removed %d rows, counted %d", table, n, want)
	}
	return nil
}

// clearSearchKinds removes the search_idx rows of the given kinds. The v2
// migration uses it to rebuild the index from the ledger.
func clearSearchKinds(ctx context.Context, ex execer, kinds ...string) error {
	if len(kinds) == 0 {
		return nil
	}
	args := make([]any, len(kinds))
	marks := make([]string, len(kinds))
	for i, k := range kinds {
		args[i] = k
		marks[i] = "?"
	}
	_, err := ex.ExecContext(ctx, `DELETE FROM search_idx WHERE kind IN (`+strings.Join(marks, ",")+`)`, args...)
	return err
}

// rowCount runs a COUNT query on tx.
func rowCount(ctx context.Context, q rowSource, query string, args ...any) (int64, error) {
	var n int64
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}
