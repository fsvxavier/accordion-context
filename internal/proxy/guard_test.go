package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// newServer is newProxy without the HTTP wrapper, so tests can reach the Server.
func newServer(t *testing.T, up *fakeUpstream, listenHost string) (*Server, *store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "acd-ctx.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	u, _ := url.Parse(up.srv.URL)
	s, err := New(Config{Upstream: u, Store: st, Version: "test", ListenHost: listenHost,
		GlobalPath: filepath.Join(t.TempDir(), "absent.yaml")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, st, dbPath
}

func TestHostHeaderGuard(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	s, _, _ := newServer(t, up, "10.20.30.40")
	tests := []struct {
		host string
		ok   bool
	}{
		{"127.0.0.1:8787", true},
		{"localhost:8787", true},
		{"localhost", true},
		{"[::1]:8787", true},
		{"[::1]", true},
		{"10.20.30.40:8787", true}, // the configured listen host
		{"evil.example:8787", false},
		{"attacker.localhost.example", false},
		{"127.0.0.2:8787", false},
		{"", false},
	}
	for _, tc := range tests {
		for _, path := range []string{"/__acd-ctx/health", "/__acd-ctx/status", "/v1/messages"} {
			before := up.count()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if tc.ok {
				if rec.Code == http.StatusForbidden {
					t.Errorf("host %q %s: refused, want accepted", tc.host, path)
				}
				continue
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("host %q %s: status %d, want 403", tc.host, path, rec.Code)
			}
			if up.count() != before {
				t.Errorf("host %q %s: refused request reached upstream", tc.host, path)
			}
		}
	}
}

func TestOriginAndPreflightRefused(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, _ := newProxy(t, up)

	get := func(path string, hdr map[string]string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ps.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("/__acd-ctx/health", nil); c != http.StatusOK {
		t.Fatalf("health without Origin: %d", c)
	}
	if c := get("/__acd-ctx/health", map[string]string{"Origin": "https://evil.example"}); c != http.StatusForbidden {
		t.Fatalf("health with Origin: %d, want 403", c)
	}
	if c := get("/__acd-ctx/status", map[string]string{"Origin": "null"}); c != http.StatusForbidden {
		t.Fatalf("status with Origin null: %d, want 403", c)
	}

	before := up.count()
	req, _ := http.NewRequest(http.MethodOptions, ps.URL+"/v1/messages", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("preflight status %d, want 403", resp.StatusCode)
	}
	if up.count() != before {
		t.Fatal("preflight was relayed upstream")
	}
}

func TestProjectDirHeaderNeverWrites(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, ".acd-ctx")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	s, _, _ := newServer(t, up, "")

	// Existing absolute directory with .acd-ctx but no project-id: resolved read-only.
	if _, ok := s.projectFromDir(root); !ok {
		t.Fatal("existing directory not resolved")
	}
	if _, err := os.Stat(filepath.Join(local, "project-id")); !os.IsNotExist(err) {
		t.Fatalf("project-id written by the proxy path (stat err %v)", err)
	}
	// Relative and missing directories are ignored.
	if _, ok := s.projectFromDir("relative/dir"); ok {
		t.Fatal("relative project dir accepted")
	}
	if _, ok := s.projectFromDir(filepath.Join(root, "missing")); ok {
		t.Fatal("missing project dir accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatal("missing project dir was created")
	}

	// End to end: the request still succeeds and writes nothing.
	ps := httptest.NewServer(s)
	defer ps.Close()
	resp, got := postMessages(t, ps.URL, compressBody(false), map[string]string{headerProject: root})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request with project dir: %d %s", resp.StatusCode, got)
	}
	if _, err := os.Stat(filepath.Join(local, "project-id")); !os.IsNotExist(err) {
		t.Fatal("request created project-id")
	}
}

func TestSessionPropagatesStoreErrors(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	s, _, dbPath := newServer(t, up, "")
	ctx := context.Background()
	p := projectRef{ID: "proj1", Root: "urn:acd-ctx:project:proj1", Storage: "global"}

	// A missing session is created.
	if _, err := s.session(ctx, p, "s_new", "m", 200_000); err != nil {
		t.Fatalf("missing session: %v", err)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions WHERE id = 's_new'"); n != 1 {
		t.Fatalf("sessions created = %d, want 1", n)
	}

	// An existing session whose Snapshot fails for another reason must surface
	// that error. Renaming blocks breaks Snapshot but not the sessions row, so
	// the old code would fall through to CreateSession and fail there with a
	// UNIQUE error instead.
	if _, err := s.session(ctx, p, "s_broken", "m", 200_000); err != nil {
		t.Fatalf("create s_broken: %v", err)
	}
	delete(s.sessions, "s_broken")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`ALTER TABLE blocks RENAME TO blocks_gone`); err != nil {
		t.Fatal(err)
	}
	_, err = s.session(ctx, p, "s_broken", "m", 200_000)
	if err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("session error = %v, want the Snapshot failure", err)
	}
	if _, ok := s.sessions["s_broken"]; ok {
		t.Fatal("session cached after a Snapshot failure")
	}
}

func TestStoreFailureFailsOpen(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	s, st, _ := newServer(t, up, "")
	ps := httptest.NewServer(s)
	defer ps.Close()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	resp, got := postMessages(t, ps.URL, compressBody(false), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(got), "Hello there.") {
		t.Fatalf("store failure did not forward the request: %d %s", resp.StatusCode, got)
	}
	if up.count() != 1 {
		t.Fatalf("upstream requests = %d, want the original forwarded once", up.count())
	}
}

func TestForkRetriesInheritAfterFailure(t *testing.T) {
	up := newFakeUpstream(t, func(n int, _ map[string]json.RawMessage) (int, string, string) {
		if n == 0 {
			return 200, "application/json", `{"id":"msg_a","type":"message","role":"assistant",` +
				`"content":[{"type":"tool_use","id":"toolu_1","name":"compress","input":{"ranges":[{"startRef":"m00002","endRef":"m00004","summary":"Read the parser source and tests in detail; no bug located in the lexer yet."}]}}],` +
				`"stop_reason":"tool_use","usage":{"input_tokens":900,"output_tokens":40}}`
		}
		return 200, "application/json", plainAnswer
	})
	s, _, dbPath := newServer(t, up, "")
	real := s.inherit
	fails := 1
	s.inherit = func(ctx context.Context, child string, f *fork) error {
		if fails > 0 {
			fails--
			return errors.New("injected inherit failure")
		}
		return real(ctx, child, f)
	}
	ps := httptest.NewServer(s)
	defer ps.Close()
	hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-retry"}

	if resp, got := postMessages(t, ps.URL, compressBody(false), hdr); resp.StatusCode != 200 {
		t.Fatalf("compress turn %d: %s", resp.StatusCode, got)
	}
	edited := strings.Replace(compressBody(false), "Status of the fix?", "Status of the build?", 1)
	if resp, got := postMessages(t, ps.URL, edited, hdr); resp.StatusCode != 200 {
		t.Fatalf("fork turn %d: %s", resp.StatusCode, got)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM blocks WHERE active = 1"); n != 1 {
		t.Fatalf("after failed inherit, active blocks = %d, want only the parent's", n)
	}
	// The next turn of the fork retries and inherits.
	if resp, got := postMessages(t, ps.URL, edited, hdr); resp.StatusCode != 200 {
		t.Fatalf("retry turn %d: %s", resp.StatusCode, got)
	}
	if !strings.Contains(string(up.request(3)["messages"]), "<history") {
		t.Fatal("fork did not inherit the parent's block on the next turn")
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM blocks WHERE active = 1"); n != 2 {
		t.Fatalf("active blocks = %d, want parent's block and its copy", n)
	}
	// Once inherited, later turns do not copy again.
	if resp, got := postMessages(t, ps.URL, edited, hdr); resp.StatusCode != 200 {
		t.Fatalf("later turn %d: %s", resp.StatusCode, got)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM blocks WHERE active = 1"); n != 2 {
		t.Fatalf("later turn duplicated blocks: %d", n)
	}
}

func TestUpstreamErrorEventIsNotDoubled(t *testing.T) {
	payload := sseEv("message_start",
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":1}}}`) +
		sseEv("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "text/event-stream", payload
	})
	ps, _, _ := newProxy(t, up)
	_, got := postMessages(t, ps.URL, streamBody(t, ""), nil)
	if n := strings.Count(string(got), "event: error"); n != 1 {
		t.Fatalf("event: error count = %d, want 1 (upstream's own):\n%s", n, got)
	}
}

func TestTruncatedStreamEmitsSSEError(t *testing.T) {
	truncated := sseEv("message_start",
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":1}}}`) +
		sseEv("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		sseEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`)
	complete := truncated +
		sseEv("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		sseEv("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`) +
		sseEv("message_stop", `{"type":"message_stop"}`)

	for _, tc := range []struct {
		name, payload string
		wantErr       bool
	}{
		{"truncated", truncated, true},
		{"complete", complete, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
				return 200, "text/event-stream", tc.payload
			})
			ps, _, _ := newProxy(t, up)
			resp, got := postMessages(t, ps.URL, streamBody(t, ""), nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			body := string(got)
			if !strings.Contains(body, "partial") {
				t.Fatalf("partial deltas not forwarded:\n%s", body)
			}
			hasErr := strings.Contains(body, "event: error\n")
			if hasErr != tc.wantErr {
				t.Fatalf("event: error present = %v, want %v:\n%s", hasErr, tc.wantErr, body)
			}
			if tc.wantErr {
				if !strings.Contains(body, "upstream stream truncated") {
					t.Fatalf("error event lacks reason:\n%s", body)
				}
				if strings.Contains(body, "event: message_stop") {
					t.Fatal("message_stop sent for a truncated stream")
				}
			}
		})
	}
}
