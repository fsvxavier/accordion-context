package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// v3MessagesDDL is the messages table as schema v3 left it: its kind CHECK does not
// know 'opaque'.
const v3MessagesDDL = `CREATE TABLE messages (
  session_id   TEXT NOT NULL REFERENCES sessions(id),
  seq          INTEGER NOT NULL,
  ref          TEXT NOT NULL,
  role         TEXT NOT NULL CHECK (role IN ('system','user','assistant','tool')),
  kind         TEXT NOT NULL CHECK (kind IN ('text','tool_call','tool_result','reasoning')),
  tool_name    TEXT,
  tool_call_id TEXT,
  content_hash TEXT NOT NULL REFERENCES contents(hash),
  tokens       INTEGER NOT NULL,
  ccr_stored   INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq),
  UNIQUE (session_id, ref)
)`

// TestMigrateV3DatabaseToV4KeepsRowsAndAcceptsOpaque rebuilds a current database
// into the v3 shape, then opens it: the rows must survive the rebuild, and the
// opaque kind must be accepted only after the migration.
func TestMigrateV3DatabaseToV4KeepsRowsAndAcceptsOpaque(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acd.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%.60s: %v", q, err)
		}
	}
	exec(`PRAGMA foreign_keys = OFF`)
	exec(`INSERT INTO projects(id, root_path, storage, created_at, last_seen_at) VALUES ('p1','/work/p1','global',1,1)`)
	exec(`INSERT INTO sessions(id, project_id, created_at, model, context_limit, mode, integration)
		VALUES ('s1','p1',1,'m',200000,'proxy','url')`)
	exec(`INSERT INTO contents(hash, size_bytes, data) VALUES ('h1', 4, x'00')`)
	exec(`INSERT INTO messages(session_id, seq, ref, role, kind, content_hash, tokens, ccr_stored, created_at)
		VALUES ('s1', 1, 'm00001', 'user', 'text', 'h1', 10, 0, 1)`)
	exec(strings.Replace(v3MessagesDDL, "CREATE TABLE messages (", "CREATE TABLE messages_v3 (", 1))
	exec(`INSERT INTO messages_v3(session_id, seq, ref, role, kind, tool_name, tool_call_id,
		content_hash, tokens, ccr_stored, created_at)
		SELECT session_id, seq, ref, role, kind, tool_name, tool_call_id,
		content_hash, tokens, ccr_stored, created_at FROM messages`)
	exec(`DROP TABLE messages`)
	exec(`ALTER TABLE messages_v3 RENAME TO messages`)
	exec(`CREATE INDEX idx_msgs_tool ON messages(session_id, tool_call_id)`)
	exec(`CREATE INDEX idx_messages_content ON messages(content_hash)`)
	exec(`PRAGMA user_version = 3`)
	if _, err := db.Exec(`INSERT INTO messages(session_id, seq, ref, role, kind, content_hash, tokens, ccr_stored, created_at)
		VALUES ('s1', 2, 'm00002', 'assistant', 'opaque', 'h1', 3, 0, 1)`); err == nil {
		t.Fatal("v3 layout accepted the opaque kind")
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open v3 database: %v", err)
	}
	defer s.Close()
	if v := userVersion(t, s); v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM messages WHERE kind = 'text'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("text rows after migration = %d (%v), want 1", n, err)
	}
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO messages(session_id, seq, ref, role, kind, content_hash, tokens, ccr_stored, created_at)
		VALUES ('s1', 2, 'm00002', 'assistant', 'opaque', 'h1', 3, 0, 1)`); err != nil {
		t.Fatalf("opaque row rejected after migration: %v", err)
	}
	var broken int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&broken); err != nil || broken != 0 {
		t.Fatalf("foreign key violations after migration: %d (%v)", broken, err)
	}
	var idx int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'messages' AND name IN ('idx_msgs_tool','idx_messages_content')`).Scan(&idx); err != nil || idx != 2 {
		t.Fatalf("messages indexes after migration = %d (%v), want 2", idx, err)
	}
}

// TestMigrateV4KeepsLineageAndForeignKeys migrates a database whose blocks cover
// messages through block_messages, and checks that the lineage survives the
// rebuild, foreign keys are enforced again afterwards, and the invariants hold.
func TestMigrateV4KeepsLineageAndForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acd.db")
	buildV1DB(t, path)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 2; seq++ {
		if _, err := raw.Exec(`INSERT INTO block_messages(session_id, block_id, seq) VALUES ('s1','b1',?)`, seq); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM block_messages`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("block_messages after migration = %d (%v), want 2", n, err)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d (%v) after migration, want 1", fk, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO block_messages(session_id, block_id, seq) VALUES ('s1','b1',99)`); err == nil {
		t.Fatal("block_messages accepted a seq missing from messages: foreign keys are not enforced")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO messages(session_id, seq, ref, role, kind, content_hash, tokens, ccr_stored, created_at)
		VALUES ('s1', 50, 'm00050', 'user', 'bogus', (SELECT hash FROM contents LIMIT 1), 1, 0, 1)`); err == nil {
		t.Fatal("kind CHECK lost in the rebuild")
	}
	if err := s.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("invariants after migration: %v", err)
	}
}

// TestMigrateV4BrokenReferenceRollsBack: a database with an orphan message makes
// the migration fail, and the file stays at its old version with the old table.
func TestMigrateV4BrokenReferenceRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acd.db")
	buildV1DB(t, path)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`INSERT INTO messages(session_id, seq, ref, role, kind, content_hash, tokens, ccr_stored, created_at)
		VALUES ('s1', 40, 'm00040', 'user', 'text', 'nohash', 1, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("migration accepted a database with a broken reference")
	}
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 1 {
		t.Fatalf("user_version after failed migration = %d (%v), want 1", v, err)
	}
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("messages after failed migration = %d (%v), want 4", n, err)
	}
}

// TestOldBackupInspectedThenMigratedRestoresIntoCurrentStore follows the restore
// path on a backup made by an older build: InspectFile accepts the file as it is,
// Open migrates the private copy, and the project imports into a current store.
func TestOldBackupInspectedThenMigratedRestoresIntoCurrentStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	buildV1DB(t, path)

	info, err := InspectFile(ctx, path)
	if err != nil {
		t.Fatalf("InspectFile on an old backup: %v", err)
	}
	if info.SchemaVersion != 1 {
		t.Fatalf("InspectFile schema = %d, want 1 (it must not migrate)", info.SchemaVersion)
	}
	src, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dump, err := src.ExportProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	dst := newMemStore(t)
	rep, err := dst.RestoreDumps(ctx, []RestoreItem{{Dump: dump,
		Target: ImportTarget{ProjectID: "p1", RootPath: "/work/p1", Storage: "global"}}}, false, false)
	if err != nil {
		t.Fatalf("restore: %v (%v)", err, rep.Conflicts)
	}
	if err := dst.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := dst.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id = 's1'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("restored messages = %d (%v), want 3", n, err)
	}
}
