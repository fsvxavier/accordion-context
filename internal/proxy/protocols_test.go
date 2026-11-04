package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// anyUpstream answers on every path and records each request, so that the
// Chat Completions, Responses and Gemini endpoints can share one fake.
type anyUpstream struct {
	srv     *httptest.Server
	mu      sync.Mutex
	paths   []string
	bodies  [][]byte
	respond func(n int, body []byte) (status int, ctype, payload string)
}

func newAnyUpstream(t *testing.T, respond func(int, []byte) (int, string, string)) *anyUpstream {
	t.Helper()
	up := &anyUpstream{respond: respond}
	up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		up.mu.Lock()
		n := len(up.bodies)
		up.paths = append(up.paths, r.URL.RequestURI())
		up.bodies = append(up.bodies, raw)
		up.mu.Unlock()
		status, ctype, payload := up.respond(n, raw)
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(up.srv.Close)
	return up
}

func (u *anyUpstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.bodies)
}

func (u *anyUpstream) body(i int) []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.bodies[i]
}

// newAnyProxy starts the handler against up with a fresh SQLite file.
func newAnyProxy(t *testing.T, up *anyUpstream) (*httptest.Server, string) {
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
	return ps, dbPath
}

func postDialect(t *testing.T, full, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, full, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key-456")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", full, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// Small builders for the fixtures. Each one marshals a Go value, so no JSON is
// escaped by hand.
func obj(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func arr(items ...any) []any {
	if items == nil {
		return []any{}
	}
	return items
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// dataLine renders one unnamed SSE event.
func dataLine(v any) string { return "data: " + js(v) + "\n\n" }

const (
	plainText    = "The fix is in progress."
	compressSumm = "Read parser source and tests; no bug located in the lexer yet."
)

// compressArgs is the arguments text of a compress call that covers m00002..m00004.
var compressArgs = js(obj("ranges", arr(obj("startRef", "m00002", "endRef", "m00004", "summary", compressSumm))))

// transcriptText returns the text of message i (1-based) of a 15-message transcript.
func transcriptText(i int) string {
	switch i {
	case 1:
		return "Please fix the parser bug in internal/parse."
	case 15:
		return "Status of the fix?"
	}
	// Long enough that the first three messages cover more than 5000 tokens, the
	// minimum the compress tool asks for in a range.
	return fmt.Sprintf("step %d reading the parser source %s", i, strings.Repeat("and noting details ", 1000))
}

// roleOf alternates user (odd) and assistant (even), starting with the user.
func transcriptRole(i int) string {
	if i%2 == 1 {
		return "user"
	}
	return "assistant"
}

func streamCT(stream bool) string {
	if stream {
		return "text/event-stream"
	}
	return "application/json"
}

// dialect describes one API dialect for the httptest suite.
type dialect struct {
	name     string
	endpoint func(stream bool) string
	request  func(stream bool) string
	plain    func(stream bool) string // upstream answer with text only
	compress func(stream bool) string // upstream answer with one compress call
	// check inspects the round-2 upstream body, which must carry the tool result.
	check func(t *testing.T, body map[string]any)
}

// lastTwo returns the last two items of a list field, failing the test otherwise.
func lastTwo(t *testing.T, body map[string]any, key string) (prev, last map[string]any) {
	t.Helper()
	list, _ := body[key].([]any)
	if len(list) < 2 {
		t.Fatalf("%s has %d items, want at least 2", key, len(list))
	}
	prev, _ = list[len(list)-2].(map[string]any)
	last, _ = list[len(list)-1].(map[string]any)
	return prev, last
}

func openaiUsage() map[string]any {
	return obj("prompt_tokens", 400, "completion_tokens", 9, "total_tokens", 409,
		"prompt_tokens_details", obj("cached_tokens", 100))
}

func openaiChunk(delta, finish any) string {
	return dataLine(obj("id", "c1", "object", "chat.completion.chunk", "model", "gpt-x",
		"choices", arr(obj("index", 0, "delta", delta, "finish_reason", finish))))
}

var openaiDialect = dialect{
	name:     "openai",
	endpoint: func(bool) string { return "/v1/chat/completions" },
	request: func(stream bool) string {
		var msgs []any
		for i := 1; i <= 15; i++ {
			msgs = append(msgs, obj("role", transcriptRole(i), "content", transcriptText(i)))
		}
		return js(obj("model", "gpt-x", "stream", stream, "messages", msgs))
	},
	plain: func(stream bool) string {
		if !stream {
			return js(obj("id", "c1", "object", "chat.completion", "model", "gpt-x",
				"choices", arr(obj("index", 0, "message", obj("role", "assistant", "content", plainText), "finish_reason", "stop")),
				"usage", openaiUsage()))
		}
		return openaiChunk(obj("role", "assistant", "content", plainText), nil) +
			openaiChunk(obj(), "stop") +
			dataLine(obj("id", "c1", "choices", arr(), "usage", openaiUsage())) +
			"data: [DONE]\n\n"
	},
	compress: func(stream bool) string {
		call := obj("id", "call_1", "type", "function", "function", obj("name", "compress", "arguments", compressArgs))
		if !stream {
			return js(obj("id", "c1", "object", "chat.completion", "model", "gpt-x",
				"choices", arr(obj("index", 0, "message", obj("role", "assistant", "content", nil, "tool_calls", arr(call)), "finish_reason", "tool_calls")),
				"usage", openaiUsage()))
		}
		return openaiChunk(obj("role", "assistant", "content", "Compressing."), nil) +
			openaiChunk(obj("tool_calls", arr(obj("index", 0, "id", "call_1", "type", "function",
				"function", obj("name", "compress", "arguments", "")))), nil) +
			openaiChunk(obj("tool_calls", arr(obj("index", 0, "function", obj("arguments", compressArgs)))), nil) +
			openaiChunk(obj(), "tool_calls") +
			dataLine(obj("id", "c1", "choices", arr(), "usage", openaiUsage())) +
			"data: [DONE]\n\n"
	},
	check: func(t *testing.T, body map[string]any) {
		prev, last := lastTwo(t, body, "messages")
		if prev["role"] != "assistant" || len(prev["tool_calls"].([]any)) != 1 {
			t.Fatalf("assistant tool round missing: %v", prev)
		}
		if last["role"] != "tool" || last["tool_call_id"] != "call_1" {
			t.Fatalf("tool message = %v", last)
		}
		if !strings.HasPrefix(fmt.Sprint(last["content"]), "Recorded") {
			t.Fatalf("tool result text = %v", last["content"])
		}
	},
}

func geminiUsage() map[string]any {
	return obj("promptTokenCount", 400, "candidatesTokenCount", 9, "totalTokenCount", 409, "cachedContentTokenCount", 100)
}

func geminiChunk(parts []any, finish any, usage any) string {
	cand := obj("content", obj("role", "model", "parts", parts), "index", 0)
	if finish != nil {
		cand["finishReason"] = finish
	}
	v := obj("candidates", arr(cand))
	if usage != nil {
		v["usageMetadata"] = usage
	}
	return dataLine(v)
}

var geminiDialect = dialect{
	name: "google",
	endpoint: func(stream bool) string {
		if stream {
			return "/v1beta/models/gemini-x:streamGenerateContent?alt=sse"
		}
		return "/v1beta/models/gemini-x:generateContent"
	},
	request: func(bool) string {
		var contents []any
		for i := 1; i <= 15; i++ {
			role := "user"
			if transcriptRole(i) == "assistant" {
				role = "model"
			}
			contents = append(contents, obj("role", role, "parts", arr(obj("text", transcriptText(i)))))
		}
		return js(obj("contents", contents))
	},
	plain: func(stream bool) string {
		if !stream {
			return js(obj("candidates", arr(obj("content", obj("role", "model", "parts", arr(obj("text", plainText))),
				"finishReason", "STOP", "index", 0)), "usageMetadata", geminiUsage()))
		}
		return geminiChunk(arr(obj("text", plainText)), nil, nil) +
			geminiChunk(arr(), "STOP", geminiUsage())
	},
	compress: func(stream bool) string {
		call := obj("functionCall", obj("name", "compress", "args", obj("ranges", arr(
			obj("startRef", "m00002", "endRef", "m00004", "summary", compressSumm)))))
		if !stream {
			return js(obj("candidates", arr(obj("content", obj("role", "model", "parts", arr(obj("text", "Compressing."), call)),
				"finishReason", "STOP", "index", 0)), "usageMetadata", geminiUsage()))
		}
		return geminiChunk(arr(obj("text", "Compressing.")), nil, nil) +
			geminiChunk(arr(call), "STOP", geminiUsage())
	},
	check: func(t *testing.T, body map[string]any) {
		prev, last := lastTwo(t, body, "contents")
		if prev["role"] != "model" || !strings.Contains(js(prev), `"functionCall"`) {
			t.Fatalf("model functionCall turn missing: %v", prev)
		}
		if last["role"] != "user" {
			t.Fatalf("functionResponse turn role = %v", last["role"])
		}
		parts, _ := last["parts"].([]any)
		if len(parts) == 0 {
			t.Fatalf("functionResponse turn has no parts: %v", last)
		}
		fr, _ := parts[0].(map[string]any)["functionResponse"].(map[string]any)
		resp, _ := fr["response"].(map[string]any)
		if fr["name"] != "compress" || !strings.HasPrefix(fmt.Sprint(resp["result"]), "Recorded") {
			t.Fatalf("functionResponse = %v", fr)
		}
	},
}

func responsesUsage() map[string]any {
	return obj("input_tokens", 400, "output_tokens", 9, "total_tokens", 409,
		"input_tokens_details", obj("cached_tokens", 100))
}

func responsesMessage() map[string]any {
	return obj("type", "message", "id", "msg_1", "role", "assistant",
		"content", arr(obj("type", "output_text", "text", plainText, "annotations", arr())))
}

func responsesCall() map[string]any {
	return obj("type", "function_call", "id", "fc_1", "call_id", "call_1", "name", "compress",
		"arguments", compressArgs, "status", "completed")
}

var responsesDialect = dialect{
	name:     "responses",
	endpoint: func(bool) string { return "/v1/responses" },
	request: func(stream bool) string {
		var input []any
		for i := 1; i <= 15; i++ {
			input = append(input, obj("role", transcriptRole(i), "content", transcriptText(i)))
		}
		return js(obj("model", "r-x", "stream", stream, "input", input))
	},
	plain: func(stream bool) string {
		if !stream {
			return js(obj("id", "resp_1", "object", "response", "status", "completed",
				"output", arr(responsesMessage()), "usage", responsesUsage()))
		}
		msgIn := obj("type", "message", "id", "msg_1", "role", "assistant", "content", arr())
		return sseEv("response.created", js(obj("type", "response.created",
			"response", obj("id", "resp_1", "status", "in_progress", "output", arr())))) +
			sseEv("response.output_item.added", js(obj("type", "response.output_item.added", "output_index", 0, "item", msgIn))) +
			sseEv("response.output_text.delta", js(obj("type", "response.output_text.delta", "item_id", "msg_1",
				"output_index", 0, "content_index", 0, "delta", plainText))) +
			sseEv("response.output_item.done", js(obj("type", "response.output_item.done", "output_index", 0, "item", responsesMessage()))) +
			sseEv("response.completed", js(obj("type", "response.completed",
				"response", obj("id", "resp_1", "status", "completed", "output", arr(responsesMessage()), "usage", responsesUsage()))))
	},
	compress: func(stream bool) string {
		if !stream {
			reasoning := obj("type", "reasoning", "id", "rs_1", "summary", arr())
			return js(obj("id", "resp_1", "object", "response", "status", "completed",
				"output", arr(reasoning, responsesCall()), "usage", responsesUsage()))
		}
		// A reasoning item sits at index 0 and the call at index 1, as in a real round.
		reasoning := obj("type", "reasoning", "id", "rs_1", "summary", arr())
		open := responsesCall()
		open["status"] = "in_progress"
		open["arguments"] = ""
		return sseEv("response.created", js(obj("type", "response.created",
			"response", obj("id", "resp_1", "status", "in_progress", "output", arr())))) +
			sseEv("response.output_item.added", js(obj("type", "response.output_item.added", "output_index", 0, "item", reasoning))) +
			sseEv("response.output_item.done", js(obj("type", "response.output_item.done", "output_index", 0, "item", reasoning))) +
			sseEv("response.output_item.added", js(obj("type", "response.output_item.added", "output_index", 1, "item", open))) +
			sseEv("response.function_call_arguments.delta", js(obj("type", "response.function_call_arguments.delta",
				"item_id", "fc_1", "output_index", 1, "delta", compressArgs))) +
			sseEv("response.function_call_arguments.done", js(obj("type", "response.function_call_arguments.done",
				"item_id", "fc_1", "output_index", 1, "arguments", compressArgs))) +
			sseEv("response.output_item.done", js(obj("type", "response.output_item.done", "output_index", 1, "item", responsesCall()))) +
			sseEv("response.completed", js(obj("type", "response.completed",
				"response", obj("id", "resp_1", "status", "completed", "output", arr(reasoning, responsesCall()), "usage", responsesUsage()))))
	},
	check: func(t *testing.T, body map[string]any) {
		prev, last := lastTwo(t, body, "input")
		if prev["type"] != "function_call" || prev["call_id"] != "call_1" {
			t.Fatalf("function_call item missing: %v", prev)
		}
		items, _ := body["input"].([]any)
		if n := len(items); n < 3 || !strings.Contains(js(items[n-3]), `"reasoning"`) {
			t.Fatalf("reasoning item must precede the function_call: %s", js(body["input"]))
		}
		if last["type"] != "function_call_output" || last["call_id"] != "call_1" {
			t.Fatalf("function_call_output = %v", last)
		}
		if !strings.HasPrefix(fmt.Sprint(last["output"]), "Recorded") {
			t.Fatalf("output text = %v", last["output"])
		}
	},
}

var dialects = []dialect{openaiDialect, responsesDialect, geminiDialect}

func TestDialectPlainTurn(t *testing.T) {
	for _, d := range dialects {
		for _, stream := range []bool{false, true} {
			name := d.name + "/json"
			if stream {
				name = d.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				up := newAnyUpstream(t, func(int, []byte) (int, string, string) {
					return 200, streamCT(stream), d.plain(stream)
				})
				ps, dbPath := newAnyProxy(t, up)
				resp, got := postDialect(t, ps.URL+d.endpoint(stream), d.request(stream))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d: %s", resp.StatusCode, got)
				}
				if string(got) != d.plain(stream) {
					t.Fatalf("client answer changed:\n%s", got)
				}
				if up.count() != 1 {
					t.Fatalf("upstream calls = %d, want 1", up.count())
				}
				if !strings.Contains(string(up.body(0)), "decompress") {
					t.Fatalf("internal tools not injected: %s", up.body(0))
				}
				if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger WHERE input_tokens = 400 AND cached_tokens = 100"); n != 1 {
					t.Fatalf("cache_ledger rows with usage = %d, want 1", n)
				}
			})
		}
	}
}

func TestDialectCompressLoop(t *testing.T) {
	for _, d := range dialects {
		for _, stream := range []bool{false, true} {
			name := d.name + "/json"
			if stream {
				name = d.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				up := newAnyUpstream(t, func(n int, _ []byte) (int, string, string) {
					if n == 0 {
						return 200, streamCT(stream), d.compress(stream)
					}
					return 200, streamCT(stream), d.plain(stream)
				})
				ps, dbPath := newAnyProxy(t, up)
				resp, got := postDialect(t, ps.URL+d.endpoint(stream), d.request(stream))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d: %s", resp.StatusCode, got)
				}
				client := string(got)
				if !strings.Contains(client, plainText) {
					t.Fatalf("client lacks the final answer:\n%s", client)
				}
				for _, leak := range []string{"compress", "call_1", "fc_1", "functionCall", "function_call"} {
					if strings.Contains(client, leak) {
						t.Fatalf("internal call leaked (%q):\n%s", leak, client)
					}
				}
				if up.count() != 2 {
					t.Fatalf("upstream calls = %d, want 2", up.count())
				}
				var round2 map[string]any
				if err := json.Unmarshal(up.body(1), &round2); err != nil {
					t.Fatalf("round 2 body: %v", err)
				}
				d.check(t, round2)
				if n := countRows(t, dbPath, "SELECT COUNT(*) FROM blocks WHERE active = 1"); n != 1 {
					t.Fatalf("active blocks = %d, want 1", n)
				}
				if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger WHERE after_compress = 1"); n != 1 {
					t.Fatalf("cache_ledger after_compress rows = %d, want 1", n)
				}
				if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger"); n != 2 {
					t.Fatalf("cache_ledger rows = %d, want 2", n)
				}
			})
		}
	}
}

// TestDialectRouting checks which paths are compacted. Compacted requests carry
// the injected tools upstream, and the others reach the upstream byte for byte.
func TestDialectRouting(t *testing.T) {
	cases := []struct {
		name      string
		path      string // "{up}" is replaced by the fake upstream origin
		d         dialect
		stream    bool
		compacted bool
	}{
		{"ctx prefix chat", "/ctx/{up}/v1/chat/completions", openaiDialect, false, true},
		{"ctx gateway prefix chat", "/ctx/{up}/gateway/v1/chat/completions", openaiDialect, false, true},
		{"project prefix gemini", "/p/proj-1/v1beta/models/gemini-x:generateContent", geminiDialect, false, true},
		{"ctx gateway gemini stream", "/ctx/{up}/gw/v1beta/models/gemini-x:streamGenerateContent?alt=sse", geminiDialect, true, true},
		{"responses item path", "/v1/responses/resp_1", responsesDialect, false, false},
		{"gemini count tokens", "/v1beta/models/gemini-x:countTokens", geminiDialect, false, false},
		{"gemini stream without sse", "/v1beta/models/gemini-x:streamGenerateContent", geminiDialect, true, false},
		{"chat sub path", "/v1/chat/completions/extra", openaiDialect, false, false},
		{"gateway prefix without ctx", "/gateway/v1/chat/completions", openaiDialect, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newAnyUpstream(t, func(int, []byte) (int, string, string) {
				return 200, streamCT(tc.stream), tc.d.plain(tc.stream)
			})
			ps, _ := newAnyProxy(t, up)
			path := strings.ReplaceAll(tc.path, "{up}", up.srv.URL)
			body := tc.d.request(tc.stream)
			postDialect(t, ps.URL+path, body)
			if up.count() != 1 {
				t.Fatalf("upstream calls = %d, want 1", up.count())
			}
			got := string(up.body(0))
			if tc.compacted && !strings.Contains(got, "decompress") {
				t.Fatalf("compacted request lacks injected tools: %s", got)
			}
			if !tc.compacted && got != body {
				t.Fatalf("pass-through body changed:\n%s", got)
			}
		})
	}
}

// TestResponsesStatefulIsForwardedUnchanged covers ErrStateful: a request that
// refers to server-side state is never rewritten.
func TestResponsesStatefulIsForwardedUnchanged(t *testing.T) {
	body := `{"model":"r-x","previous_response_id":"resp_0","input":[{"role":"user","content":"hi"}]}`
	up := newAnyUpstream(t, func(int, []byte) (int, string, string) {
		return 200, "application/json", responsesDialect.plain(false)
	})
	ps, dbPath := newAnyProxy(t, up)
	postDialect(t, ps.URL+"/v1/responses", body)
	if up.count() != 1 || string(up.body(0)) != body {
		t.Fatalf("stateful request was rewritten: %s", up.body(0))
	}
	if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger"); n != 0 {
		t.Fatalf("cache_ledger rows = %d, want 0 for a stateful request", n)
	}
}

// TestNudgeNeverDropped checks AppendNudge for the three dialects. The Chat
// Completions case, where the last message is a tool result, must not drop the
// nudge.
func TestNudgeNeverDropped(t *testing.T) {
	// Plain text: json.Marshal would escape angle brackets, which breaks the substring checks.
	nudge := "CONTEXT NUDGE TEXT"

	chat := js(obj("model", "gpt-x", "messages", arr(
		obj("role", "user", "content", "go"),
		obj("role", "assistant", "content", nil, "tool_calls", arr()),
		obj("role", "tool", "tool_call_id", "call_1", "content", "done"))))
	out, err := openaiProtocol{}.AppendNudge([]byte(chat), nudge)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	msgs := got["messages"].([]any)
	if last := msgs[len(msgs)-1].(map[string]any); last["role"] != "user" || last["content"] != nudge {
		t.Fatalf("nudge after tool result = %v", last)
	}

	resp := js(obj("model", "r-x", "input", arr(obj("type", "message", "role", "user", "content", "go"))))
	out, err = responsesProtocol{}.AppendNudge([]byte(resp), nudge)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"input_text"`) || !strings.Contains(string(out), nudge) {
		t.Fatalf("responses nudge missing: %s", out)
	}

	gem := js(obj("contents", arr(obj("role", "model", "parts", arr(obj("text", "ok"))))))
	out, err = googleProtocol{}.AppendNudge([]byte(gem), nudge)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), nudge) || !strings.Contains(string(out), `"role":"user"`) {
		t.Fatalf("gemini nudge missing: %s", out)
	}
}
