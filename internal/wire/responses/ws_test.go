package responses

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const wsCompressArgs = `{"ranges":[{"startRef":"m00002","endRef":"m00004","summary":"Read the parser source."}]}`

func wsFrame(extra string) string {
	base := `{"type":"response.create","model":"gpt-x","input":[{"role":"user","content":"hi"}]`
	if extra != "" {
		base += "," + extra
	}
	return base + "}"
}

func TestReadCreateClassifiesFrames(t *testing.T) {
	c := NewFrameCodec()
	if _, _, err := c.ReadCreate([]byte(wsFrame(""))); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := c.ReadCreate([]byte(`{"type":"response.cancel","response_id":"r"}`)); !errors.Is(err, ErrNotCreate) {
		t.Fatalf("cancel: err = %v, want ErrNotCreate", err)
	}
	stateful := wsFrame(`"previous_response_id":"resp_0"`)
	if _, _, err := c.ReadCreate([]byte(stateful)); !errors.Is(err, ErrStateful) {
		t.Fatalf("stateful: err = %v, want ErrStateful", err)
	}
	if _, _, err := c.ReadCreate([]byte(`not json`)); err == nil {
		t.Fatal("malformed frame accepted")
	}
}

func ev(v map[string]any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// callRound feeds one round in which the model asks for compress and then ends.
func callRound(t *testing.T, c *FrameCodec, id string) {
	t.Helper()
	c.StartRound()
	call := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "compress", "arguments": ""}
	feed := []map[string]any{
		{"type": "response.created", "sequence_number": 1, "response": map[string]any{"id": id}},
		{"type": "response.output_item.added", "sequence_number": 2, "output_index": 0, "item": call},
		{"type": "response.function_call_arguments.delta", "sequence_number": 3, "output_index": 0, "delta": wsCompressArgs},
		{"type": "response.function_call_arguments.done", "sequence_number": 4, "output_index": 0, "arguments": wsCompressArgs},
		{"type": "response.output_item.done", "sequence_number": 5, "output_index": 0,
			"item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "compress", "arguments": wsCompressArgs}},
		{"type": "response.completed", "sequence_number": 6, "response": map[string]any{"id": id,
			"output": []any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "compress", "arguments": wsCompressArgs}},
			"usage":  map[string]any{"input_tokens": 90, "output_tokens": 4, "input_tokens_details": map[string]any{"cached_tokens": 10}}}},
	}
	for _, f := range feed {
		out, tk, err := c.ServerEvent(ev(f))
		if err != nil {
			t.Fatalf("ServerEvent: %v", err)
		}
		if strings.Contains(string(out), "compress") || strings.Contains(string(out), "call_1") {
			t.Fatalf("internal call leaked: %s", out)
		}
		if tk == TerminalHeld && out != nil {
			t.Fatal("terminal frame returned instead of held")
		}
	}
}

func TestServerEventWithholdsInternalCallAndReportsIt(t *testing.T) {
	c := NewFrameCodec()
	if _, _, err := c.ReadCreate([]byte(wsFrame(""))); err != nil {
		t.Fatal(err)
	}
	callRound(t, c, "resp_1")
	res := c.Result()
	if !res.Complete || !res.HasUsage || res.Usage.CachedTokens != 10 || res.Usage.InputTokens != 90 {
		t.Fatalf("usage/complete = %+v", res)
	}
	if len(res.Calls) != 1 || res.Calls[0].Compress == nil || res.Calls[0].Name != "compress" {
		t.Fatalf("calls = %+v", res.Calls)
	}
	if len(res.Assistant) != 1 || res.ClientCalls != 0 {
		t.Fatalf("assistant = %d items, client calls = %d", len(res.Assistant), res.ClientCalls)
	}
	fin, err := c.FinishRound()
	if err != nil {
		t.Fatal(err)
	}
	if fin == nil {
		t.Fatal("no held terminal frame")
	}
	var m struct {
		Type     string `json:"type"`
		Response struct {
			ID     string           `json:"id"`
			Output []map[string]any `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(fin, &m); err != nil {
		t.Fatal(err)
	}
	if m.Type != "response.completed" || m.Response.ID != "resp_1" || len(m.Response.Output) != 0 {
		t.Fatalf("terminal = %s", fin)
	}
}

func TestContinuationRoundStaysOneResponse(t *testing.T) {
	c := NewFrameCodec()
	if _, _, err := c.ReadCreate([]byte(wsFrame(""))); err != nil {
		t.Fatal(err)
	}
	callRound(t, c, "resp_1") // round 1 ends with an internal call; its terminal is held and not sent

	// Round 2: the model answers with text at upstream output index 0.
	c.StartRound()
	feed := []map[string]any{
		{"type": "response.created", "sequence_number": 1, "response": map[string]any{"id": "resp_2"}},
		{"type": "response.in_progress", "sequence_number": 2, "response": map[string]any{"id": "resp_2"}},
		{"type": "response.output_item.added", "sequence_number": 3, "output_index": 0,
			"item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "content": []any{}}},
		{"type": "response.output_item.done", "sequence_number": 4, "output_index": 0,
			"item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "content": []any{}}},
		{"type": "response.completed", "sequence_number": 5, "response": map[string]any{"id": "resp_2"}},
	}
	var forwarded []map[string]any
	for _, f := range feed {
		out, _, err := c.ServerEvent(ev(f))
		if err != nil {
			t.Fatal(err)
		}
		if out == nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		forwarded = append(forwarded, m)
	}
	for _, m := range forwarded {
		if m["type"] == "response.created" || m["type"] == "response.in_progress" {
			t.Fatalf("continuation re-announced the response: %v", m)
		}
		if resp, ok := m["response"].(map[string]any); ok && resp["id"] != "resp_1" {
			t.Fatalf("response id = %v, want resp_1", resp["id"])
		}
	}
	// The added item of round 2 sits after round 1's index (0), so it is index 1.
	if got := forwarded[0]["output_index"]; got != float64(1) {
		t.Fatalf("output_index = %v, want 1", got)
	}
	// Sequence numbers keep counting for the client.
	var seqs []float64
	for _, m := range forwarded {
		if s, ok := m["sequence_number"].(float64); ok {
			seqs = append(seqs, s)
		}
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Fatalf("sequence numbers not contiguous: %v", seqs)
		}
	}

	res := c.Result()
	if !res.Complete || res.ClientCalls != 0 || len(res.Calls) != 0 {
		t.Fatalf("round 2 result = %+v", res)
	}
	fin, err := c.FinishRound()
	if err != nil {
		t.Fatal(err)
	}
	var term struct {
		Response struct {
			ID     string           `json:"id"`
			Output []map[string]any `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(fin, &term); err != nil {
		t.Fatal(err)
	}
	if term.Response.ID != "resp_1" || len(term.Response.Output) != 1 || term.Response.Output[0]["id"] != "msg_1" {
		t.Fatalf("terminal = %s", fin)
	}
}

func TestErrorEventEndsRound(t *testing.T) {
	c := NewFrameCodec()
	if _, _, err := c.ReadCreate([]byte(wsFrame(""))); err != nil {
		t.Fatal(err)
	}
	c.StartRound()
	frame := []byte(`{"type":"error","error":{"message":"boom"}}`)
	out, tk, err := c.ServerEvent(frame)
	if err != nil || tk != TerminalError || string(out) != string(frame) {
		t.Fatalf("error event: out=%s tk=%v err=%v", out, tk, err)
	}
	if fin, _ := c.FinishRound(); fin != nil {
		t.Fatalf("terminal frame after error: %s", fin)
	}
}

func TestMalformedServerFrameIsForwardedRaw(t *testing.T) {
	c := NewFrameCodec()
	if _, _, err := c.ReadCreate([]byte(wsFrame(""))); err != nil {
		t.Fatal(err)
	}
	c.StartRound()
	out, tk, err := c.ServerEvent([]byte(`{broken`))
	if err != nil || tk != NotTerminal || string(out) != `{broken` {
		t.Fatalf("malformed: out=%s tk=%v err=%v", out, tk, err)
	}
}
