package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// seed creates project and its sessions in the database at path. Each session
// has ten messages, a T1 block over 1-5, a T2 block over that T1, an intent and
// an event, so the copies carry every table with data.
func seed(t *testing.T, path, project, root string, sessions ...string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.EnsureProject(ctx, project, root, "global", 1); err != nil {
		t.Fatal(err)
	}
	for _, sid := range sessions {
		if err := st.CreateSession(ctx, store.Session{
			ID: sid, ProjectID: project, Model: "m", ContextLimit: 200000, Mode: "proxy", Integration: "url",
		}); err != nil {
			t.Fatal(err)
		}
		appendN(t, st, sid, 1, 10)
		if err := st.ApplyCompress(ctx, sid, store.CompressPlan{
			NewBlocks: []store.NewBlock{{ID: "b1", Tier: 1, Topic: "first", Summary: "sum", StartSeq: 1, EndSeq: 5, SrcTokens: 50, SumTokens: 5}},
			Now:       2, TokensAfter: 60,
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.ApplyCompress(ctx, sid, store.CompressPlan{
			NewBlocks:   []store.NewBlock{{ID: "b2", Tier: 2, Summary: "tier2", StartSeq: 1, EndSeq: 5, SrcTokens: 5, SumTokens: 1, Sources: []string{"b1"}}},
			Deactivate:  []string{"b1"},
			ParentOf:    map[string]string{"b1": "b2"},
			Now:         3,
			TokensAfter: 40,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddIntent(ctx, sid, 1, "finish "+sid); err != nil {
			t.Fatal(err)
		}
		if err := st.AppendEvent(ctx, sid, "turn", map[string]int{"n": 1}); err != nil {
			t.Fatal(err)
		}
	}
}

func appendN(t *testing.T, st *store.Store, sid string, from, to int) {
	t.Helper()
	for i := from; i <= to; i++ {
		role := kernel.RoleUser
		if i%2 == 0 {
			role = kernel.RoleAssistant
		}
		if _, err := st.AppendMessage(context.Background(), sid, store.NewMessage{
			Role: role, Kind: kernel.KindText, Text: fmt.Sprintf("%s message %d", sid, i), Tokens: 10,
		}); err != nil {
			t.Fatalf("append %s/%d: %v", sid, i, err)
		}
	}
}

// signature renders every project-owned row, in key order, so two databases
// can be compared row for row.
func signature(t *testing.T, path, project string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sess := `(SELECT id FROM sessions WHERE project_id = ?1)`
	queries := []string{
		`SELECT * FROM sessions WHERE project_id = ?1 ORDER BY id`,
		`SELECT * FROM messages WHERE session_id IN ` + sess + ` ORDER BY session_id, seq`,
		`SELECT * FROM blocks WHERE session_id IN ` + sess + ` ORDER BY session_id, block_id`,
		`SELECT * FROM block_messages WHERE session_id IN ` + sess + ` ORDER BY session_id, block_id, seq`,
		`SELECT * FROM block_sources WHERE session_id IN ` + sess + ` ORDER BY session_id, block_id, source_id`,
		`SELECT * FROM intents WHERE session_id IN ` + sess + ` ORDER BY session_id, intent_id`,
		`SELECT * FROM trigger_state WHERE session_id IN ` + sess + ` ORDER BY session_id`,
		`SELECT * FROM events WHERE session_id IN ` + sess + ` ORDER BY session_id, event_seq`,
		`SELECT * FROM cache_ledger WHERE session_id IN ` + sess + ` ORDER BY session_id, call_seq`,
		`SELECT * FROM search_idx WHERE session_id IN ` + sess + ` ORDER BY session_id, kind, key, body`,
		`SELECT * FROM contents WHERE hash IN (SELECT content_hash FROM messages WHERE session_id IN ` + sess + `) ORDER BY hash`,
	}
	var b strings.Builder
	for _, q := range queries {
		rows, err := db.Query(q, project)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&b, vals...)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		b.WriteString("--\n")
	}
	return b.String()
}

func countRows(t *testing.T, path, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustCreate(t *testing.T, req CreateRequest) CreateResult {
	t.Helper()
	res, err := Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return res
}

func restoreFile(t *testing.T, file string, req RestoreRequest) (RestoreResult, error) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	req.Source = f
	return Restore(context.Background(), req)
}

func globalDest(path string) Destination {
	return Destination{DBPath: path, Storage: StorageGlobal}
}

func TestFullBackupWhileAnotherHandleWrites(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "acd-ctx.db")
	seed(t, dbPath, "p1", "/work/p1", "s1")
	before := countRows(t, dbPath, "messages")

	writer, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writeErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1000; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := writer.AppendMessage(context.Background(), "s1", store.NewMessage{
				Role: kernel.RoleUser, Kind: kernel.KindText, Text: fmt.Sprintf("live %d", i), Tokens: 1,
			}); err != nil {
				writeErr = err
				return
			}
		}
	}()

	out := filepath.Join(dir, "full.db")
	res := mustCreate(t, CreateRequest{Source: dbPath, Out: out})
	close(stop)
	wg.Wait()
	if writeErr != nil {
		t.Fatalf("concurrent writer: %v", writeErr)
	}
	if res.Info.SchemaVersion != store.SchemaVersion() || len(res.Info.Sessions) != 1 {
		t.Fatalf("info = %+v", res.Info)
	}
	got := countRows(t, out, "messages")
	if got < before {
		t.Fatalf("backup has %d messages, fewer than the %d committed before it started", got, before)
	}
	if _, err := store.InspectFile(context.Background(), out); err != nil {
		t.Fatalf("backup not consistent: %v", err)
	}
}

func TestProjectBackupContainsOnlyThatProject(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "acd-ctx.db")
	seed(t, src, "p1", "/work/p1", "s1", "s2")
	seed(t, src, "p2", "/work/p2", "s3")

	out := filepath.Join(dir, "p1.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: out})

	if n := countRows(t, out, "projects"); n != 1 {
		t.Fatalf("projects = %d, want 1", n)
	}
	db, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`SELECT COUNT(*) FROM sessions WHERE project_id <> 'p1'`,
		`SELECT COUNT(*) FROM messages WHERE session_id = 's3'`,
		`SELECT COUNT(*) FROM events WHERE session_id = 's3'`,
		`SELECT COUNT(*) FROM search_idx WHERE session_id = 's3'`,
	} {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s = %d (%v), want 0", q, n, err)
		}
	}
	if n := countRows(t, out, "messages"); n != 20 {
		t.Fatalf("messages = %d, want 20", n)
	}
}

func TestProjectRoundTripIntoFreshDB(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1", "s2")
	backupFile := filepath.Join(dir, "p1.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: backupFile})

	dst := filepath.Join(dir, "fresh.db")
	res, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeMerge, Dest: globalDest(dst)})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.Sessions != 2 || res.Report.Written["messages"] != 20 {
		t.Fatalf("report = %+v sessions=%d", res.Report.Written, res.Sessions)
	}
	if want, got := signature(t, src, "p1"), signature(t, dst, "p1"); want != got {
		t.Fatal("restored rows differ from the source")
	}
	if _, err := store.InspectFile(context.Background(), dst); err != nil {
		t.Fatalf("restored database fails validation: %v", err)
	}
}

func TestRestoreMergeConflictImportsNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")
	backupFile := filepath.Join(dir, "p1.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: backupFile})

	// The same project already present: merge refuses and writes nothing.
	dst := filepath.Join(dir, "dst.db")
	seed(t, dst, "p1", "/work/p1", "other")
	before := signature(t, dst, "p1")
	_, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeMerge, Dest: globalDest(dst)})
	if !errors.Is(err, store.ErrRestoreConflict) {
		t.Fatalf("err = %v, want conflict", err)
	}
	if !strings.Contains(err.Error(), "project p1 already exists") {
		t.Fatalf("conflict does not name the project: %v", err)
	}
	if signature(t, dst, "p1") != before {
		t.Fatal("refused merge changed the destination")
	}

	// A session ID owned by another project: also refused, nothing imported.
	clash := filepath.Join(dir, "clash.db")
	seed(t, clash, "p9", "/work/p9", "s1")
	_, err = restoreFile(t, backupFile, RestoreRequest{Mode: ModeMerge, Dest: globalDest(clash)})
	if !errors.Is(err, store.ErrRestoreConflict) || !strings.Contains(err.Error(), "session s1 already exists") {
		t.Fatalf("session clash err = %v", err)
	}
	if n := countRows(t, clash, "messages"); n != 10 {
		t.Fatalf("messages after refused merge = %d, want the original 10", n)
	}
}

func TestRestoreReplacePurgesAndNeedsYes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")
	backupFile := filepath.Join(dir, "p1.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: backupFile})

	dst := filepath.Join(dir, "dst.db")
	seed(t, dst, "p1", "/work/p1", "old")

	if _, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeReplace, Dest: globalDest(dst)}); !errors.Is(err, ErrNeedYes) {
		t.Fatalf("replace without --yes: err = %v", err)
	}
	res, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeReplace, Yes: true, Dest: globalDest(dst)})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if res.Report.Purged["sessions"] != 1 {
		t.Fatalf("purged = %+v", res.Report.Purged)
	}
	if want, got := signature(t, src, "p1"), signature(t, dst, "p1"); want != got {
		t.Fatal("replaced project differs from the backup")
	}
	if n := countRows(t, dst, "sessions"); n != 1 {
		t.Fatalf("sessions after replace = %d, want only the restored one", n)
	}
}

func TestRestoreReplaceRollsBackOnImportError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dst.db")
	seed(t, dbPath, "p1", "/work/p1", "keep")
	before := signature(t, dbPath, "p1")

	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seed(t, filepath.Join(dir, "src.db"), "p1", "/work/p1", "s1")
	dump, err := st.ExportProject(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	// Break one row so the import fails after the purge has already run.
	for i := range dump.Tables {
		if dump.Tables[i].Table == "messages" {
			dump.Tables[i].Rows[0]["no_such_column"] = 1
		}
	}
	dst, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	_, err = dst.RestoreDumps(context.Background(), []store.RestoreItem{{
		Dump:   dump,
		Target: store.ImportTarget{ProjectID: "p1", RootPath: "/work/p1", Storage: "global"},
	}}, true, false)
	if err == nil {
		t.Fatal("broken import succeeded")
	}
	if signature(t, dbPath, "p1") != before {
		t.Fatal("failed replace left the purged data gone: transaction not rolled back")
	}
}

func TestRestoreDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")
	backupFile := filepath.Join(dir, "p1.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: backupFile})

	dst := filepath.Join(dir, "dst.db")
	res, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeMerge, DryRun: true, Dest: globalDest(dst)})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !res.DryRun || res.Report.Written["messages"] != 10 {
		t.Fatalf("plan = %+v", res.Report.Written)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dry run created the destination (stat err %v)", err)
	}

	// Against an existing database the dry run must leave it unchanged.
	live := filepath.Join(dir, "live.db")
	seed(t, live, "p9", "/work/p9", "other")
	before := signature(t, live, "p9")
	if _, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeMerge, DryRun: true, Dest: globalDest(live)}); err != nil {
		t.Fatalf("dry run against live: %v", err)
	}
	if signature(t, live, "p9") != before || countRows(t, live, "sessions") != 1 {
		t.Fatal("dry run changed the existing destination")
	}
}

// registeredProject writes a project row with no sessions, as project init does.
func registeredProject(t *testing.T, path, id, root string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.EnsureProject(context.Background(), id, root, "local", 1); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreIntoRegistrationOnlyRow(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")
	backupFile := filepath.Join(dir, "p1.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: backupFile})

	// Same root, no sessions: the registration that project init writes. Allowed.
	ok := filepath.Join(dir, "ok.db")
	registeredProject(t, ok, "p1", "/work/p1")
	if _, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeMerge, Dest: globalDest(ok)}); err != nil {
		t.Fatalf("restore over registration: %v", err)
	}
	if n := countRows(t, ok, "messages"); n != 10 {
		t.Fatalf("messages = %d, want 10", n)
	}

	// Registered at another root: a silent re-point would be wrong, so refuse.
	moved := filepath.Join(dir, "moved.db")
	registeredProject(t, moved, "p1", "/elsewhere/p1")
	_, err := restoreFile(t, backupFile, RestoreRequest{Mode: ModeMerge, Dest: globalDest(moved)})
	if !errors.Is(err, store.ErrRestoreConflict) || !strings.Contains(err.Error(), "registered at /elsewhere/p1") {
		t.Fatalf("different root: err = %v", err)
	}
	if n := countRows(t, moved, "sessions"); n != 0 {
		t.Fatalf("refused restore wrote %d sessions", n)
	}
}

func TestRestoreRejectsCorruptAndNewerFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")
	good := filepath.Join(dir, "good.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: good})

	dst := filepath.Join(dir, "dst.db")
	seed(t, dst, "p9", "/work/p9", "other")
	before := signature(t, dst, "p9")

	// Not a database at all.
	text := filepath.Join(dir, "text.db")
	if err := os.WriteFile(text, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreFile(t, text, RestoreRequest{Mode: ModeMerge, Dest: globalDest(dst)}); err == nil {
		t.Fatal("plain text accepted")
	}

	// Header intact, pages damaged.
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 16384 {
		t.Fatalf("backup too small to damage: %d bytes", len(raw))
	}
	for i := 4096; i < 16384; i++ {
		raw[i] = 0xff
	}
	bad := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(bad, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = restoreFile(t, bad, RestoreRequest{Mode: ModeMerge, Dest: globalDest(dst)})
	if err == nil {
		t.Fatal("damaged backup accepted")
	}
	if !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("damaged backup rejected for the wrong reason: %v", err)
	}

	// Newer schema than this build supports.
	newer := filepath.Join(dir, "newer.db")
	mustCopy(t, good, newer)
	db, err := sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", store.SchemaVersion()+1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = restoreFile(t, newer, RestoreRequest{Mode: ModeMerge, Dest: globalDest(dst)})
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("newer schema: err = %v", err)
	}

	if signature(t, dst, "p9") != before {
		t.Fatal("a rejected backup changed the destination")
	}
}

func TestCreateFileRulesAndPermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")

	out := filepath.Join(dir, "out.db")
	mustCreate(t, CreateRequest{Source: src, Out: out})
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
	if _, err := Create(context.Background(), CreateRequest{Source: src, Out: out}); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite without force: err = %v", err)
	}
	mustCreate(t, CreateRequest{Source: src, Out: out, Force: true})

	if _, err := Create(context.Background(), CreateRequest{Source: src, Out: src}); err == nil {
		t.Fatal("output onto the source database accepted")
	}
	if _, err := Create(context.Background(), CreateRequest{Source: src, Out: src + "-wal"}); err == nil {
		t.Fatal("output onto the source journal accepted")
	}

	var buf bytes.Buffer
	res, err := Create(context.Background(), CreateRequest{Source: src, Stdout: &buf})
	if err != nil {
		t.Fatalf("stdout backup: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("SQLite format 3\x00")) || res.Path != "" {
		t.Fatalf("stdout backup is not a database (path %q)", res.Path)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".acd-ctx-backup-") {
			t.Fatalf("temporary directory left behind: %s", e.Name())
		}
	}
}

func mustCopy(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
