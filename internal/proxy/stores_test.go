package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// claudeCodeBody is a request shaped like Claude Code's: a system message inside
// the messages array, after the first user message.
func claudeCodeBody(turns ...string) string {
	msgs := []string{
		`{"role":"user","content":"first question"}`,
		`{"role":"system","content":[{"type":"text","text":"reminder: stay on task"}]}`,
	}
	for i, t := range turns {
		if i > 0 {
			msgs = append(msgs, `{"role":"assistant","content":[{"type":"text","text":"Hello there."}]}`)
		}
		msgs = append(msgs, `{"role":"user","content":`+jsonString(t)+`}`)
	}
	return `{"model":"claude-x","max_tokens":64,"system":"be brief","messages":[` + strings.Join(msgs, ",") + `]}`
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A system message inside messages no longer makes the turn fail open: it is
// recorded as one opaque message and sent upstream byte for byte, turn after turn.
func TestSystemMessageInMessagesIsCompacted(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)

	const sys = `{"role":"system","content":[{"type":"text","text":"reminder: stay on task"}]}`
	for i, body := range []string{claudeCodeBody("go"), claudeCodeBody("go", "and again")} {
		resp, got := postMessages(t, ps.URL, body, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("turn %d: status %d: %s", i, resp.StatusCode, got)
		}
		msgs := string(up.request(i)["messages"])
		if !strings.Contains(msgs, sys) {
			t.Fatalf("turn %d: system message not sent verbatim:\n%s", i, msgs)
		}
		if _, ok := up.request(i)["tools"]; !ok {
			t.Fatalf("turn %d: internal tools not injected, the turn failed open", i)
		}
	}
	if n := countRows(t, dbPath, `SELECT count(*) FROM messages WHERE kind = 'opaque'`); n != 1 {
		t.Fatalf("opaque messages = %d, want 1", n)
	}
	if n := countRows(t, dbPath, `SELECT count(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions = %d, want 1 (no fork between turns)", n)
	}
}

// localProject makes a project root with storage: local and returns it.
func localProject(t *testing.T, id string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".acd-ctx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("storage: local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project-id"), []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// newServeProxy is newProxy with the local store opener of serve.
func newServeProxy(t *testing.T, up *fakeUpstream) (*httptest.Server, *Server, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "acd-ctx.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	u, _ := url.Parse(up.srv.URL)
	h, err := New(Config{Upstream: u, Store: st, Version: "test", OpenStore: store.Open,
		GlobalPath: filepath.Join(t.TempDir(), "absent.yaml")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	ps := httptest.NewServer(h)
	t.Cleanup(ps.Close)
	return ps, h, dbPath
}

// One serve process keeps a storage: local project in the project's own
// database, named by X-Acd-Ctx-Project-Dir, and nothing of it in the global one.
func TestLocalStorageProjectUsesProjectDatabase(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, globalDB := newServeProxy(t, up)
	root := localProject(t, "localproj01")
	hdr := map[string]string{headerProject: root}

	for _, body := range []string{claudeCodeBody("go"), claudeCodeBody("go", "more")} {
		if resp, got := postMessages(t, ps.URL, body, hdr); resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, got)
		}
	}
	localDB := localDBPath(root)
	if n := countRows(t, localDB, `SELECT count(*) FROM sessions WHERE project_id = 'localproj01'`); n != 1 {
		t.Fatalf("local sessions = %d, want 1", n)
	}
	if n := countRows(t, localDB, `SELECT count(*) FROM projects WHERE id = 'localproj01' AND storage = 'local'`); n != 1 {
		t.Fatalf("local project rows = %d, want 1", n)
	}
	if n := countRows(t, globalDB, `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("global sessions = %d, want 0", n)
	}

	// A request without the header still goes to the global database.
	if resp, got := postMessages(t, ps.URL, claudeCodeBody("elsewhere"), nil); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	if n := countRows(t, globalDB, `SELECT count(*) FROM sessions`); n != 1 {
		t.Fatalf("global sessions = %d, want 1", n)
	}
}

// A purge of a local database by another handle (clean, restore --mode replace)
// bumps its epoch; the proxy then drops its sessions and creates them again.
func TestLocalStoragePurgeDropsSessions(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, _ := newServeProxy(t, up)
	root := localProject(t, "localproj02")
	hdr := map[string]string{headerProject: root}

	if resp, got := postMessages(t, ps.URL, claudeCodeBody("go"), hdr); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	other, err := store.Open(localDBPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.PurgeProject(context.Background(), "localproj02"); err != nil {
		t.Fatal(err)
	}
	other.Close()
	if n := countRows(t, localDBPath(root), `SELECT count(*) FROM sessions`); n != 0 {
		t.Fatalf("sessions after purge = %d, want 0", n)
	}

	if resp, got := postMessages(t, ps.URL, claudeCodeBody("go", "after purge"), hdr); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	if n := countRows(t, localDBPath(root), `SELECT count(*) FROM sessions WHERE project_id = 'localproj02'`); n != 1 {
		t.Fatalf("sessions after the next request = %d, want 1", n)
	}
}

// The session endpoints find a session kept in a local database.
func TestSessionStatusOfLocalProject(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, _ := newServeProxy(t, up)
	root := localProject(t, "localproj03")
	if resp, got := postMessages(t, ps.URL, claudeCodeBody("go"), map[string]string{headerProject: root}); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	sid := firstSessionID(t, localDBPath(root))
	resp, err := ps.Client().Get(ps.URL + "/__acd-ctx/sessions/" + sid + "/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status endpoint = %d, want 200", resp.StatusCode)
	}
}

func firstSessionID(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id string
	if err := db.QueryRow(`SELECT id FROM sessions LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
