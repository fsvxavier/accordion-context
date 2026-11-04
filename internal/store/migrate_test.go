package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// buildV1DB writes a database exactly as schema v1 left it: the v1 base schema,
// messages whose text is not in search_idx, and a block that is. It is written
// with raw SQL, because the current store would index everything it writes.
func buildV1DB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(schemaSQL)
	exec(`INSERT INTO projects(id, root_path, storage, created_at, last_seen_at) VALUES ('p1','/work/p1','global',1,1)`)
	exec(`INSERT INTO sessions(id, project_id, created_at, model, context_limit, mode, integration)
		VALUES ('s1','p1',1,'m',200000,'proxy','url')`)
	exec(`INSERT INTO trigger_state(session_id, low_water_tokens, nudge_pending, nudge_shown_at)
		VALUES ('s1',0,0,0)`)
	for seq := 1; seq <= 3; seq++ {
		text := fmt.Sprintf("message %d about the parser", seq)
		sum := sha256.Sum256([]byte(text))
		hash := hex.EncodeToString(sum[:])
		exec(`INSERT OR IGNORE INTO contents(hash, size_bytes, data) VALUES (?,?,?)`,
			hash, len(text), enc.EncodeAll([]byte(text), nil))
		exec(`INSERT INTO messages(session_id, seq, ref, role, kind, content_hash, tokens, ccr_stored, created_at)
			VALUES ('s1', ?, ?, 'user', 'text', ?, 10, 0, 1)`, seq, fmt.Sprintf("m%05d", seq), hash)
	}
	exec(`INSERT INTO blocks(session_id, block_id, tier, topic, summary, start_seq, end_seq,
		src_tokens, sum_tokens, active, created_at)
		VALUES ('s1','b1',1,'parser','summary of the parser work',1,2,100,10,1,1)`)
	exec(`INSERT INTO search_idx(session_id, kind, key, body) VALUES ('s1','block','b1','parser
summary of the parser work')`)
	exec(`PRAGMA user_version = 1`)
}

func TestMigrateV1DatabaseToV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acd.db")
	buildV1DB(t, path)
	ctx := context.Background()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open v1 database: %v", err)
	}
	if v := userVersion(t, s); v != 2 {
		t.Fatalf("user_version = %d after migration, want 2", v)
	}
	if n := countIndexRows(t, s, indexKindMessage, "m00002"); n != 1 {
		t.Fatalf("backfill indexed m00002 %d times, want 1", n)
	}
	if n := countIndexRows(t, s, indexKindBlock, "b1"); n != 1 {
		t.Fatalf("block b1 has %d index rows after backfill, want exactly 1", n)
	}
	hits, err := s.Search(ctx, "s1", "parser", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 4 {
		t.Fatalf("search for parser found %d hits, want 3 messages and 1 block: %+v", len(hits), hits)
	}
	if !hasHit(hits, indexKindMessage, "m00003") || !hasHit(hits, indexKindBlock, "b1") {
		t.Fatalf("backfilled hits missing: %+v", hits)
	}
	// The v2 column exists: the forced-nudge flag reads and writes on the migrated row.
	if err := s.RequestNudge(ctx, "s1", map[string]any{"spans": []any{}}); err != nil {
		t.Fatalf("request nudge on migrated database: %v", err)
	}
	if forced, err := s.ForcedNudge(ctx, "s1"); err != nil || !forced {
		t.Fatalf("ForcedNudge = %v, %v; want true", forced, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A second open is a no-op: nothing is indexed twice.
	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen v2 database: %v", err)
	}
	defer s.Close()
	if n := countIndexRows(t, s, indexKindMessage, "m00002"); n != 1 {
		t.Fatalf("reopen duplicated m00002: %d rows", n)
	}
	if n := countIndexRows(t, s, indexKindBlock, "b1"); n != 1 {
		t.Fatalf("reopen duplicated b1: %d rows", n)
	}
}

func TestMigrateFreshDatabaseReachesV2(t *testing.T) {
	s := newMemStore(t)
	if v := userVersion(t, s); v != schemaVersion {
		t.Fatalf("fresh user_version = %d, want %d", v, schemaVersion)
	}
	if _, err := s.db.Exec(`SELECT nudge_forced FROM trigger_state LIMIT 0`); err != nil {
		t.Fatalf("fresh database lacks nudge_forced: %v", err)
	}
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
