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
