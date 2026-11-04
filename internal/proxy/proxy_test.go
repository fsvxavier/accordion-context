package proxy

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// fakeUpstream records every request body and answers with scripted replies.
type fakeUpstream struct {
	mu      sync.Mutex
	bodies  []map[string]json.RawMessage
	headers []http.Header
	respond func(n int, body map[string]json.RawMessage) (status int, ctype, payload string)
	srv     *httptest.Server
}

func newFakeUpstream(t *testing.T, respond func(int, map[string]json.RawMessage) (int, string, string)) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{respond: respond}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil && len(raw) > 0 {
			http.Error(w, "bad json", 400)
			return
		}
		f.mu.Lock()
		n := len(f.bodies)
		f.bodies = append(f.bodies, body)
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		if r.URL.Path != "/v1/messages" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[]}`)
			return
		}
		status, ctype, payload := f.respond(n, body)
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		io.WriteString(w, payload)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) request(i int) map[string]json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

func (f *fakeUpstream) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

// newProxy starts the handler against up with a fresh SQLite file.
func newProxy(t *testing.T, up *fakeUpstream) (*httptest.Server, *store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "acd-ctx.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	u, _ := url.Parse(up.srv.URL)
	h, err := New(Config{Upstream: u, Store: st, Version: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ps := httptest.NewServer(h)
	t.Cleanup(ps.Close)
	return ps, st, dbPath
}

func postMessages(t *testing.T, base, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", "test-key-123")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func countRows(t *testing.T, dbPath, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

const plainAnswer = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x",` +
	`"content":[{"type":"text","text":"Hello there."}],"stop_reason":"end_turn",` +
	`"usage":{"input_tokens":10,"cache_read_input_tokens":5,"cache_creation_input_tokens":0,"output_tokens":3}}`

func TestPlainTurn(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)

	body := `{"model":"claude-x","max_tokens":64,"system":"be brief","messages":[{"role":"user","content":"hello"}]}`
	resp, got := postMessages(t, ps.URL, body, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	if string(got) != plainAnswer {
		t.Fatalf("client body changed:\n%s", got)
	}
	if up.count() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.count())
	}
	if up.headers[0].Get("x-api-key") != "test-key-123" || up.headers[0].Get("anthropic-version") == "" {
		t.Fatalf("auth or version header not forwarded")
	}
	if up.headers[0].Get("X-Acd-Ctx-Project-Dir") != "" {
		t.Fatalf("internal headers must not be forwarded")
	}
	var tools []map[string]any
	if err := json.Unmarshal(up.request(0)["tools"], &tools); err != nil || len(tools) != 2 {
		t.Fatalf("tools not injected: %v %s", err, up.request(0)["tools"])
	}
	if string(up.request(0)["system"]) != `"be brief"` {
		t.Fatalf("system prompt altered: %s", up.request(0)["system"])
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger"); n != 1 {
		t.Fatalf("cache_ledger rows = %d, want 1", n)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM messages"); n != 2 {
		t.Fatalf("ledger messages = %d, want 2 (system + user)", n)
	}

	// Non-messages endpoints pass through untouched.
	r2, err := http.Get(ps.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if !strings.Contains(string(b2), `"data"`) {
		t.Fatalf("passthrough body: %s", b2)
	}
}

// compressBody is a 15-message transcript; the last user message is m00015.
func compressBody(stream bool) string {
	return transcriptBody(stream, 15, nil)
}

// transcriptBody builds n alternating messages (odd = user). extra, when set,
// is appended after them as raw JSON messages.
func transcriptBody(stream bool, n int, extra []string) string {
	var msgs []string
	for i := 1; i <= n; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		text := "step " + strings.Repeat("reading the parser source and noting details ", 8)
		if i == 1 {
			text = "Please fix the parser bug in internal/parse."
		}
		if i == 15 {
			text = "Status of the fix?"
		}
		msgs = append(msgs, `{"role":"`+role+`","content":"`+text+` #`+string(rune('a'+i))+`"}`)
	}
	msgs = append(msgs, extra...)
	s := "false"
	if stream {
		s = "true"
	}
	return `{"model":"claude-x","max_tokens":64,"stream":` + s + `,"messages":[` + strings.Join(msgs, ",") + `]}`
}

func TestCompressLoopIsHiddenFromClient(t *testing.T) {
	up := newFakeUpstream(t, func(n int, body map[string]json.RawMessage) (int, string, string) {
		if n == 0 {
			return 200, "application/json", `{"id":"msg_a","type":"message","role":"assistant",` +
				`"content":[{"type":"text","text":"Compressing early work."},` +
				`{"type":"tool_use","id":"toolu_1","name":"compress","input":{"ranges":[{"startRef":"m00002","endRef":"m00004","summary":"Read parser source; no bug found yet."}]}}],` +
				`"stop_reason":"tool_use","usage":{"input_tokens":900,"output_tokens":40}}`
		}
		return 200, "application/json", `{"id":"msg_b","type":"message","role":"assistant",` +
			`"content":[{"type":"text","text":"The fix is in progress."}],"stop_reason":"end_turn",` +
			`"usage":{"input_tokens":400,"output_tokens":9}}`
	})
	ps, _, dbPath := newProxy(t, up)

	resp, got := postMessages(t, ps.URL, compressBody(false), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	var out struct {
		Content []map[string]any `json:"content"`
		Stop    string           `json:"stop_reason"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("client body not JSON: %v\n%s", err, got)
	}
	if len(out.Content) != 1 || out.Content[0]["text"] != "The fix is in progress." || out.Stop != "end_turn" {
		t.Fatalf("client saw internal round: %s", got)
	}
	if strings.Contains(string(got), "compress") || strings.Contains(string(got), "toolu_1") {
		t.Fatalf("internal tool leaked: %s", got)
	}
	if up.count() != 2 {
		t.Fatalf("upstream calls = %d, want 2", up.count())
	}

	// The continuation carries the tool_result for the compress call.
	var msgs []map[string]any
	if err := json.Unmarshal(up.request(1)["messages"], &msgs); err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	blocks, _ := last["content"].([]any)
	if last["role"] != "user" || len(blocks) != 1 {
		t.Fatalf("continuation tail = %v", last)
	}
	res := blocks[0].(map[string]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != "toolu_1" || res["is_error"] != nil {
		t.Fatalf("tool_result = %v", res)
	}
	if prev := msgs[len(msgs)-2]; prev["role"] != "assistant" {
		t.Fatalf("assistant round missing before tool_result")
	}

	// Next client turn: the transcript grows and resends the compressed range.
	// The window must carry the history envelope and drop the covered messages.
	followUp := transcriptBody(false, 17, []string{
		`{"role":"assistant","content":"The fix is in progress."}`,
		`{"role":"user","content":"And now?"}`,
	})
	resp, got = postMessages(t, ps.URL, followUp, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("second turn status %d: %s", resp.StatusCode, got)
	}
	if up.count() != 3 {
		t.Fatalf("upstream calls after second turn = %d, want 3", up.count())
	}
	window := string(up.bodies[2]["messages"])
	if !strings.Contains(window, "<history") {
		t.Fatalf("window lacks history envelope:\n%s", window)
	}
	for _, covered := range []string{`#c"`, `#d"`, `#e"`} {
		if strings.Contains(window, covered) {
			t.Fatalf("covered message %s still visible in window", covered)
		}
	}
	if !strings.Contains(window, `#f"`) {
		t.Fatalf("uncovered message #f missing from window")
	}

	// The compression is confirmed by a stored block, not by model text.
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM blocks WHERE active = 1"); n != 1 {
		t.Fatalf("active blocks = %d, want 1", n)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger WHERE after_compress = 1"); n != 1 {
		t.Fatalf("cache_ledger after_compress rows = %d, want 1", n)
	}
}

func TestStreamingTurnIsReplayedVerbatim(t *testing.T) {
	sse := "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_s","type":"message","role":"assistant","content":[],"usage":{"input_tokens":12,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Streamed."}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "text/event-stream", sse
	})
	ps, _, dbPath := newProxy(t, up)

	resp, got := postMessages(t, ps.URL, compressBody(true), map[string]string{
		"X-Acd-Ctx-Conversation": "conv-stream-1",
		"Accept-Encoding":        "br, zstd",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !bytes.Equal(got, []byte(sse)) {
		t.Fatalf("stream not replayed verbatim:\n%s", got)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type = %q", resp.Header.Get("Content-Type"))
	}
	if string(up.request(0)["stream"]) != "true" {
		t.Fatalf("stream flag lost upstream")
	}
	if up.headers[0].Get("Accept-Encoding") == "br, zstd" {
		t.Fatalf("client Accept-Encoding forwarded")
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger"); n != 1 {
		t.Fatalf("cache_ledger rows = %d, want 1", n)
	}
}

func TestHealthStatusAndProjectPrefix(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)

	for _, p := range []string{"/__acd-ctx/health", "/__acd-ctx/status"} {
		r, err := http.Get(ps.URL + p)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, r)
		}
		r.Body.Close()
	}

	// /p/<id>/ selects the project and is stripped before forwarding.
	_, _ = postMessages(t, ps.URL+"/p/proj-abc", `{"model":"m","messages":[{"role":"user","content":"x"}]}`, nil)
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM projects WHERE id = 'proj-abc'"); n != 1 {
		t.Fatalf("project row for URL prefix missing")
	}
	bad, err := http.Post(ps.URL+"/p/bad%20id/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid project id status = %d", bad.StatusCode)
	}
}

func TestCheckLoopback(t *testing.T) {
	ok := []string{"127.0.0.1:8787", "[::1]:8787", "localhost:8787"}
	bad := []string{"0.0.0.0:8787", ":8787", "192.168.1.10:8787", "example.com:8787"}
	for _, a := range ok {
		if err := CheckLoopback(a); err != nil {
			t.Errorf("%s rejected: %v", a, err)
		}
	}
	for _, a := range bad {
		if err := CheckLoopback(a); err == nil {
			t.Errorf("%s accepted", a)
		}
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing store accepted")
	}
	u, _ := url.Parse("ftp://x")
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := New(Config{Store: st, Upstream: u}); err == nil {
		t.Fatal("non-http upstream accepted")
	}
}
