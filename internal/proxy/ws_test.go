package proxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// testLog sends proxy log lines to the test log.
type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Logf("%s", strings.TrimSpace(string(p)))
	return len(p), nil
}

// fakeWS is an upstream WebSocket server. respond returns the events to send for
// each response.create frame (n counts frames on all connections). closeNow ends
// the connection after the events, without a terminal event.
type fakeWS struct {
	srv     *httptest.Server
	token   string // required Authorization header; empty accepts any
	mu      sync.Mutex
	frames  [][]byte
	headers []http.Header
	respond func(n int, frame []byte) (events []string, closeNow bool)
}

func newFakeWS(t *testing.T, respond func(int, []byte) ([]string, bool)) *fakeWS {
	t.Helper()
	f := &fakeWS{respond: respond}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		if f.token != "" && r.Header.Get("Authorization") != f.token {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(64 << 20) // the real server accepts whole transcripts
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			f.mu.Lock()
			n := len(f.frames)
			f.frames = append(f.frames, data)
			f.mu.Unlock()
			events, closeNow := f.respond(n, data)
			for _, ev := range events {
				if err := c.Write(ctx, websocket.MessageText, []byte(ev)); err != nil {
					return
				}
			}
			if closeNow {
				return
			}
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeWS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

func (f *fakeWS) frame(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frames[i]
}

func (f *fakeWS) header(i int) http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headers[i]
}

// newWSProxy starts the proxy against up and returns its base URL, the server and
// the database path.
func newWSProxy(t *testing.T, up *fakeWS, tune func(*Config)) (string, *Server, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "acd-ctx.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	u, _ := url.Parse(up.srv.URL)
	cfg := Config{Upstream: u, Store: st, Version: "test", GlobalPath: filepath.Join(t.TempDir(), "absent.yaml"),
		Logger: slog.New(slog.NewTextHandler(testLog{t}, nil))}
	if tune != nil {
		tune(&cfg)
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ps := httptest.NewServer(h)
	t.Cleanup(ps.Close)
	return ps.URL, h, dbPath
}

// dialWSProxy opens a client connection to the proxy's Responses path.
func dialWSProxy(t *testing.T, base string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/v1/responses",
		&websocket.DialOptions{HTTPHeader: hdr})
}

func mustDial(t *testing.T, base string) *websocket.Conn {
	t.Helper()
	c, _, err := dialWSProxy(t, base, http.Header{"Authorization": []string{"Bearer test-key"}})
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

// readUntilEnd reads client frames until a terminal event, returning their types
// and raw frames.
func readUntilEnd(t *testing.T, c *websocket.Conn) ([]string, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var types, frames []string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("client read after %v: %v", types, err)
		}
		var ev struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &ev)
		types = append(types, ev.Type)
		frames = append(frames, string(data))
		switch ev.Type {
		case "response.completed", "response.incomplete", "response.failed", "error":
			return types, frames
		}
	}
}

// wsCreate builds a response.create frame with a 15-message transcript.
func wsCreate(extra ...any) string {
	var input []any
	for i := 1; i <= 15; i++ {
		input = append(input, obj("role", transcriptRole(i), "content", transcriptText(i)))
	}
	fields := []any{"type", "response.create", "model", "gpt-x", "input", input}
	fields = append(fields, extra...)
	return js(obj(fields...))
}

func evJSON(v map[string]any) string { return js(v) }

// textTurn is the upstream reply of a plain response with resp id id.
func textTurn(id string) []string {
	return []string{
		evJSON(obj("type", "response.created", "sequence_number", 0, "response", obj("id", id, "status", "in_progress"))),
		evJSON(obj("type", "response.in_progress", "sequence_number", 1, "response", obj("id", id, "status", "in_progress"))),
		evJSON(obj("type", "response.output_item.added", "sequence_number", 2, "output_index", 0,
			"item", obj("type", "message", "id", "msg_1", "role", "assistant", "content", arr()))),
		evJSON(obj("type", "response.output_text.delta", "sequence_number", 3, "output_index", 0, "item_id", "msg_1", "delta", plainText)),
		evJSON(obj("type", "response.output_item.done", "sequence_number", 4, "output_index", 0, "item", responsesMessage())),
		evJSON(obj("type", "response.completed", "sequence_number", 5,
			"response", obj("id", id, "status", "completed", "output", arr(responsesMessage()), "usage", responsesUsage()))),
	}
}

func eventTypes(frames []string) []string {
	var out []string
	for _, f := range frames {
		var ev struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(f), &ev)
		out = append(out, ev.Type)
	}
	return out
}

func TestWSPlainTurn(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return textTurn("resp_1"), false })
	base, _, dbPath := newWSProxy(t, up, nil)
	c := mustDial(t, base)

	if err := c.Write(context.Background(), websocket.MessageText, []byte(wsCreate())); err != nil {
		t.Fatal(err)
	}
	types, frames := readUntilEnd(t, c)
	want := []string{"response.created", "response.in_progress", "response.output_item.added",
		"response.output_text.delta", "response.output_item.done", "response.completed"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("client events = %v, want %v", types, want)
	}
	if !strings.Contains(frames[len(frames)-1], `"id":"resp_1"`) {
		t.Fatalf("terminal lost the response id: %s", frames[len(frames)-1])
	}
	if up.count() != 1 {
		t.Fatalf("upstream frames = %d, want 1", up.count())
	}
	sent := string(up.frame(0))
	if !strings.Contains(sent, `"type":"response.create"`) || !strings.Contains(sent, `"compress"`) {
		t.Fatalf("upstream frame lacks type or injected tools: %.300s", sent)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger"); n != 1 {
		t.Fatalf("cache_ledger rows = %d, want 1", n)
	}
}

func TestWSMultipleTurnsOnOneConnection(t *testing.T) {
	up := newFakeWS(t, func(n int, _ []byte) ([]string, bool) {
		return textTurn("resp_" + string(rune('1'+n))), false
	})
	base, _, _ := newWSProxy(t, up, nil)
	c := mustDial(t, base)
	for i, id := range []string{"resp_1", "resp_2"} {
		if err := c.Write(context.Background(), websocket.MessageText, []byte(wsCreate())); err != nil {
			t.Fatal(err)
		}
		_, frames := readUntilEnd(t, c)
		if !strings.Contains(frames[len(frames)-1], `"id":"`+id+`"`) {
			t.Fatalf("turn %d terminal = %s", i, frames[len(frames)-1])
		}
	}
	if up.count() != 2 {
		t.Fatalf("upstream frames = %d, want 2", up.count())
	}
}

func TestWSCompressLoopIsHiddenFromClient(t *testing.T) {
	up := newFakeWS(t, func(n int, frame []byte) ([]string, bool) {
		if n == 0 {
			// Round 1: a preamble, then an internal compress call that is never shown.
			call := obj("type", "function_call", "id", "fc_1", "call_id", "call_1", "name", "compress", "arguments", "")
			done := obj("type", "function_call", "id", "fc_1", "call_id", "call_1", "name", "compress", "arguments", compressArgs, "status", "completed")
			return []string{
				evJSON(obj("type", "response.created", "sequence_number", 1, "response", obj("id", "resp_1", "status", "in_progress"))),
				evJSON(obj("type", "response.output_item.added", "sequence_number", 2, "output_index", 0, "item", call)),
				evJSON(obj("type", "response.function_call_arguments.delta", "sequence_number", 3, "output_index", 0, "item_id", "fc_1", "delta", compressArgs)),
				evJSON(obj("type", "response.function_call_arguments.done", "sequence_number", 4, "output_index", 0, "item_id", "fc_1", "arguments", compressArgs)),
				evJSON(obj("type", "response.output_item.done", "sequence_number", 5, "output_index", 0, "item", done)),
				evJSON(obj("type", "response.completed", "sequence_number", 6,
					"response", obj("id", "resp_1", "status", "completed", "output", arr(done), "usage", responsesUsage()))),
			}, false
		}
		// Round 2: the model answers with text, with output index 0 again upstream.
		return textTurn("resp_2"), false
	})
	base, _, dbPath := newWSProxy(t, up, nil)
	c := mustDial(t, base)

	if err := c.Write(context.Background(), websocket.MessageText, []byte(wsCreate())); err != nil {
		t.Fatal(err)
	}
	types, frames := readUntilEnd(t, c)
	for _, f := range frames {
		if strings.Contains(f, "compress") || strings.Contains(f, "call_1") {
			t.Fatalf("internal call leaked to client: %s", f)
		}
	}
	created := 0
	for _, ty := range types {
		if ty == "response.created" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("client saw %d response.created, want 1: %v", created, types)
	}
	last := frames[len(frames)-1]
	if !strings.Contains(last, `"id":"resp_1"`) || !strings.Contains(last, `"type":"response.completed"`) {
		t.Fatalf("terminal frame = %s", last)
	}
	var term struct {
		Response struct {
			Output []map[string]any `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(last), &term); err != nil || len(term.Response.Output) != 1 ||
		term.Response.Output[0]["type"] != "message" {
		t.Fatalf("terminal output = %s (err %v)", last, err)
	}
	// Sequence numbers and output indexes stay consistent for the client.
	prev := 0.0
	for _, f := range frames {
		var m map[string]any
		_ = json.Unmarshal([]byte(f), &m)
		if seq, ok := m["sequence_number"].(float64); ok {
			if seq <= prev {
				t.Fatalf("sequence_number %v after %v", seq, prev)
			}
			prev = seq
		}
	}

	if up.count() != 2 {
		t.Fatalf("upstream frames = %d, want 2", up.count())
	}
	if cont := string(up.frame(1)); !strings.Contains(cont, "function_call_output") || !strings.Contains(cont, "call_1") {
		t.Fatalf("continuation lacks the tool output: %.400s", cont)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger"); n != 2 {
		t.Fatalf("cache_ledger rows = %d, want 2 (one per round)", n)
	}
}

func TestWSStatefulPassesThroughUntouched(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return textTurn("resp_9"), false })
	base, _, _ := newWSProxy(t, up, nil)
	c := mustDial(t, base)

	frame := js(obj("type", "response.create", "model", "gpt-x", "previous_response_id", "resp_0",
		"input", arr(obj("role", "user", "content", "hi"))))
	if err := c.Write(context.Background(), websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
	types, _ := readUntilEnd(t, c)
	if types[len(types)-1] != "response.completed" {
		t.Fatalf("events = %v", types)
	}
	if got := string(up.frame(0)); got != frame {
		t.Fatalf("stateful frame changed:\n got %s\nwant %s", got, frame)
	}
}

func TestWSNonCreateFrameForwardedUnchanged(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return nil, false })
	base, _, _ := newWSProxy(t, up, nil)
	c := mustDial(t, base)

	cancel := js(obj("type", "response.cancel", "response_id", "resp_1"))
	if err := c.Write(context.Background(), websocket.MessageText, []byte(cancel)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for up.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if up.count() != 1 || string(up.frame(0)) != cancel {
		t.Fatalf("cancel frame not forwarded unchanged: %d frames", up.count())
	}
}

func TestWSAuthFailureReachesClient(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return textTurn("resp_1"), false })
	up.token = "Bearer upstream-key"
	base, _, _ := newWSProxy(t, up, nil)

	_, resp, err := dialWSProxy(t, base, http.Header{"Authorization": []string{"Bearer wrong"}})
	if err == nil {
		t.Fatal("dial with a wrong key succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("client got %v, want 401", resp)
	}

	// The client's own key reaches the upstream handshake.
	c, _, err := dialWSProxy(t, base, http.Header{"Authorization": []string{"Bearer upstream-key"}})
	if err != nil {
		t.Fatalf("dial with the right key: %v", err)
	}
	defer c.CloseNow()
	if got := up.header(0).Get("Authorization"); got != "Bearer wrong" {
		t.Fatalf("first handshake Authorization = %q", got)
	}
	if got := up.header(1).Get("Authorization"); got != "Bearer upstream-key" {
		t.Fatalf("handshake Authorization = %q", got)
	}
}

func TestWSOriginIsRefused(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return textTurn("resp_1"), false })
	base, _, _ := newWSProxy(t, up, nil)
	_, resp, err := dialWSProxy(t, base, http.Header{"Origin": []string{"http://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Origin not refused: err=%v resp=%v", err, resp)
	}
	if up.count() != 0 {
		t.Fatalf("upstream contacted for a refused origin")
	}
}

func TestWSOversizedFrameClosesConnection(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return textTurn("resp_1"), false })
	base, _, _ := newWSProxy(t, up, func(c *Config) { c.WSMaxFrameBytes = 2048 })
	c := mustDial(t, base)

	big := js(obj("type", "response.create", "model", "gpt-x", "input",
		arr(obj("role", "user", "content", strings.Repeat("x", 8192)))))
	if err := c.Write(context.Background(), websocket.MessageText, []byte(big)); err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("oversized frame was answered")
	}
	if up.count() != 0 {
		t.Fatalf("oversized frame reached the upstream")
	}
}

func TestWSUpstreamCloseMidTurnSendsErrorAndCloses(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) {
		return textTurn("resp_1")[:2], true // created and in_progress, then the connection drops
	})
	base, _, _ := newWSProxy(t, up, nil)
	c := mustDial(t, base)

	if err := c.Write(context.Background(), websocket.MessageText, []byte(wsCreate())); err != nil {
		t.Fatal(err)
	}
	types, frames := readUntilEnd(t, c)
	if types[len(types)-1] != "error" || !strings.Contains(frames[len(frames)-1], "upstream closed") {
		t.Fatalf("events = %v last = %s", types, frames[len(frames)-1])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusInternalError {
		t.Fatalf("close status = %v (err %v), want 1011", got, err)
	}
}

func TestWSIdleTimeoutClosesConnection(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return nil, false })
	base, _, _ := newWSProxy(t, up, func(c *Config) { c.WSIdleTimeout = 100 * time.Millisecond })
	c := mustDial(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusNormalClosure {
		t.Fatalf("close status = %v (err %v), want 1000", got, err)
	}
}

func TestWSShutdownSendsGoingAway(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return nil, false })
	base, srv, _ := newWSProxy(t, up, nil)
	c := mustDial(t, base)
	// Let the session register before shutting it down.
	deadline := time.Now().Add(5 * time.Second)
	for {
		srv.wsMu.Lock()
		n := len(srv.wsSessions)
		srv.wsMu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	srv.CloseWebSockets()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
		t.Fatalf("close status = %v (err %v), want 1001", got, err)
	}
}

func TestWSPingIsAnswered(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return nil, false })
	base, _, _ := newWSProxy(t, up, nil)
	c := mustDial(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Ping waits for the pong, which only a concurrent Read delivers.
	go func() { _, _, _ = c.Read(ctx) }()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestWSForeignHostRefused(t *testing.T) {
	up := newFakeWS(t, func(int, []byte) ([]string, bool) { return nil, false })
	base, _, _ := newWSProxy(t, up, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/v1/responses",
		&websocket.DialOptions{Host: "evil.example"})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host not refused: err=%v resp=%v", err, resp)
	}
	if up.count() != 0 {
		t.Fatal("upstream contacted for a refused host")
	}
}
