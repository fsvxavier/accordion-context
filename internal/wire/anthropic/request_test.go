package anthropic

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

const sampleRequest = `{
  "model": "claude-sonnet-x",
  "max_tokens": 1024,
  "metadata": {"user_id": "u<1>&2"},
  "system": "You are <agent> & friends.\n\nDoctrine.",
  "messages": [
    {"role": "user", "content": "fix the parser"},
    {"role": "assistant", "content": [
      {"type": "thinking", "thinking": "plan it", "signature": "SIG"},
      {"type": "text", "text": "reading the file"},
      {"type": "tool_use", "id": "toolu_1", "name": "read_file", "input": {"path": "a.go", "n": 2}}
    ]},
    {"role": "user", "content": [
      {"type": "tool_result", "tool_use_id": "toolu_1", "content": "package a"},
      {"type": "text", "text": "thanks"}
    ]},
    {"role": "assistant", "content": [{"type": "image", "source": {"type": "base64", "data": "AAA"}}]}
  ],
  "tools": [{"name": "read_file", "description": "reads", "input_schema": {"type": "object"}}],
  "stream": true
}`

func mustDecode(t *testing.T, body string) (*Request, []kernel.Message) {
	t.Helper()
	req, msgs, err := Decode([]byte(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return req, msgs
}

func TestDecodeKinds(t *testing.T) {
	_, msgs := mustDecode(t, sampleRequest)
	type want struct {
		role kernel.Role
		kind kernel.Kind
		text string
		id   string
		name string
	}
	wants := []want{
		{kernel.RoleSystem, kernel.KindText, "You are <agent> & friends.\n\nDoctrine.", "", ""},
		{kernel.RoleUser, kernel.KindText, "fix the parser", "", ""},
		{kernel.RoleAssistant, kernel.KindReasoning, "plan it", "", ""},
		{kernel.RoleAssistant, kernel.KindText, "reading the file", "", ""},
		{kernel.RoleAssistant, kernel.KindToolCall, `{"path":"a.go","n":2}`, "toolu_1", "read_file"},
		{kernel.RoleTool, kernel.KindToolResult, "package a", "toolu_1", "read_file"},
		{kernel.RoleUser, kernel.KindText, "thanks", "", ""},
		{kernel.RoleAssistant, kernel.KindText, "[image]", "", ""},
	}
	if len(msgs) != len(wants) {
		t.Fatalf("got %d messages, want %d: %+v", len(msgs), len(wants), msgs)
	}
	for i, w := range wants {
		m := msgs[i]
		if m.Seq != int64(i+1) || m.Ref != kernel.FormatRef(int64(i+1)) {
			t.Errorf("msg %d: seq/ref = %d/%s", i, m.Seq, m.Ref)
		}
		if m.Role != w.role || m.Kind != w.kind || m.Text != w.text || m.ToolCallID != w.id || m.ToolName != w.name {
			t.Errorf("msg %d: got %+v, want %+v", i, m, w)
		}
	}
}

func TestDecodeStringContentAndNoSystem(t *testing.T) {
	_, msgs := mustDecode(t, `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"}]}`)
	if len(msgs) != 2 || msgs[0].Role != kernel.RoleUser || msgs[0].Text != "hi" ||
		msgs[1].Role != kernel.RoleAssistant || msgs[1].Text != "yo" {
		t.Fatalf("unexpected: %+v", msgs)
	}
}

func TestDecodeSystemArray(t *testing.T) {
	_, msgs := mustDecode(t, `{"system":[{"type":"text","text":"a"},{"type":"text","text":"b"}],"messages":[]}`)
	if len(msgs) != 1 || msgs[0].Role != kernel.RoleSystem || msgs[0].Text != "a\n\nb" {
		t.Fatalf("unexpected system: %+v", msgs)
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := map[string]string{
		"not object":     `[]`,
		"no messages":    `{"model":"x"}`,
		"bad role":       `{"messages":[{"role":"system","content":"x"}]}`,
		"bad content":    `{"messages":[{"role":"user","content":42}]}`,
		"bad json":       `{"messages":`,
		"messages bogus": `{"messages":{}}`,
	}
	for name, body := range cases {
		if _, _, err := Decode([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestEncodeRoundTripIsByteStable(t *testing.T) {
	req, msgs := mustDecode(t, sampleRequest)
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid JSON: %s", out)
	}
	in, _ := parseObject([]byte(sampleRequest))
	got, err := parseObject(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range in {
		if f.key == "messages" {
			continue
		}
		g := lookup(got, f.key)
		if !bytes.Equal(f.raw, g) {
			t.Errorf("field %s changed:\n in: %s\nout: %s", f.key, f.raw, g)
		}
	}
	// The system prompt keeps its exact bytes, including HTML-sensitive characters.
	if !bytes.Contains(out, []byte(`"system": "You are <agent> & friends.`)) && !bytes.Contains(out, []byte(`"system":"You are <agent> & friends.`)) {
		t.Errorf("system prompt not byte-identical in output: %s", out)
	}
	// Re-decoding gives the same kernel view.
	_, again := mustDecode(t, string(out))
	if len(again) != len(msgs) {
		t.Fatalf("round trip count %d != %d", len(again), len(msgs))
	}
	for i := range msgs {
		if again[i].Kind != msgs[i].Kind || again[i].Text != msgs[i].Text || again[i].ToolCallID != msgs[i].ToolCallID {
			t.Errorf("msg %d changed: %+v -> %+v", i, msgs[i], again[i])
		}
	}
}

func TestEncodeMergesAndKeepsPairing(t *testing.T) {
	req, msgs := mustDecode(t, sampleRequest)
	// Window: system, first user, a summary carrier, the tool pair, then the last user text.
	carrier := kernel.Message{Role: kernel.RoleUser, Kind: kernel.KindText, Text: "<history block=\"b1\">done</history>"}
	window := []kernel.Message{msgs[0], msgs[1], carrier, msgs[2], msgs[3], msgs[4], msgs[5], msgs[6]}
	out, err := Encode(req, window)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				ID        string `json:"id"`
				ToolUseID string `json:"tool_use_id"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	// Expect: user[text, carrier-text] , assistant[thinking-dropped? no: kept text, tool_use], user[tool_result, text]
	if len(body.Messages) != 3 {
		t.Fatalf("want 3 messages, got %d: %s", len(body.Messages), out)
	}
	if body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 2 {
		t.Errorf("first message not merged: %s", out)
	}
	asst := body.Messages[1]
	if asst.Role != "assistant" || len(asst.Content) != 3 || asst.Content[2].Type != "tool_use" || asst.Content[2].ID != "toolu_1" {
		t.Errorf("assistant message wrong: %s", out)
	}
	last := body.Messages[2]
	if last.Role != "user" || len(last.Content) != 2 || last.Content[0].Type != "tool_result" || last.Content[0].ToolUseID != "toolu_1" {
		t.Errorf("tool_result must lead the user message: %s", out)
	}
}

func TestEncodeDropsOrphanPairs(t *testing.T) {
	req, msgs := mustDecode(t, sampleRequest)
	cases := map[string][]kernel.Message{
		// tool_result without its tool_use (the call was compressed away).
		"orphan result": {msgs[1], msgs[5], msgs[6]},
		// tool_use without its result (the result was compressed away).
		"orphan use": {msgs[1], msgs[2], msgs[3], msgs[4], msgs[6]},
		// result placed after a text block in the same user run: tool_result must be moved first.
		"result after text": {msgs[1], msgs[4], msgs[6], msgs[5]},
	}
	for name, window := range cases {
		out, err := Encode(req, window)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		assertPairsValid(t, name, out)
	}
}

func assertPairsValid(t *testing.T, name string, out []byte) {
	t.Helper()
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				ToolUseID string `json:"tool_use_id"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	ids := func(m int, typ string) map[string]bool {
		set := map[string]bool{}
		for _, c := range body.Messages[m].Content {
			if c.Type == typ {
				if typ == "tool_use" {
					set[c.ID] = true
				} else {
					set[c.ToolUseID] = true
				}
			}
		}
		return set
	}
	for i, m := range body.Messages {
		if len(m.Content) == 0 {
			t.Errorf("%s: message %d empty", name, i)
		}
		if i > 0 && body.Messages[i-1].Role == m.Role {
			t.Errorf("%s: consecutive %s messages at %d", name, m.Role, i)
		}
		// Every tool_use must be answered by the immediately following user message.
		if m.Role == "assistant" {
			uses := ids(i, "tool_use")
			if len(uses) > 0 {
				if i+1 >= len(body.Messages) || body.Messages[i+1].Role != "user" {
					t.Errorf("%s: tool_use at %d not followed by user message", name, i)
					continue
				}
				results := ids(i+1, "tool_result")
				for id := range uses {
					if !results[id] {
						t.Errorf("%s: tool_use %s has no result", name, id)
					}
				}
			}
		}
		// Every tool_result must answer a tool_use in the immediately preceding assistant message.
		if m.Role == "user" {
			results := ids(i, "tool_result")
			if len(results) > 0 {
				if i == 0 || body.Messages[i-1].Role != "assistant" {
					t.Errorf("%s: tool_result at %d without preceding assistant", name, i)
					continue
				}
				uses := ids(i-1, "tool_use")
				for id := range results {
					if !uses[id] {
						t.Errorf("%s: orphan tool_result %s", name, id)
					}
				}
				if m.Content[0].Type != "tool_result" {
					t.Errorf("%s: tool_result must lead message %d", name, i)
				}
			}
		}
	}
}

func TestEncodeSynthesizedToolPair(t *testing.T) {
	req, _ := mustDecode(t, `{"messages":[{"role":"user","content":"go"}]}`)
	window := []kernel.Message{
		{Role: kernel.RoleUser, Kind: kernel.KindText, Text: "go"},
		{Role: kernel.RoleAssistant, Kind: kernel.KindToolCall, ToolName: "grep", ToolCallID: "c9", Text: `{"q": "<x>"}`},
		{Role: kernel.RoleTool, Kind: kernel.KindToolResult, ToolCallID: "c9", Text: "<hit>"},
		{Role: kernel.RoleAssistant, Kind: kernel.KindReasoning, Text: "dropped: no signature"},
		{Role: kernel.RoleAssistant, Kind: kernel.KindText, Text: "found it"},
	}
	out, err := Encode(req, window)
	if err != nil {
		t.Fatal(err)
	}
	assertPairsValid(t, "synth", out)
	if bytes.Contains(out, []byte("dropped")) {
		t.Errorf("reasoning without signature must not be sent: %s", out)
	}
	if !bytes.Contains(out, []byte(`"input":{"q": "<x>"}`)) && !bytes.Contains(out, []byte(`"input":{"q":"<x>"}`)) {
		t.Errorf("tool input lost: %s", out)
	}
	if !bytes.Contains(out, []byte(`<hit>`)) {
		t.Errorf("'<' must not be escaped in synthesized blocks: %s", out)
	}
}

func TestEncodeSkipsSystemAndRejectsEmpty(t *testing.T) {
	req, _ := mustDecode(t, sampleRequest)
	if _, err := Encode(req, []kernel.Message{{Role: kernel.RoleSystem, Kind: kernel.KindText, Text: "x"}}); err == nil {
		t.Error("expected error for a window with no sendable messages")
	}
}

func TestEncodeDoesNotMutateRequest(t *testing.T) {
	req, msgs := mustDecode(t, sampleRequest)
	before := len(req.fields)
	if _, err := Encode(req, msgs[1:2]); err != nil {
		t.Fatal(err)
	}
	if len(req.fields) != before {
		t.Fatal("Encode changed the request's fields")
	}
	if lookup(req.fields, "messages") == nil || !strings.Contains(string(lookup(req.fields, "messages")), "fix the parser") {
		t.Fatal("original messages field was modified")
	}
}

func TestInjectToolsAndDetect(t *testing.T) {
	if HasTool([]byte(sampleRequest), ToolCompress) {
		t.Fatal("sample must not declare compress")
	}
	out, err := InjectTools([]byte(sampleRequest))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid JSON: %s", out)
	}
	for _, n := range []string{ToolCompress, ToolDecompress, "read_file"} {
		if !HasTool(out, n) {
			t.Errorf("tool %s missing after injection", n)
		}
	}
	// The existing tool definition is copied byte for byte from the sample.
	if !bytes.Contains(out, []byte(`{"name": "read_file", "description": "reads", "input_schema": {"type": "object"}}`)) {
		t.Errorf("existing tool altered: %s", out)
	}
	again, err := InjectTools(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, out) {
		t.Error("InjectTools is not idempotent")
	}
}

func TestInjectToolsWithoutToolsField(t *testing.T) {
	out, err := InjectTools([]byte(`{"messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !HasTool(out, ToolCompress) || !HasTool(out, ToolDecompress) {
		t.Fatalf("tools not added: %s", out)
	}
}

func TestHasToolMalformed(t *testing.T) {
	for _, b := range []string{``, `[]`, `{"tools":5}`, `{"tools":[1]}`} {
		if HasTool([]byte(b), ToolCompress) {
			t.Errorf("%q: expected false", b)
		}
	}
	if _, err := InjectTools([]byte(`{"tools":5}`)); err == nil {
		t.Error("expected error for non-array tools")
	}
}

func TestToolDefinitionsAreValidJSON(t *testing.T) {
	for _, d := range ToolDefinitions() {
		if !json.Valid(d) {
			t.Errorf("invalid definition: %s", d)
		}
	}
	defs := ToolDefinitions()
	defs[0][0] = 'X' // copies must not alias package state
	if ToolDefinitions()[0][0] != '{' {
		t.Error("ToolDefinitions aliases shared state")
	}
}
