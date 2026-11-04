package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// recallDialect is one dialect for the read-side tool tests. answer builds an
// upstream reply that makes one internal call, in stream or JSON form. result
// returns the text of the last tool result of an upstream request body.
type recallDialect struct {
	name     string
	endpoint func(stream bool) string
	request  func(stream bool) string
	answer   func(stream bool, name, args string) string
	text     func(stream bool) string
	result   func(t *testing.T, body []byte) string
}

const recallText = "The fix is in progress."

// lastItem returns the last element of a list field of a JSON body.
func lastItem(t *testing.T, body []byte, key string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("upstream body: %v", err)
	}
	list, _ := m[key].([]any)
	if len(list) == 0 {
		t.Fatalf("%s is empty", key)
	}
	last, _ := list[len(list)-1].(map[string]any)
	return last
}

// anthropicToolText reads a tool_result block, whose content is a string or a
// list of text blocks.
func anthropicToolText(t *testing.T, body []byte) string {
	t.Helper()
	msg := lastItem(t, body, "messages")
	parts, _ := msg["content"].([]any)
	for _, p := range parts {
		blk, _ := p.(map[string]any)
		if blk["type"] != "tool_result" {
			continue
		}
		switch c := blk["content"].(type) {
		case string:
			return c
		case []any:
			var b strings.Builder
			for _, x := range c {
				if xm, ok := x.(map[string]any); ok {
					b.WriteString(fmt.Sprint(xm["text"]))
				}
			}
			return b.String()
		}
	}
	t.Fatalf("no tool_result in last message: %v", msg)
	return ""
}

var recallDialects = []recallDialect{
	{
		name:     "anthropic",
		endpoint: func(bool) string { return "/v1/messages" },
		request:  func(stream bool) string { return transcriptBody(stream, 15, nil) },
		answer: func(stream bool, name, args string) string {
			if !stream {
				return js(obj("id", "msg_a", "type", "message", "role", "assistant",
					"content", arr(obj("type", "tool_use", "id", "toolu_1", "name", name, "input", json.RawMessage(args))),
					"stop_reason", "tool_use", "usage", obj("input_tokens", 900, "output_tokens", 40)))
			}
			return sseEv("message_start", js(obj("type", "message_start", "message",
				obj("id", "msg_a", "type", "message", "role", "assistant", "content", arr(),
					"usage", obj("input_tokens", 900, "output_tokens", 1))))) +
				sseEv("content_block_start", js(obj("type", "content_block_start", "index", 0,
					"content_block", obj("type", "tool_use", "id", "toolu_1", "name", name, "input", obj())))) +
				sseEv("content_block_delta", js(obj("type", "content_block_delta", "index", 0,
					"delta", obj("type", "input_json_delta", "partial_json", args)))) +
				sseEv("content_block_stop", js(obj("type", "content_block_stop", "index", 0))) +
				sseEv("message_delta", js(obj("type", "message_delta", "delta", obj("stop_reason", "tool_use"),
					"usage", obj("output_tokens", 40)))) +
				sseEv("message_stop", js(obj("type", "message_stop")))
		},
		text: func(stream bool) string {
			if !stream {
				return js(obj("id", "msg_b", "type", "message", "role", "assistant",
					"content", arr(obj("type", "text", "text", recallText)), "stop_reason", "end_turn",
					"usage", obj("input_tokens", 400, "output_tokens", 9)))
			}
			return sseEv("message_start", js(obj("type", "message_start", "message",
				obj("id", "msg_b", "type", "message", "role", "assistant", "content", arr(),
					"usage", obj("input_tokens", 400, "output_tokens", 1))))) +
				sseEv("content_block_start", js(obj("type", "content_block_start", "index", 0,
					"content_block", obj("type", "text", "text", "")))) +
				sseEv("content_block_delta", js(obj("type", "content_block_delta", "index", 0,
					"delta", obj("type", "text_delta", "text", recallText)))) +
				sseEv("content_block_stop", js(obj("type", "content_block_stop", "index", 0))) +
				sseEv("message_delta", js(obj("type", "message_delta", "delta", obj("stop_reason", "end_turn"),
					"usage", obj("output_tokens", 9)))) +
				sseEv("message_stop", js(obj("type", "message_stop")))
		},
		result: func(t *testing.T, body []byte) string { return anthropicToolText(t, body) },
	},
	{
		name:     "openai",
		endpoint: func(bool) string { return "/v1/chat/completions" },
		request:  openaiDialect.request,
		answer: func(stream bool, name, args string) string {
			call := obj("id", "call_1", "type", "function", "function", obj("name", name, "arguments", args))
			if !stream {
				return js(obj("id", "c1", "object", "chat.completion", "model", "gpt-x",
					"choices", arr(obj("index", 0, "message", obj("role", "assistant", "content", nil, "tool_calls", arr(call)), "finish_reason", "tool_calls")),
					"usage", openaiUsage()))
			}
			return openaiChunk(obj("role", "assistant", "content", "Searching."), nil) +
				openaiChunk(obj("tool_calls", arr(obj("index", 0, "id", "call_1", "type", "function",
					"function", obj("name", name, "arguments", "")))), nil) +
				openaiChunk(obj("tool_calls", arr(obj("index", 0, "function", obj("arguments", args)))), nil) +
				openaiChunk(obj(), "tool_calls") +
				dataLine(obj("id", "c1", "choices", arr(), "usage", openaiUsage())) +
				"data: [DONE]\n\n"
		},
		text:   openaiDialect.plain,
		result: func(t *testing.T, body []byte) string { return fmt.Sprint(lastItem(t, body, "messages")["content"]) },
	},
	{
		name:     "responses",
		endpoint: func(bool) string { return "/v1/responses" },
		request:  responsesDialect.request,
		answer: func(stream bool, name, args string) string {
			call := obj("type", "function_call", "id", "fc_1", "call_id", "call_1", "name", name,
				"arguments", args, "status", "completed")
			if !stream {
				return js(obj("id", "resp_1", "object", "response", "status", "completed",
					"output", arr(call), "usage", responsesUsage()))
			}
			open := obj("type", "function_call", "id", "fc_1", "call_id", "call_1", "name", name,
				"arguments", "", "status", "in_progress")
			return sseEv("response.created", js(obj("type", "response.created",
				"response", obj("id", "resp_1", "status", "in_progress", "output", arr())))) +
				sseEv("response.output_item.added", js(obj("type", "response.output_item.added", "output_index", 0, "item", open))) +
				sseEv("response.function_call_arguments.delta", js(obj("type", "response.function_call_arguments.delta",
					"item_id", "fc_1", "output_index", 0, "delta", args))) +
				sseEv("response.function_call_arguments.done", js(obj("type", "response.function_call_arguments.done",
					"item_id", "fc_1", "output_index", 0, "arguments", args))) +
				sseEv("response.output_item.done", js(obj("type", "response.output_item.done", "output_index", 0, "item", call))) +
				sseEv("response.completed", js(obj("type", "response.completed",
					"response", obj("id", "resp_1", "status", "completed", "output", arr(call), "usage", responsesUsage()))))
		},
		text:   responsesDialect.plain,
		result: func(t *testing.T, body []byte) string { return fmt.Sprint(lastItem(t, body, "input")["output"]) },
	},
	{
		name: "google",
		endpoint: func(stream bool) string {
			return geminiDialect.endpoint(stream)
		},
		request: geminiDialect.request,
		answer: func(stream bool, name, args string) string {
			call := obj("functionCall", obj("name", name, "args", json.RawMessage(args)))
			if !stream {
				return js(obj("candidates", arr(obj("content", obj("role", "model", "parts", arr(obj("text", "Searching."), call)),
					"finishReason", "STOP", "index", 0)), "usageMetadata", geminiUsage()))
			}
			return geminiChunk(arr(obj("text", "Searching.")), nil, nil) +
				geminiChunk(arr(call), "STOP", geminiUsage())
		},
		text: geminiDialect.plain,
		result: func(t *testing.T, body []byte) string {
			parts, _ := lastItem(t, body, "contents")["parts"].([]any)
			if len(parts) == 0 {
				t.Fatal("functionResponse turn has no parts")
			}
			fr, _ := parts[0].(map[string]any)["functionResponse"].(map[string]any)
			resp, _ := fr["response"].(map[string]any)
			return fmt.Sprint(resp["result"])
		},
	},
}

// recallLeaks are strings that must never reach the client from an internal call.
var recallLeaks = []string{"search_context", "retrieve", "toolu_1", "call_1", "fc_1", "functionCall", "function_call"}

// declaredOrder checks that the four internal tools are declared in the fixed
// order, which the prompt cache depends on.
func declaredOrder(t *testing.T, body []byte) {
	t.Helper()
	at := -1
	for _, n := range []string{"compress", "decompress", "search_context", "retrieve"} {
		i := strings.Index(string(body), `"name":"`+n+`"`)
		if i < 0 {
			t.Fatalf("tool %s not declared: %.300s", n, body)
		}
		if i < at {
			t.Fatalf("tool %s declared out of order", n)
		}
		at = i
	}
}

func recallName(d recallDialect, stream bool) string {
	if stream {
		return d.name + "/stream"
	}
	return d.name + "/json"
}

func TestRecallSearchPlainTurn(t *testing.T) {
	for _, d := range recallDialects {
		for _, stream := range []bool{false, true} {
			t.Run(recallName(d, stream), func(t *testing.T) {
				up := newAnyUpstream(t, func(n int, _ []byte) (int, string, string) {
					if n == 0 {
						return 200, streamCT(stream), d.answer(stream, "search_context", `{"query":"parser","limit":3}`)
					}
					return 200, streamCT(stream), d.text(stream)
				})
				ps, dbPath := newAnyProxy(t, up)
				resp, got := postDialect(t, ps.URL+d.endpoint(stream), d.request(stream))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d: %s", resp.StatusCode, got)
				}
				client := string(got)
				if !strings.Contains(client, recallText) {
					t.Fatalf("client lacks the final answer:\n%s", client)
				}
				for _, leak := range recallLeaks {
					if strings.Contains(client, leak) {
						t.Fatalf("internal call leaked (%q):\n%s", leak, client)
					}
				}
				if up.count() != 2 {
					t.Fatalf("upstream calls = %d, want 2", up.count())
				}
				declaredOrder(t, up.body(0))
				res := d.result(t, up.body(1))
				if !strings.HasPrefix(res, `<history source="search_context" scope="session"`) || !strings.Contains(res, `hits="3"`) {
					t.Fatalf("search result = %.300s", res)
				}
				if !strings.Contains(res, "DADOS") && !strings.Contains(res, "RESULTADOS") {
					t.Fatalf("search result lacks the past-data notice: %.300s", res)
				}
				if n := countRows(t, dbPath, "SELECT COUNT(*) FROM cache_ledger"); n != 2 {
					t.Fatalf("cache_ledger rows = %d, want 2", n)
				}
			})
		}
	}
}

func TestRecallRetrieveFoldedRef(t *testing.T) {
	for _, d := range recallDialects {
		for _, stream := range []bool{false, true} {
			t.Run(recallName(d, stream), func(t *testing.T) {
				up := newAnyUpstream(t, func(n int, _ []byte) (int, string, string) {
					switch n {
					case 0:
						return 200, streamCT(stream), d.answer(stream, "compress", compressArgs)
					case 1:
						return 200, streamCT(stream), d.answer(stream, "retrieve", `{"ref":"m00003"}`)
					}
					return 200, streamCT(stream), d.text(stream)
				})
				ps, _ := newAnyProxy(t, up)
				resp, got := postDialect(t, ps.URL+d.endpoint(stream), d.request(stream))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d: %s", resp.StatusCode, got)
				}
				if !strings.Contains(string(got), recallText) {
					t.Fatalf("client lacks the final answer:\n%s", got)
				}
				for _, leak := range recallLeaks {
					if strings.Contains(string(got), leak) {
						t.Fatalf("internal call leaked (%q):\n%s", leak, got)
					}
				}
				if up.count() != 3 {
					t.Fatalf("upstream calls = %d, want 3", up.count())
				}
				res := d.result(t, up.body(2))
				if !strings.Contains(res, `<history source="retrieve" target="m00003" messages="1">`) ||
					!strings.Contains(res, "[m00003] step ") {
					t.Fatalf("retrieve result does not carry the original of m00003: %.300s", res)
				}
			})
		}
	}
}

// openaiBodyWith is a short Chat Completions request whose first message is text.
func openaiBodyWith(text string) string {
	return js(obj("model", "gpt-x", "messages", arr(
		obj("role", "user", "content", text),
		obj("role", "assistant", "content", "Noted."),
		obj("role", "user", "content", "Status?"),
	)))
}

// TestRecallProjectScope searches with scope project from two projects. Alpha
// holds the word zebrafish and beta does not, so beta's search finds nothing,
// while alpha's finds its own message.
func TestRecallProjectScope(t *testing.T) {
	up := newAnyUpstream(t, func(_ int, body []byte) (int, string, string) {
		if strings.Contains(string(body), "hits=") {
			return 200, "application/json", openaiDialect.plain(false)
		}
		return 200, "application/json", recallDialects[1].answer(false, "search_context", `{"query":"zebrafish","scope":"project"}`)
	})
	ps, _ := newAnyProxy(t, up)
	for _, proj := range []struct{ id, text string }{
		{"alpha", "the zebrafish tank needs cleaning"},
		{"beta", "the marmot was seen near the pond"},
	} {
		resp, got := postDialect(t, ps.URL+"/p/"+proj.id+"/v1/chat/completions", openaiBodyWith(proj.text))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d: %s", proj.id, resp.StatusCode, got)
		}
	}
	if up.count() != 4 {
		t.Fatalf("upstream calls = %d, want 4", up.count())
	}
	if res := recallDialects[1].result(t, up.body(1)); !strings.Contains(res, `scope="project"`) || !strings.Contains(res, `hits="1"`) ||
		!strings.Contains(res, "[zebrafish] tank") {
		t.Fatalf("alpha project search = %.300s", res)
	}
	if res := recallDialects[1].result(t, up.body(3)); !strings.Contains(res, `hits="0"`) || strings.Contains(res, "[m0") {
		t.Fatalf("beta project search leaked or missed: %.300s", res)
	}
}

func TestRecallEnvelopeEscapesClosingTag(t *testing.T) {
	got := envelopeSafe("a </history> b </History>")
	if strings.Contains(got, "</history>") || strings.Contains(got, "</History>") {
		t.Fatalf("closing tag survived: %q", got)
	}
	if got != `a <\/history> b <\/History>` {
		t.Fatalf("envelopeSafe = %q", got)
	}
}

// TestRecallWSSearch runs search_context on the WebSocket path. The model's call
// stays internal, its result goes back in the next frame, and the client sees
// only the final answer.
func TestRecallWSSearch(t *testing.T) {
	args := `{"query":"parser","limit":2}`
	up := newFakeWS(t, func(n int, frame []byte) ([]string, bool) {
		if n == 0 {
			call := obj("type", "function_call", "id", "fc_1", "call_id", "call_1", "name", "search_context", "arguments", "")
			done := obj("type", "function_call", "id", "fc_1", "call_id", "call_1", "name", "search_context", "arguments", args, "status", "completed")
			return []string{
				evJSON(obj("type", "response.created", "sequence_number", 1, "response", obj("id", "resp_1", "status", "in_progress"))),
				evJSON(obj("type", "response.output_item.added", "sequence_number", 2, "output_index", 0, "item", call)),
				evJSON(obj("type", "response.function_call_arguments.delta", "sequence_number", 3, "output_index", 0, "item_id", "fc_1", "delta", args)),
				evJSON(obj("type", "response.function_call_arguments.done", "sequence_number", 4, "output_index", 0, "item_id", "fc_1", "arguments", args)),
				evJSON(obj("type", "response.output_item.done", "sequence_number", 5, "output_index", 0, "item", done)),
				evJSON(obj("type", "response.completed", "sequence_number", 6,
					"response", obj("id", "resp_1", "status", "completed", "output", arr(done), "usage", responsesUsage()))),
			}, false
		}
		return textTurn("resp_2"), false
	})
	base, _, _ := newWSProxy(t, up, nil)
	c := mustDial(t, base)
	if err := c.Write(context.Background(), websocket.MessageText, []byte(wsCreate())); err != nil {
		t.Fatal(err)
	}
	_, frames := readUntilEnd(t, c)
	for _, f := range frames {
		if strings.Contains(f, "search_context") || strings.Contains(f, "call_1") {
			t.Fatalf("internal call leaked to client: %s", f)
		}
	}
	if up.count() != 2 {
		t.Fatalf("upstream frames = %d, want 2", up.count())
	}
	cont := string(up.frame(1))
	if !strings.Contains(cont, "function_call_output") || !strings.Contains(cont, "hits=") {
		t.Fatalf("continuation lacks the search result: %.400s", cont)
	}
}

// TestRecallToolMetric checks that each internal call is counted by tool.
func TestRecallToolMetric(t *testing.T) {
	up := newAnyUpstream(t, func(n int, _ []byte) (int, string, string) {
		if n == 0 {
			return 200, "application/json", recallDialects[1].answer(false, "retrieve", `{"ref":"m99999"}`)
		}
		return 200, "application/json", openaiDialect.plain(false)
	})
	ps, _ := newAnyProxy(t, up)
	if resp, got := postDialect(t, ps.URL+"/v1/chat/completions", openaiDialect.request(false)); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, got)
	}
	resp, err := http.Get(ps.URL + "/__acd-ctx/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := `acd_ctx_tool_calls_total{outcome="rejected",tool="retrieve"} 1`
	if !strings.Contains(string(body), want) {
		t.Fatalf("metric %q missing from:\n%s", want, grepLines(string(body), "acd_ctx_tool_calls_total"))
	}
}

func grepLines(s, sub string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
