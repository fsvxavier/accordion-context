package acdctx_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fsvxavier/accordion-context/pkg/acdctx"
)

// fakeUpstream records every request body and answers with handle, which gets the
// 1-based request number.
type fakeUpstream struct {
	mu     sync.Mutex
	bodies []string
	srv    *httptest.Server
}

func newFakeUpstream(t *testing.T, handle func(n int, body string, w http.ResponseWriter)) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(b))
		n := len(f.bodies)
		f.mu.Unlock()
		handle(n, string(b), w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

const textAnswer = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-test",` +
	`"content":[{"type":"text","text":"hello from upstream"}],"stop_reason":"end_turn",` +
	`"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":4}}`

// compressAnswer is the model asking for a compress call over the messages 2..13.
const compressAnswer = `{"id":"msg_2","type":"message","role":"assistant","model":"claude-test",` +
	`"content":[{"type":"tool_use","id":"tu_1","name":"compress","input":{"ranges":[` +
	`{"startRef":"m00002","endRef":"m00013","summary":"SUMMARY-MARK the setup messages were reviewed and the task was closed"}]}}],` +
	`"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":9000,"output_tokens":40}}`

func newTransportClient(t *testing.T, upstreams ...string) *http.Client {
	t.Helper()
	dir := t.TempDir()
	tr, err := acdctx.NewTransport(nil, acdctx.Options{
		DBPath:     dir + "/acd-ctx.db",
		Cwd:        dir,
		ConfigPath: dir + "/absent.yaml",
		Upstreams:  upstreams,
	})
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return &http.Client{Transport: tr}
}

// messagesBody builds an Anthropic Messages request with n alternating messages.
func messagesBody(t *testing.T, n int, stream bool) string {
	t.Helper()
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]msg, 0, n)
	for i := 1; i <= n; i++ {
		role, name := "user", "user"
		if i%2 == 0 {
			role, name = "assistant", "assistant"
		}
		msgs = append(msgs, msg{Role: role, Content: bigText(i, name)})
	}
	b, err := json.Marshal(map[string]any{
		"model": "claude-test", "max_tokens": 256, "stream": stream, "messages": msgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func post(t *testing.T, c *http.Client, url, body string) (int, string) {
	t.Helper()
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

func TestTransportPlainTurn(t *testing.T) {
	up := newFakeUpstream(t, func(_ int, _ string, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, textAnswer)
	})
	c := newTransportClient(t, up.srv.URL)

	status, body := post(t, c, up.srv.URL+"/v1/messages", messagesBody(t, 4, false))
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, "hello from upstream") {
		t.Fatalf("answer not relayed: %s", body)
	}
	got := up.received()
	if len(got) != 1 {
		t.Fatalf("upstream got %d requests, want 1", len(got))
	}
	// The proxy path injects the compress and decompress tools into the request.
	if !strings.Contains(got[0], `"compress"`) || !strings.Contains(got[0], `"decompress"`) {
		t.Fatalf("request was not compacted; tools missing: %.200s", got[0])
	}
}

func TestTransportCompressLoopIsServerSide(t *testing.T) {
	up := newFakeUpstream(t, func(n int, body string, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			fmt.Fprint(w, compressAnswer)
			return
		}
		fmt.Fprint(w, textAnswer)
	})
	c := newTransportClient(t, up.srv.URL)

	status, body := post(t, c, up.srv.URL+"/v1/messages", messagesBody(t, 30, false))
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if strings.Contains(body, "tool_use") || strings.Contains(body, `"compress"`) {
		t.Fatalf("internal tool call leaked to the client: %.300s", body)
	}
	if !strings.Contains(body, "hello from upstream") {
		t.Fatalf("final answer missing: %.300s", body)
	}

	got := up.received()
	if len(got) != 2 {
		t.Fatalf("upstream got %d requests, want 2 (tool round, then answer)", len(got))
	}
	second := got[1]
	if !strings.Contains(second, "Recorded 1 block") {
		t.Fatalf("compress was not recorded: %.300s", second[len(second)-400:])
	}
	if !strings.Contains(second, "tool_result") || !strings.Contains(second, "SUMMARY-MARK") {
		t.Fatalf("second round does not carry the compress result and summary")
	}
	// The request is JSON, so markers are matched without their angle brackets.
	// The loop extends the round it is in, so the originals stay in that request
	// (as in the proxy). The next client turn gets the reduced window.
	status, body = post(t, c, up.srv.URL+"/v1/messages", messagesBody(t, 31, false))
	if status != http.StatusOK {
		t.Fatalf("follow-up status %d: %s", status, body)
	}
	got = up.received()
	if len(got) != 3 {
		t.Fatalf("upstream got %d requests after the follow-up, want 3", len(got))
	}
	third := got[2]
	// The request is JSON, so markers are matched without their angle brackets.
	if strings.Contains(third, "assistant-02") {
		t.Fatal("compressed original is still sent in the next turn")
	}
	if !strings.Contains(third, "SUMMARY-MARK") || !strings.Contains(third, "assistant-14") {
		t.Fatal("next turn lacks the summary or the recent zone")
	}
}

func TestTransportStreamsServerSentEvents(t *testing.T) {
	const sse = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_3","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streamed words"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	up := newFakeUpstream(t, func(_ int, _ string, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	})
	c := newTransportClient(t, up.srv.URL)

	status, body := post(t, c, up.srv.URL+"/v1/messages", messagesBody(t, 4, true))
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	for _, want := range []string{"text_delta", "streamed words", "message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream lacks %q: %s", want, body)
		}
	}
}

func TestTransportLeavesOtherOriginsUntouched(t *testing.T) {
	up := newFakeUpstream(t, func(_ int, _ string, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, textAnswer)
	})
	// The upstream is not in the allowlist, so the request must pass unchanged.
	c := newTransportClient(t, "https://api.anthropic.com")

	payload := messagesBody(t, 4, false)
	status, _ := post(t, c, up.srv.URL+"/v1/messages", payload)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	got := up.received()
	if len(got) != 1 || got[0] != payload {
		t.Fatal("request to an unlisted origin was modified")
	}
}

func TestNewTransportRejectsBadUpstream(t *testing.T) {
	dir := t.TempDir()
	_, err := acdctx.NewTransport(nil, acdctx.Options{
		DBPath: dir + "/acd-ctx.db", Upstreams: []string{"not a url"},
	})
	if err == nil {
		t.Fatal("NewTransport accepted a relative upstream")
	}
}

func TestHandlerEmbedsProxy(t *testing.T) {
	up := newFakeUpstream(t, func(_ int, _ string, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, textAnswer)
	})
	dir := t.TempDir()
	h, err := acdctx.Handler(acdctx.Options{
		DBPath: dir + "/acd-ctx.db", Cwd: dir, ConfigPath: dir + "/absent.yaml", Upstreams: []string{up.srv.URL},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	defer h.Close()
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/__acd-ctx/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}

	status, body := post(t, http.DefaultClient, srv.URL+"/ctx/"+up.srv.URL+"/v1/messages", messagesBody(t, 4, false))
	if status != http.StatusOK || !strings.Contains(body, "hello from upstream") {
		t.Fatalf("embedded proxy: %d %s", status, body)
	}
	if got := up.received(); len(got) != 1 || !strings.Contains(got[0], `"compress"`) {
		t.Fatal("embedded proxy did not compact the request")
	}
}
