package proxy

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// secretText is user text that must never reach metrics, status or an anonymized trace.
const secretText = "TOP-SECRET-PHRASE-7731 please fix the parser"

// sessionIDFrom reads the one session the proxy created in dbPath.
func sessionIDFrom(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id string
	if err := db.QueryRow(`SELECT id FROM sessions ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("no session row: %v", err)
	}
	return id
}

func getBody(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestObservabilityEndpointsAfterTurn(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)
	body := `{"model":"claude-x","max_tokens":64,"messages":[{"role":"user","content":"` + secretText + `"}]}`
	if resp, got := postMessages(t, ps.URL, body, nil); resp.StatusCode != 200 {
		t.Fatalf("turn status %d: %s", resp.StatusCode, got)
	}
	sid := sessionIDFrom(t, dbPath)

	// Metrics: text format, the new families present, no message text, no session id.
	resp, metrics := getBody(t, ps.URL+"/__acd-ctx/metrics")
	if resp.StatusCode != 200 {
		t.Fatalf("metrics status %d", resp.StatusCode)
	}
	for _, name := range []string{
		"acd_ctx_context_tokens_bucket", "acd_ctx_requests_total", "acd_ctx_request_duration_seconds",
		"acd_ctx_upstream_duration_seconds", "acd_ctx_cache_read_ratio", "acd_ctx_compress_total",
		"acd_ctx_recall_hint_total", "acd_ctx_token_calib_factor", "acd_ctx_fold_cache_cost_tokens",
	} {
		if !strings.Contains(string(metrics), name) {
			t.Errorf("metrics lack %s", name)
		}
	}
	if !strings.Contains(string(metrics), `acd_ctx_requests_total{outcome="compacted",protocol="anthropic"}`) {
		t.Errorf("compacted request not counted:\n%s", metrics)
	}
	if strings.Contains(string(metrics), "TOP-SECRET") || strings.Contains(string(metrics), sid) {
		t.Fatal("metrics leak message text or session id")
	}

	// Status: JSON with the sections of spec §20, and no message text.
	resp, raw := getBody(t, ps.URL+"/__acd-ctx/sessions/"+sid+"/status")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	var st map[string]json.RawMessage
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("status is not JSON: %v", err)
	}
	for _, k := range []string{"session", "trigger", "window", "blocks", "intents", "recent_decisions", "cache"} {
		if _, ok := st[k]; !ok {
			t.Errorf("status lacks %q", k)
		}
	}
	if strings.Contains(string(raw), "TOP-SECRET") {
		t.Fatal("status leaks message text")
	}
	if !strings.Contains(string(raw), `"type":"turn"`) {
		t.Errorf("status lacks the turn event: %s", raw)
	}

	// Trace: text verbatim by default, text-free when anonymized.
	resp, tr := getBody(t, ps.URL+"/__acd-ctx/sessions/"+sid+"/trace.jsonl")
	if resp.StatusCode != 200 || !strings.Contains(string(tr), "TOP-SECRET-PHRASE-7731") {
		t.Fatalf("trace status %d, text missing: %s", resp.StatusCode, tr)
	}
	resp, anon := getBody(t, ps.URL+"/__acd-ctx/sessions/"+sid+"/trace.jsonl?anonymize=1")
	if resp.StatusCode != 200 {
		t.Fatalf("anonymized trace status %d", resp.StatusCode)
	}
	if strings.Contains(string(anon), "TOP-SECRET") || !strings.Contains(string(anon), `"sha256"`) {
		t.Fatalf("anonymized trace leaks text or has no digest: %s", anon)
	}

	// Errors: unknown session, bad parameters, wrong method.
	if r, _ := getBody(t, ps.URL+"/__acd-ctx/sessions/s_missing/status"); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown session status = %d", r.StatusCode)
	}
	if r, _ := getBody(t, ps.URL+"/__acd-ctx/sessions/"+sid+"/trace.jsonl?anonymize=maybe"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad anonymize status = %d", r.StatusCode)
	}
	if r, _ := getBody(t, ps.URL+"/__acd-ctx/sessions/"+sid+"/status?decisions=0"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad decisions status = %d", r.StatusCode)
	}
	if r, _ := getBody(t, ps.URL+"/__acd-ctx/sessions/"+sid+"/nope"); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown tail status = %d", r.StatusCode)
	}
	post, err := http.Post(ps.URL+"/__acd-ctx/metrics", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST metrics status = %d", post.StatusCode)
	}
}

func TestObservabilityRefusesNonLoopbackAndForeignHost(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	u, _ := url.Parse(up.srv.URL)
	h, err := New(Config{Upstream: u, Store: newTestStore(t), Version: "test",
		GlobalPath: filepath.Join(t.TempDir(), "absent.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{pathMetrics, pathSessions + "s_x/status", pathSessions + "s_x/trace.jsonl"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
		req.RemoteAddr = "198.51.100.7:4433"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a remote peer: status %d, want 403", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://evil.example"+pathMetrics, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign Host: status %d, want 403", rec.Code)
	}
}

// TestMetricsRegistryIsPerServer builds several handlers in one process: each has
// its own registry, so no registration panics.
func TestMetricsRegistryIsPerServer(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	u, _ := url.Parse(up.srv.URL)
	for i := 0; i < 3; i++ {
		if _, err := New(Config{Upstream: u, Store: newTestStore(t), Version: "test",
			GlobalPath: filepath.Join(t.TempDir(), "absent.yaml")}); err != nil {
			t.Fatalf("New #%d: %v", i, err)
		}
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acd-ctx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestTurnDecisionLogFields checks that every compaction turn writes the decision
// line of spec §20 with its fields, and that the message text stays out of it.
func TestTurnDecisionLogFields(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	var logs bytes.Buffer
	u, _ := url.Parse(up.srv.URL)
	h, err := New(Config{Upstream: u, Store: newTestStore(t), Version: "test",
		GlobalPath: filepath.Join(t.TempDir(), "absent.yaml"),
		Logger:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))})
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(h)
	t.Cleanup(ps.Close)
	body := `{"model":"claude-x","max_tokens":64,"messages":[{"role":"user","content":"` + secretText + `"}]}`
	if resp, got := postMessages(t, ps.URL, body, nil); resp.StatusCode != 200 {
		t.Fatalf("turn status %d: %s", resp.StatusCode, got)
	}
	line := ""
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, `msg="turn decision"`) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no turn decision line in:\n%s", logs.String())
	}
	for _, f := range []string{"row=", "U=", "G=", "S=", "P1=", "P2=", "P3=", "cost=", "benefit=", "recall_hints="} {
		if !strings.Contains(line, f) {
			t.Errorf("decision line lacks %s: %s", f, line)
		}
	}
	if strings.Contains(logs.String(), "TOP-SECRET") {
		t.Fatal("log carries message text")
	}
}
