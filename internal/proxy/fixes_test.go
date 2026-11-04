package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// sseEv renders one server-sent event.
func sseEv(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

// sseEvent is one parsed event of a client stream.
type sseEvent struct {
	name string
	data map[string]any
}

func parseClientSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var ev sseEvent
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.data); err != nil {
					t.Fatalf("bad data line %q: %v", line, err)
				}
			}
		}
		out = append(out, ev)
	}
	return out
}

func postURL(t *testing.T, full, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, full, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", full, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// compressRound is the first upstream round of a compress loop: thinking, text,
// then an internal compress call.
const compressRound = `event: message_start
data: {"type":"message_start","message":{"id":"msg_a","type":"message","role":"assistant","content":[],"usage":{"input_tokens":900,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"it"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Compressing early work."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"compress","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00004\","}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"summary\":\"Read the parser source and tests in detail; no bug located in the lexer yet.\"}]}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":40}}

event: message_stop
data: {"type":"message_stop"}

`

// finalRound is a plain text round that ends the answer.
const finalRound = `event: message_start
data: {"type":"message_start","message":{"id":"msg_b","type":"message","role":"assistant","content":[],"usage":{"input_tokens":400,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The fix is in progress."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":9}}

event: message_stop
data: {"type":"message_stop"}

`

func streamBody(t *testing.T, system string) string {
	t.Helper()
	b := strings.Replace(compressBody(true), `"messages":[`, `"system":"`+system+`","messages":[`, 1)
	if system == "" {
		b = compressBody(true)
	}
	return b
}

func TestWindowCarriesRefLabels(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, _ := newProxy(t, up)
	body := strings.Replace(compressBody(false), `"messages":[`, `"system":"be brief","messages":[`, 1)
	if resp, got := postMessages(t, ps.URL, body, nil); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	if string(up.request(0)["system"]) != `"be brief"` {
		t.Fatalf("system prompt altered: %s", up.request(0)["system"])
	}
	window := string(up.request(0)["messages"])
	// The system message is seq 1, so the first user message is m00002 and the last is m00016.
	for _, want := range []string{`[m00002] Please fix the parser bug`, `[m00003] step `, `[m00016] Status of the fix?`} {
		if !strings.Contains(window, want) {
			t.Fatalf("window lacks %q:\n%s", want, window)
		}
	}
	if strings.Contains(string(up.request(0)["system"]), "[m") {
		t.Fatal("system prompt must not carry refs")
	}
}

func TestNudgeListsCompressibleRanges(t *testing.T) {
	spans := []kernel.Span{
		{Start: "m00002", End: "m00008", Tokens: 700},
		{Start: "m00020", End: "m00031", Tokens: 1200},
	}
	got := nudgeText(kernel.VoiceGentle, spans)
	for _, want := range []string{"m00002..m00008 (700 tok)", "m00020..m00031 (1200 tok)", "[m00042]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("nudge lacks %q: %s", want, got)
		}
	}
	if strings.Contains(nudgeText(kernel.VoiceGentle, nil), "tokens aproximados") {
		t.Fatal("empty span list must not add a range section")
	}
}

func TestStreamDeltasArriveBeforeUpstreamEnds(t *testing.T) {
	release := make(chan struct{})
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, sseEv("message_start",
			`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":1}}}`))
		io.WriteString(w, sseEv("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
		io.WriteString(w, sseEv("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"first words"}}`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, sseEv("content_block_stop", `{"type":"content_block_stop","index":0}`))
		io.WriteString(w, sseEv("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`))
		io.WriteString(w, sseEv("message_stop", `{"type":"message_stop"}`))
	}))
	defer upSrv.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	st, err := store.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := url.Parse(upSrv.URL)
	h, err := New(Config{Upstream: u, Store: st, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(h)
	defer ps.Close()

	resp, err := http.Post(ps.URL+"/v1/messages", "application/json", strings.NewReader(compressBody(true)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	seen := make(chan string, 1)
	go func() {
		var got strings.Builder
		for !strings.Contains(got.String(), "first words") {
			line, err := br.ReadString('\n')
			if err != nil {
				break
			}
			got.WriteString(line)
		}
		seen <- got.String()
	}()
	select {
	case got := <-seen:
		if !strings.Contains(got, "first words") {
			t.Fatalf("delta not received: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client received nothing while upstream was still open: stream is buffered")
	}
	close(release)
	rest, _ := io.ReadAll(br)
	if !strings.Contains(string(rest), "message_stop") {
		t.Fatalf("stream did not finish: %q", rest)
	}
}

func TestStreamContinuationKeepsThinkingAndTextVerbatim(t *testing.T) {
	up := newFakeUpstream(t, func(n int, _ map[string]json.RawMessage) (int, string, string) {
		if n == 0 {
			return 200, "text/event-stream", compressRound
		}
		return 200, "text/event-stream", finalRound
	})
	ps, _, _ := newProxy(t, up)

	resp, got := postMessages(t, ps.URL, compressBody(true), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	events := parseClientSSE(t, string(got))

	starts, stops := 0, 0
	var blockTypes []string
	for _, ev := range events {
		switch ev.name {
		case "message_start":
			starts++
		case "message_stop":
			stops++
		case "content_block_start":
			cb := ev.data["content_block"].(map[string]any)
			blockTypes = append(blockTypes, cb["type"].(string))
			if idx := int(ev.data["index"].(float64)); idx != len(blockTypes)-1 {
				t.Fatalf("downstream index %d for block %d", idx, len(blockTypes)-1)
			}
		}
	}
	if starts != 1 || stops != 1 {
		t.Fatalf("message_start=%d message_stop=%d, want 1 each", starts, stops)
	}
	if strings.Join(blockTypes, ",") != "thinking,text,text" {
		t.Fatalf("client blocks = %v", blockTypes)
	}
	if strings.Contains(string(got), "compress") || strings.Contains(string(got), "toolu_1") {
		t.Fatalf("internal tool leaked on the stream: %s", got)
	}
	last := events[len(events)-2]
	if last.name != "message_delta" || last.data["delta"].(map[string]any)["stop_reason"] != "end_turn" {
		t.Fatalf("final message_delta = %v", last)
	}

	// The continuation carries the first round's thinking (with signature) and text verbatim.
	var msgs []struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(up.request(1)["messages"], &msgs); err != nil {
		t.Fatal(err)
	}
	asst := msgs[len(msgs)-2]
	if asst.Role != "assistant" || len(asst.Content) != 3 {
		t.Fatalf("assistant round = %s", up.request(1)["messages"])
	}
	var think, text map[string]string
	_ = json.Unmarshal(asst.Content[0], &think)
	_ = json.Unmarshal(asst.Content[1], &text)
	if think["type"] != "thinking" || think["thinking"] != "plan it" || think["signature"] != "SIG" {
		t.Fatalf("thinking not preserved: %v", think)
	}
	if text["type"] != "text" || text["text"] != "Compressing early work." {
		t.Fatalf("text not preserved: %v", text)
	}
	if up.count() != 2 {
		t.Fatalf("upstream calls = %d, want 2", up.count())
	}
}

func TestStreamFinalRoundNeverLeaksInternalCall(t *testing.T) {
	// Round 1 mixes an internal compress call with a client tool, so it is not continued.
	round := strings.Replace(compressRound, "event: message_delta",
		`event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_read","name":"Read","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: message_delta`, 1)
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "text/event-stream", round
	})
	ps, _, _ := newProxy(t, up)

	resp, got := postMessages(t, ps.URL, compressBody(true), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if strings.Contains(string(got), "compress") || strings.Contains(string(got), "toolu_1") {
		t.Fatalf("internal tool leaked: %s", got)
	}
	if !strings.Contains(string(got), `"toolu_read"`) || !strings.Contains(string(got), `"stop_reason":"tool_use"`) {
		t.Fatalf("client tool call must reach the client with tool_use stop: %s", got)
	}
	// Downstream indexes stay contiguous: the withheld block leaves no gap.
	next := 0
	for _, ev := range parseClientSSE(t, string(got)) {
		if ev.name == "content_block_start" {
			if idx := int(ev.data["index"].(float64)); idx != next {
				t.Fatalf("content index %d, want %d", idx, next)
			}
			next++
		}
	}
	if next != 3 {
		t.Fatalf("content blocks = %d, want 3 (thinking, text, client tool)", next)
	}
	if up.count() != 1 {
		t.Fatalf("round with a client tool must not continue, upstream calls = %d", up.count())
	}
}

func TestEditedTranscriptForksInsteadOfBypassing(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)
	hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-fork"}

	if resp, got := postMessages(t, ps.URL, compressBody(false), hdr); resp.StatusCode != 200 {
		t.Fatalf("first turn %d: %s", resp.StatusCode, got)
	}
	// The client edits the first user message and keeps the same conversation id.
	edited := strings.Replace(compressBody(false), "Please fix the parser bug", "Please fix the lexer bug", 1)
	if resp, got := postMessages(t, ps.URL, edited, hdr); resp.StatusCode != 200 {
		t.Fatalf("edited turn %d: %s", resp.StatusCode, got)
	}
	if up.count() != 2 {
		t.Fatalf("upstream calls = %d", up.count())
	}
	if !strings.Contains(string(up.request(1)["tools"]), `"compress"`) {
		t.Fatalf("edited transcript bypassed compaction: tools missing")
	}
	if !strings.Contains(string(up.request(1)["messages"]), "[m00001] Please fix the lexer bug") {
		t.Fatalf("forked window does not carry the edited transcript")
	}

	// The next turn extends the edited transcript and keeps using the fork.
	next := transcriptBody(false, 17, []string{
		`{"role":"assistant","content":"The fix is in progress."}`,
		`{"role":"user","content":"And now?"}`,
	})
	next = strings.Replace(next, "Please fix the parser bug", "Please fix the lexer bug", 1)
	if resp, got := postMessages(t, ps.URL, next, hdr); resp.StatusCode != 200 {
		t.Fatalf("third turn %d: %s", resp.StatusCode, got)
	}
	if !strings.Contains(string(up.request(2)["tools"]), `"compress"`) {
		t.Fatal("fork lost compaction on the next turn")
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions"); n != 2 {
		t.Fatalf("sessions = %d, want the original and one fork", n)
	}
}

func TestCtxPrefixAllowlist(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)
	target := up.srv.URL

	resp, got := postURL(t, ps.URL+"/ctx/"+target+"/v1/messages", compressBody(false))
	if resp.StatusCode != 200 || string(got) != plainAnswer {
		t.Fatalf("allowed /ctx/ target: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(string(up.request(0)["tools"]), `"compress"`) {
		t.Fatal("/ctx/ messages path must be compacted")
	}

	before := up.count()
	if resp, _ := postURL(t, ps.URL+"/ctx/https://evil.example.com/v1/messages", compressBody(false)); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("disallowed origin status = %d, want 403", resp.StatusCode)
	}
	if resp, _ := postURL(t, ps.URL+"/ctx/ftp://evil.example.com/v1/messages", "{}"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-http target status = %d, want 400", resp.StatusCode)
	}
	if up.count() != before {
		t.Fatal("refused target must not be contacted")
	}

	if resp, got := postURL(t, ps.URL+"/p/proj-ctx/ctx/"+target+"/v1/messages", compressBody(false)); resp.StatusCode != 200 {
		t.Fatalf("project prefix with /ctx/: %d %s", resp.StatusCode, got)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM projects WHERE id = 'proj-ctx'"); n != 1 {
		t.Fatal("project prefix ignored with /ctx/")
	}

	r, err := http.Get(ps.URL + "/ctx/" + target + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), `"data"`) {
		t.Fatalf("/ctx/ passthrough body: %s", b)
	}
}

func TestCtxAllowlistIsIndependentOfDefaultUpstream(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	st, err := store.Open(filepath.Join(t.TempDir(), "allow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dead, _ := url.Parse("http://127.0.0.1:1")
	h, err := New(Config{Upstream: dead, Store: st, Version: "test", UpstreamAllow: []string{up.srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(h)
	defer ps.Close()

	if resp, _ := postURL(t, ps.URL+"/v1/messages", compressBody(false)); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("default upstream must stay the configured one, status %d", resp.StatusCode)
	}
	if resp, got := postURL(t, ps.URL+"/ctx/"+up.srv.URL+"/v1/messages", compressBody(false)); resp.StatusCode != 200 {
		t.Fatalf("allowlisted /ctx/ target: %d %s", resp.StatusCode, got)
	}
}

func TestNewRejectsBadAllowlist(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bad.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := url.Parse("https://api.anthropic.com")
	for _, bad := range []string{"api.anthropic.com", "ftp://x.example", "https://x.example/path", "https://x.example?q=1"} {
		if _, err := New(Config{Upstream: u, Store: st, UpstreamAllow: []string{bad}}); err == nil {
			t.Errorf("allowlist entry %q accepted", bad)
		}
	}
}

func TestParseCtxTarget(t *testing.T) {
	up, tail, err := parseCtxTarget("https://API.example.com:8443/v1/messages?x=1")
	if err != nil {
		t.Fatal(err)
	}
	if originOf(up) != "https://api.example.com:8443" || tail != "/v1/messages?x=1" {
		t.Fatalf("origin %q tail %q", originOf(up), tail)
	}
	for _, bad := range []string{"example.com/v1", "https://user@host/v1", "https:///v1", "gopher://x/y"} {
		if _, _, err := parseCtxTarget(bad); err == nil {
			t.Errorf("target %q accepted", bad)
		}
	}
}

// extendBody appends one raw message to a compressBody-shaped request.
func extendBody(body, msg string) string {
	i := strings.LastIndex(body, "]}")
	return body[:i] + "," + msg + body[i:]
}

func TestCollidingConversationsGetStableForks(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)

	// Same first user message and no conversation header: both clients share the base session.
	a := compressBody(false)
	b := strings.Replace(a, "#b\"", "#B-other\"", 1)
	if a == b {
		t.Fatal("test transcript did not change")
	}
	bExt := extendBody(b, `{"role":"assistant","content":"ok"}`)
	for i, body := range []string{a, b, a, bExt} {
		if resp, got := postMessages(t, ps.URL, body, nil); resp.StatusCode != 200 {
			t.Fatalf("turn %d: %d %s", i, resp.StatusCode, got)
		}
		if !strings.Contains(string(up.request(i)["tools"]), `"compress"`) {
			t.Fatalf("turn %d bypassed compaction", i)
		}
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM sessions"); n != 2 {
		t.Fatalf("sessions = %d, want base and one fork (no per-turn sessions)", n)
	}
}

func TestForkInheritsCompressedBlocks(t *testing.T) {
	up := newFakeUpstream(t, func(n int, _ map[string]json.RawMessage) (int, string, string) {
		if n == 0 {
			return 200, "application/json", `{"id":"msg_a","type":"message","role":"assistant",` +
				`"content":[{"type":"tool_use","id":"toolu_1","name":"compress","input":{"ranges":[{"startRef":"m00002","endRef":"m00004","summary":"Read the parser source and tests in detail; no bug located in the lexer yet."}]}}],` +
				`"stop_reason":"tool_use","usage":{"input_tokens":900,"output_tokens":40}}`
		}
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)
	hdr := map[string]string{"X-Acd-Ctx-Conversation": "conv-inherit"}

	if resp, got := postMessages(t, ps.URL, compressBody(false), hdr); resp.StatusCode != 200 {
		t.Fatalf("compress turn %d: %s", resp.StatusCode, got)
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM blocks WHERE active = 1"); n != 1 {
		t.Fatalf("active blocks before fork = %d", n)
	}
	// Editing the last user message diverges the ledger near its end, far after the block.
	edited := strings.Replace(compressBody(false), "Status of the fix?", "Status of the build?", 1)
	if resp, got := postMessages(t, ps.URL, edited, hdr); resp.StatusCode != 200 {
		t.Fatalf("edited turn %d: %s", resp.StatusCode, got)
	}
	window := string(up.request(2)["messages"])
	if !strings.Contains(window, "<history") {
		t.Fatalf("fork lost its compressed block; window has raw history only:\n%s", window)
	}
	if strings.Contains(window, `#c"`) {
		t.Fatal("covered message visible in the fork window")
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM blocks WHERE active = 1"); n != 2 {
		t.Fatalf("active blocks = %d, want the parent's block and its copy", n)
	}
}

func TestCtxPrefixWithGatewayPathCompacts(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, _ := newProxy(t, up)
	// A gateway serves the Messages API under a path prefix.
	resp, _ := postURL(t, ps.URL+"/ctx/"+up.srv.URL+"/anthropic/v1/messages", compressBody(false))
	if resp.StatusCode != 200 {
		t.Fatalf("gateway path status %d", resp.StatusCode)
	}
	if up.count() != 1 || !strings.Contains(string(up.request(0)["tools"]), `"compress"`) {
		t.Fatalf("gateway messages path not compacted (calls=%d)", up.count())
	}
}

func TestRouteAllowlistAdversarial(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "adv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := url.Parse("https://api.anthropic.com")
	s, err := New(Config{Upstream: u, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	ok := []string{
		"/ctx/https://api.anthropic.com/v1/messages",
		"/ctx/https://API.Anthropic.COM/v1/messages",
	}
	for _, p := range ok {
		if rt, status, msg := s.route(p); status != 0 || rt.up.Host == "" {
			t.Errorf("%q refused: %d %s", p, status, msg)
		}
	}
	bad := []string{
		"/ctx/https://api.anthropic.com@evil.example/v1/messages",
		"/ctx/https://api.anthropic.com:pw@evil.example/v1/messages",
		"/ctx/https://evil.example#@api.anthropic.com/v1/messages",
		"/ctx/https://evil.example?@api.anthropic.com/v1/messages",
		"/ctx/https://evil.example\\@api.anthropic.com/v1/messages",
		"/ctx/https://api.anthropic.com%40evil.example/v1/messages",
		"/ctx/https://api.anthropic.com%2f@evil.example/v1/messages",
		"/ctx/https://api.anthropic.com.evil.example/v1/messages",
		"/ctx/https://evil.example/api.anthropic.com/v1/messages",
		"/ctx/https://api.anthropic.com:443/v1/messages",
		"/ctx/https://api.anthropic.com:8443/v1/messages",
		"/ctx/https://api.anthropic.com./v1/messages",
		"/ctx/http://api.anthropic.com/v1/messages",
		"/ctx/HTTPS://api.anthropic.com/v1/messages",
		"/ctx/https:/api.anthropic.com/v1/messages",
		"/ctx//api.anthropic.com/v1/messages",
		"/ctx/https://127.0.0.1/v1/messages",
		"/ctx/https://[::1]/v1/messages",
		"/ctx/http://2130706433/v1/messages",
		"/ctx/http://localhost:8787/ctx/https://api.anthropic.com/v1/messages",
		"/ctx/https://api.anthropic.com\x00.evil.example/v1/messages",
		"/ctx/https://api.anthropic.com\n.evil.example/v1/messages",
		"/ctx/https://api.anthropic.com evil.example/v1/messages",
		"/ctx/file:///etc/passwd",
		"/ctx/",
	}
	for _, p := range bad {
		if _, status, _ := s.route(p); status == 0 {
			t.Errorf("%q was routed", p)
		}
	}
}

func TestAllowSetRejectsUserinfo(t *testing.T) {
	u, _ := url.Parse("https://api.anthropic.com")
	if _, err := allowSet([]string{"https://user:pw@api.anthropic.com"}, u); err == nil {
		t.Fatal("allowlist entry with userinfo accepted")
	}
}

func TestCtxRedirectNotFollowedAndNoURLInErrors(t *testing.T) {
	var hits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer evil.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	}))
	defer up.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "redir.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	uu, _ := url.Parse(up.URL)
	h, err := New(Config{Upstream: uu, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(h)
	defer ps.Close()
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Get(ps.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || hits != 0 {
		t.Fatalf("redirect status %d, followed hits %d", resp.StatusCode, hits)
	}

	// A dead upstream must not leak the query string (API keys) through the error.
	dead, _ := url.Parse("http://127.0.0.1:1")
	_, err = (&Server{cfg: Config{Upstream: dead}, client: &http.Client{}}).upstreamCall(
		httptest.NewRequest(http.MethodGet, "/v1/x?key=SECRET-KEY", nil), dead, "/v1/x", nil)
	if err == nil || strings.Contains(err.Error(), "SECRET-KEY") {
		t.Fatalf("error leaks the query: %v", err)
	}
}
