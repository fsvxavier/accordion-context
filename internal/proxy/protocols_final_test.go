package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestDialectFinalRoundStripsInternalCalls runs a model that asks for compress on
// every round. The loop must stop at MaxLoop, and the client must get the final
// answer without any trace of the internal calls, in JSON and in SSE.
func TestDialectFinalRoundStripsInternalCalls(t *testing.T) {
	for _, d := range dialects {
		for _, stream := range []bool{false, true} {
			name := d.name + "/json"
			if stream {
				name = d.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				up := newAnyUpstream(t, func(int, []byte) (int, string, string) {
					return 200, streamCT(stream), d.compress(stream)
				})
				ps, _ := newAnyProxy(t, up)
				resp, got := postDialect(t, ps.URL+d.endpoint(stream), d.request(stream))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d: %s", resp.StatusCode, got)
				}
				if up.count() != defaultMaxLoop {
					t.Fatalf("upstream calls = %d, want %d (MaxLoop)", up.count(), defaultMaxLoop)
				}
				client := string(got)
				for _, leak := range []string{"compress", "call_1", "fc_1", "functionCall", "function_call", "tool_calls"} {
					if strings.Contains(client, leak) {
						t.Fatalf("internal call leaked on the final round (%q):\n%s", leak, client)
					}
				}
				if d.name != "openai" {
					return
				}
				// The only tool call of the final round was removed, so the choice must
				// finish as stop, not tool_calls.
				if stream {
					if !strings.Contains(client, `"finish_reason":"stop"`) || strings.Contains(client, `"finish_reason":"tool_calls"`) {
						t.Fatalf("stream finish reason not rewritten to stop:\n%s", client)
					}
					return
				}
				var out struct {
					Choices []struct {
						FinishReason string `json:"finish_reason"`
					} `json:"choices"`
				}
				if err := json.Unmarshal(got, &out); err != nil || len(out.Choices) == 0 {
					t.Fatalf("client body: %v\n%s", err, got)
				}
				if out.Choices[0].FinishReason != "stop" {
					t.Fatalf("finish_reason = %q, want stop", out.Choices[0].FinishReason)
				}
			})
		}
	}
}
