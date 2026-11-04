package openai

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// sampleBody has every role, a parallel tool call pair, an array content with an
// image part, and unknown top-level and message-level fields.
const sampleBody = `{"model":"gpt-4o","temperature":0.2,"x_custom":{"a": 1},"messages":[` +
	`{"role":"system","content":"sys prompt"},` +
	`{"role":"developer","content":"dev rules"},` +
	`{"role":"user","content":"first question"},` +
	`{"role":"assistant","content":"looking","x_meta":{"k":true},"tool_calls":[` +
	`{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}},` +
	`{"id":"call_2","type":"function","function":{"name":"grep","arguments":"{}"}}]},` +
	`{"role":"tool","tool_call_id":"call_1","content":"file body"},` +
	`{"role":"tool","tool_call_id":"call_2","content":"no match"},` +
	`{"role":"user","content":[{"type":"text","text":"now"},{"type":"image_url","image_url":{"url":"x"}}]}` +
	`]}`

func mustDecode(t *testing.T, body string) (*Request, []kernel.Message) {
	t.Helper()
	req, msgs, err := Decode([]byte(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return req, msgs
}

// window selects the messages with the given seqs, in order. Seq 0 is a
// synthetic message with the given text, as a summary block is.
type synth struct {
	at   int // insert before this seq
	text string
}

func window(msgs []kernel.Message, seqs []int64, extra ...synth) []kernel.Message {
	byseq := map[int64]kernel.Message{}
	for _, m := range msgs {
		byseq[m.Seq] = m
	}
	var out []kernel.Message
	for _, s := range seqs {
		for _, e := range extra {
			if int64(e.at) == s {
				out = append(out, kernel.Message{Role: kernel.RoleUser, Kind: kernel.KindText, Text: e.text})
			}
		}
		out = append(out, byseq[s])
	}
	return out
}

// checkPairs fails the test when a tool result is not answered by the directly
// preceding assistant message, or when a tool call has no result.
func checkPairs(t *testing.T, out []byte) {
	t.Helper()
	var doc struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	pending := map[string]bool{}
	for _, m := range doc.Messages {
		if m.Role == "tool" {
			if !pending[m.ToolCallID] {
				t.Errorf("orphan tool result %q in\n%s", m.ToolCallID, out)
			}
			delete(pending, m.ToolCallID)
			continue
		}
		if len(pending) > 0 {
			t.Errorf("tool calls without results %v in\n%s", pending, out)
		}
		pending = map[string]bool{}
		if m.Role == "assistant" {
			for _, c := range m.ToolCalls {
				pending[c.ID] = true
			}
		}
	}
	if len(pending) > 0 {
		t.Errorf("tool calls without results at end %v", pending)
	}
}

func roles(t *testing.T, out []byte) []string {
	t.Helper()
	var doc struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	var r []string
	for _, m := range doc.Messages {
		r = append(r, m.Role)
	}
	return r
}

func TestDecodeMapping(t *testing.T) {
	_, msgs := mustDecode(t, sampleBody)
	type want struct {
		role   kernel.Role
		kind   kernel.Kind
		text   string
		tool   string
		callID string
	}
	wants := []want{
		{kernel.RoleSystem, kernel.KindText, "sys prompt", "", ""},
		{kernel.RoleSystem, kernel.KindText, "dev rules", "", ""},
		{kernel.RoleUser, kernel.KindText, "first question", "", ""},
		{kernel.RoleAssistant, kernel.KindText, "looking", "", ""},
		{kernel.RoleAssistant, kernel.KindToolCall, `{"path":"a.go"}`, "read_file", "call_1"},
		{kernel.RoleAssistant, kernel.KindToolCall, "{}", "grep", "call_2"},
		{kernel.RoleTool, kernel.KindToolResult, "file body", "read_file", "call_1"},
		{kernel.RoleTool, kernel.KindToolResult, "no match", "grep", "call_2"},
		{kernel.RoleUser, kernel.KindText, "now\n[image_url]", "", ""},
	}
	if len(msgs) != len(wants) {
		t.Fatalf("got %d messages, want %d", len(msgs), len(wants))
	}
	for i, w := range wants {
		m := msgs[i]
		if m.Seq != int64(i+1) || m.Ref != kernel.FormatRef(int64(i+1)) {
			t.Errorf("msg %d: seq/ref = %d/%s", i, m.Seq, m.Ref)
		}
		got := want{m.Role, m.Kind, m.Text, m.ToolName, m.ToolCallID}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("msg %d: got %+v, want %+v", i, got, w)
		}
	}
}

func TestEncodeIdentityPreservesBytes(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	for _, keep := range []string{
		`"model":"gpt-4o"`,
		`"temperature":0.2`,
		`"x_custom":{"a": 1}`,
		`{"role":"system","content":"sys prompt"}`,
		`{"role":"developer","content":"dev rules"}`,
		`{"role":"tool","tool_call_id":"call_1","content":"file body"}`,
		`{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}`,
		`"x_meta":{"k":true}`,
		`{"type":"image_url","image_url":{"url":"x"}}`,
	} {
		if !strings.Contains(string(out), keep) {
			t.Errorf("output lost %s\n%s", keep, out)
		}
	}
	// Text is labelled with its reference, system text is not.
	for _, want := range []string{`"[m00003] first question"`, `"[m00004] looking"`, `{"type":"text","text":"[m00009] "}`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing label %s\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "[m00001]") || strings.Contains(string(out), "[m00002]") {
		t.Errorf("system text must not be labelled\n%s", out)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid JSON\n%s", out)
	}
}

func TestEncodeCompressedWindowKeepsPairing(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Seqs 3 (first user) and 6..8 (call_2 and its result) are compressed away. A
	// summary block sits before the last user message.
	out, err := Encode(req, window(msgs, []int64{1, 2, 4, 5, 7, 9}, synth{at: 9, text: "<history>summary</history>"}))
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	want := []string{"system", "developer", "assistant", "tool", "user", "user"}
	if got := roles(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("roles = %v, want %v\n%s", got, want, out)
	}
	if strings.Contains(string(out), "first question") {
		t.Errorf("compressed user message leaked\n%s", out)
	}
}

func TestEncodeKeepsCallWithResultAndDropsRest(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Keep the assistant text and call_1 with its result. Drop call_2 and its result.
	out, err := Encode(req, window(msgs, []int64{4, 5, 7}))
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	if strings.Contains(string(out), "call_2") {
		t.Errorf("call without a result must be removed\n%s", out)
	}
	// Assistant text and the kept call remain, with a labelled text.
	if !strings.Contains(string(out), `"tool_calls":[{"id":"call_1"`) {
		t.Errorf("kept call missing\n%s", out)
	}
	if !strings.Contains(string(out), `"content":"[m00004] looking"`) {
		t.Errorf("labelled text missing\n%s", out)
	}
	if strings.Contains(string(out), "no match") {
		t.Errorf("orphan result for dropped call must be removed\n%s", out)
	}
}

func TestEncodeCallWithoutTextGetsNullContent(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Keep only call_1 and its result: the assistant text (4) is compressed away.
	out, err := Encode(req, window(msgs, []int64{5, 7}))
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	if !strings.Contains(string(out), `"content":null`) {
		t.Errorf("assistant without text must have null content\n%s", out)
	}
	if strings.Contains(string(out), "looking") {
		t.Errorf("compressed text leaked\n%s", out)
	}
}

func TestEncodeAssistantTextOnlyWhenCallsDropped(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	out, err := Encode(req, window(msgs, []int64{4, 5}))
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	if strings.Contains(string(out), "tool_calls") {
		t.Errorf("tool_calls field must be removed when no call survives\n%s", out)
	}
	if !strings.Contains(string(out), `"x_meta":{"k":true}`) {
		t.Errorf("unknown assistant field must survive\n%s", out)
	}
}

func TestEncodeOrphanResultDropped(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	out, err := Encode(req, window(msgs, []int64{1, 7}))
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	if got := roles(t, out); !reflect.DeepEqual(got, []string{"system"}) {
		t.Errorf("roles = %v, want [system]", got)
	}
}

func TestEncodeSummaryBlockFresh(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	summary := kernel.Message{Role: kernel.RoleUser, Kind: kernel.KindText, Text: "<history>x</history>"}
	out, err := Encode(req, []kernel.Message{msgs[0], summary, msgs[8]})
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	if got := roles(t, out); !reflect.DeepEqual(got, []string{"system", "user", "user"}) {
		t.Errorf("roles = %v", got)
	}
	if !strings.Contains(string(out), `"<history>x</history>"`) {
		t.Errorf("summary missing\n%s", out)
	}
}

func TestEncodeFreshToolCallAndResult(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	call := kernel.Message{Role: kernel.RoleAssistant, Kind: kernel.KindToolCall,
		ToolName: "compress", ToolCallID: "call_new", Text: `{"ranges":[]}`}
	res := kernel.Message{Role: kernel.RoleTool, Kind: kernel.KindToolResult,
		ToolName: "compress", ToolCallID: "call_new", Text: "ok"}
	out, err := Encode(req, []kernel.Message{msgs[0], call, res})
	if err != nil {
		t.Fatal(err)
	}
	checkPairs(t, out)
	if !strings.Contains(string(out), `"arguments":"{\"ranges\":[]}"`) {
		t.Errorf("fresh call arguments wrong\n%s", out)
	}
}

func TestEncodeNoSendableMessages(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	if _, err := Encode(req, []kernel.Message{msgs[6]}); err == nil {
		t.Error("expected error when the window holds only an orphan result")
	}
	if _, err := Encode(req, []kernel.Message{msgs[4]}); err == nil {
		t.Error("expected error when the window holds only a call without its result")
	}
	if _, err := Encode(req, nil); err == nil {
		t.Error("expected error for an empty window")
	}
}

func TestEncodeDoesNotMutateRequest(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	first, err := Encode(req, window(msgs, []int64{4, 5}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(req, window(msgs, []int64{4, 5}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("Encode is not repeatable")
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := map[string]string{
		"not object":      `[]`,
		"no messages":     `{"model":"m"}`,
		"null messages":   `{"messages":null}`,
		"bad role":        `{"messages":[{"role":"bogus","content":"x"}]}`,
		"legacy function": `{"messages":[{"role":"function","name":"f","content":"x"}]}`,
		"bad tool_calls":  `{"messages":[{"role":"assistant","tool_calls":"x"}]}`,
		"bad content":     `{"messages":[{"role":"user","content":5}]}`,
		"trailing":        `{"messages":[]} x`,
	}
	for name, body := range cases {
		if _, _, err := Decode([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestDecodeEmptyMessagesAndNullContent(t *testing.T) {
	req, msgs, err := Decode([]byte(`{"messages":[{"role":"user"},{"role":"assistant","content":null}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || req == nil {
		t.Fatalf("got %d messages", len(msgs))
	}
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid JSON %s", out)
	}
}
