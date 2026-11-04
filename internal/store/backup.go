package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// Backup and restore support (spec §18.7). Everything here works on whole
// files or inside one transaction. Nothing overwrites a live database file.

// ErrRestoreConflict reports that a restore would duplicate or overwrite data
// already in the destination. RestoreDumps writes nothing when it returns it.
var ErrRestoreConflict = errors.New("store: restore conflicts with existing data")

// sqliteMagic is the first 16 bytes of every SQLite 3 database file.
var sqliteMagic = []byte("SQLite format 3\x00")

// SchemaVersion is the newest schema this build reads and writes.
func SchemaVersion() int { return schemaVersion }

// VacuumInto writes a consistent copy of the database to path. It runs inside
// a read transaction, so a writer on another handle can keep committing while
// the copy is made. path must not exist.
func (s *Store) VacuumInto(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?1`, path); err != nil {
		return fmt.Errorf("vacuum into %s: %w", path, err)
	}
	return nil
}

// Seal folds the write-ahead log into the main file and switches the database
// back to rollback-journal mode, so the file alone holds all of its data. It
// is meant for scratch copies that are about to be moved.
func (s *Store) Seal(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode = DELETE`); err != nil {
		return fmt.Errorf("journal mode: %w", err)
	}
	return nil
}

// FileProject is one row of projects in a database file.
type FileProject struct {
	ID       string
	RootPath string
	Storage  string
}

// FileInfo describes a database file that passed InspectFile.
type FileInfo struct {
	SchemaVersion int
	Projects      []FileProject
	Sessions      []string
}

// InspectFile validates a database file without migrating or otherwise
// changing it. It checks the SQLite magic, refuses a schema newer than this
// build, requires the acd-ctx tables, runs PRAGMA integrity_check, and runs
// the invariants (I1-I3, I6, I7) for every session. The file is opened on a
// plain handle; callers pass a scratch copy, never the user's file.
func InspectFile(ctx context.Context, path string) (FileInfo, error) {
	head, err := readHead(path, len(sqliteMagic))
	if err != nil || !bytes.Equal(head, sqliteMagic) {
		return FileInfo{}, errors.New("not an SQLite database file")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return FileInfo{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var info FileInfo
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&info.SchemaVersion); err != nil {
		return FileInfo{}, fmt.Errorf("read schema version: %w", err)
	}
	if info.SchemaVersion > schemaVersion {
		return FileInfo{}, fmt.Errorf("database schema v%d is newer than supported v%d", info.SchemaVersion, schemaVersion)
	}

	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('projects','sessions')`).Scan(&tables); err != nil {
		return FileInfo{}, fmt.Errorf("read schema: %w", err)
	}
	if tables != 2 {
		return FileInfo{}, errors.New("not an acd-ctx database (projects or sessions table missing)")
	}

	checks, err := stringColumn(ctx, db, `PRAGMA integrity_check`)
	if err != nil {
		return FileInfo{}, fmt.Errorf("integrity check: %w", err)
	}
	if len(checks) != 1 || checks[0] != "ok" {
		return FileInfo{}, fmt.Errorf("integrity check failed: %s", strings.Join(firstN(checks, 3), "; "))
	}

	rows, err := db.QueryContext(ctx, `SELECT id, root_path, storage FROM projects ORDER BY id`)
	if err != nil {
		return FileInfo{}, fmt.Errorf("read projects: %w", err)
	}
	for rows.Next() {
		var p FileProject
		if err := rows.Scan(&p.ID, &p.RootPath, &p.Storage); err != nil {
			rows.Close()
			return FileInfo{}, fmt.Errorf("scan project: %w", err)
		}
		info.Projects = append(info.Projects, p)
	}
	if err := rows.Close(); err != nil {
		return FileInfo{}, err
	}

	info.Sessions, err = stringColumn(ctx, db, `SELECT id FROM sessions ORDER BY id`)
	if err != nil {
		return FileInfo{}, fmt.Errorf("read sessions: %w", err)
	}
	for _, sid := range info.Sessions {
		if err := checkInvariants(ctx, db, sid); err != nil {
			return FileInfo{}, fmt.Errorf("session %s: %w", sid, err)
		}
	}
	return info, nil
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func firstN(v []string, n int) []string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// RestoreItem is one project of a restore: its rows and the identity they take
// in the destination.
type RestoreItem struct {
	Dump   ProjectDump
	Target ImportTarget
}

// RestoreReport counts the rows a restore removed and wrote. Conflicts lists
// every reason the restore was refused, when it was.
type RestoreReport struct {
	Purged    map[string]int64
	Written   map[string]int64
	Conflicts []string
}

// RestoreDumps applies items in one transaction, so either every project lands
// or none does. Merge (replace=false) refuses when a project ID, a session ID or
// a root path already belongs to another project. Replace first purges each
// target project ID that exists, using the purge core of purge.go, and then
// imports. dryRun runs the whole plan and rolls back, so
// the report shows what would happen. Every imported session must pass the
// invariants before commit.
func (s *Store) RestoreDumps(ctx context.Context, items []RestoreItem, replace, dryRun bool) (RestoreReport, error) {
	rep := RestoreReport{Purged: map[string]int64{}, Written: map[string]int64{}}
	seen := map[string]bool{}
	for _, it := range items {
		id := it.Target.ProjectID
		if id == "" || it.Target.RootPath == "" {
			return rep, errors.New("store: restore target needs a project ID and root path")
		}
		if seen[id] {
			return rep, fmt.Errorf("store: project %s appears twice in the restore", id)
		}
		seen[id] = true
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer tx.Rollback()

	if replace {
		for _, it := range items {
			id := it.Target.ProjectID
			n, err := rowCount(ctx, tx, `SELECT COUNT(*) FROM projects WHERE id = ?1`, id)
			if err != nil {
				return rep, err
			}
			if n == 0 {
				continue
			}
			res, err := purgeTx(ctx, tx, projectSelection(id), false)
			if err != nil {
				return rep, err
			}
			addCounts(rep.Purged, res.Rows)
		}
	}

	for _, it := range items {
		id := it.Target.ProjectID
		// Deliberate deviation from "refuse every existing project ID": a row
		// with no sessions at the same root is only the registration that
		// `project init --local-storage` writes, so importing over it is safe.
		// Any other existing row is a conflict.
		var regRoot string
		err := tx.QueryRowContext(ctx, `SELECT root_path FROM projects WHERE id = ?1`, id).Scan(&regRoot)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return rep, err
		}
		if err == nil {
			n, err := rowCount(ctx, tx, `SELECT COUNT(*) FROM sessions WHERE project_id = ?1`, id)
			if err != nil {
				return rep, err
			}
			switch {
			case n > 0:
				rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("project %s already exists", id))
			case regRoot != it.Target.RootPath:
				rep.Conflicts = append(rep.Conflicts,
					fmt.Sprintf("project %s is registered at %s, not %s", id, regRoot, it.Target.RootPath))
			}
		}
		var owner sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE root_path = ?1`, it.Target.RootPath).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return rep, fmt.Errorf("check root %s: %w", it.Target.RootPath, err)
		}
		if owner.Valid && owner.String != id {
			rep.Conflicts = append(rep.Conflicts,
				fmt.Sprintf("root %s belongs to project %s", it.Target.RootPath, owner.String))
		}
		for _, sid := range it.Dump.SessionIDs() {
			n, err := rowCount(ctx, tx, `SELECT COUNT(*) FROM sessions WHERE id = ?1`, sid)
			if err != nil {
				return rep, err
			}
			if n > 0 {
				rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("session %s already exists", sid))
			}
		}
	}
	if len(rep.Conflicts) > 0 {
		sort.Strings(rep.Conflicts)
		return rep, fmt.Errorf("%w: %s", ErrRestoreConflict, strings.Join(rep.Conflicts, "; "))
	}

	for _, it := range items {
		counts, err := importTx(ctx, tx, it.Dump, it.Target)
		if err != nil {
			return rep, fmt.Errorf("restore project %s: %w", it.Target.ProjectID, err)
		}
		addCounts(rep.Written, counts)
	}
	if dryRun {
		return rep, nil
	}
	if err := tx.Commit(); err != nil {
		return rep, fmt.Errorf("commit restore: %w", err)
	}
	return rep, nil
}

func addCounts(dst, src map[string]int64) {
	for k, v := range src {
		dst[k] += v
	}
}

// importTx is ImportProject on an open transaction: the same inserts and the
// same per-session invariant check, without its own commit.
func importTx(ctx context.Context, tx *sql.Tx, d ProjectDump, dst ImportTarget) (map[string]int64, error) {
	var srcProject Row
	for _, t := range d.Tables {
		if t.Table == "projects" && len(t.Rows) == 1 {
			srcProject = t.Rows[0]
		}
	}
	if srcProject == nil {
		return nil, errors.New("dump has no project row")
	}
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
	counts := map[string]int64{"projects": 1}
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
				verb = "INSERT OR IGNORE"
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
	return counts, nil
}
