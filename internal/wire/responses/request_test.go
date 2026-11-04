package responses

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

const sampleBody = `{"model":"gpt-x","instructions":"You are helpful.","input":[
 {"role":"user","content":"fix the bug in parser.go"},
 {"type":"message","role":"assistant","content":[{"type":"output_text","text":"Looking."}],"id":"msg_1","status":"completed"},
 {"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"think"}],"encrypted_content":"opaque=="},
 {"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"parser.go\"}"},
 {"type":"function_call_output","call_id":"call_1","output":"package parser"},
 {"role":"user","content":[{"type":"input_text","text":"thanks"}]}
],"tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}],"store":false,"metadata":{"k":"v"}}`

func mustDecode(t *testing.T, body string) (*Request, []kernel.Message) {
	t.Helper()
	req, msgs, err := Decode([]byte(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return req, msgs
}

func TestDecodeMapsItemsAndLinksCalls(t *testing.T) {
	_, msgs := mustDecode(t, sampleBody)
	if len(msgs) != 7 {
		t.Fatalf("got %d messages, want 7 (instructions + 6 items)", len(msgs))
	}
	if msgs[0].Role != kernel.RoleSystem || msgs[0].Text != "You are helpful." || msgs[0].Seq != 1 {
		t.Fatalf("instructions message: %+v", msgs[0])
	}
	if msgs[3].Kind != kernel.KindReasoning || msgs[3].Text != "think" {
		t.Fatalf("reasoning message: %+v", msgs[3])
	}
	call := msgs[4]
	if call.Kind != kernel.KindToolCall || call.ToolName != "read_file" || call.ToolCallID != "call_1" ||
		call.Text != `{"path":"parser.go"}` {
		t.Fatalf("function_call message: %+v", call)
	}
	out := msgs[5]
	if out.Kind != kernel.KindToolResult || out.Role != kernel.RoleTool || out.ToolName != "read_file" ||
		out.ToolCallID != "call_1" || out.Text != "package parser" {
		t.Fatalf("function_call_output linked wrongly: %+v", out)
	}
	if msgs[6].Text != "thanks" || msgs[6].Role != kernel.RoleUser {
		t.Fatalf("content-part message: %+v", msgs[6])
	}
	for i, m := range msgs {
		if want := kernel.FormatRef(int64(i + 1)); m.Ref != want {
			t.Fatalf("message %d ref %q, want %q", i, m.Ref, want)
		}
	}
}

func TestDecodeStringInputIsOneUserMessage(t *testing.T) {
	_, msgs := mustDecode(t, `{"model":"m","input":"hello"}`)
	if len(msgs) != 1 || msgs[0].Role != kernel.RoleUser || msgs[0].Text != "hello" {
		t.Fatalf("got %+v", msgs)
	}
}

// TestDecodeRejectsUnsupportedInput covers shapes the codec refuses. An unknown
// item type is not among them: it is opaque (see TestDecodeUnknownItemsAreOpaque).
func TestDecodeRejectsUnsupportedInput(t *testing.T) {
	cases := map[string]string{
		"bad role":         `{"input":[{"role":"tool","content":"x"}]}`,
		"input missing":    `{"model":"m"}`,
		"instructions obj": `{"instructions":{"a":1},"input":"x"}`,
		"input number":     `{"input":3}`,
		"not an object":    `[1,2]`,
	}
	for name, body := range cases {
		if _, _, err := Decode([]byte(body)); err == nil {
			t.Errorf("%s: Decode accepted %s", name, body)
		}
	}
}

func TestDecodeStatefulReturnsErrStateful(t *testing.T) {
	stateful := []string{
		`{"previous_response_id":"resp_1","input":"x"}`,
		`{"conversation":"conv_1","input":"x"}`,
		`{"conversation":{"id":"conv_1"},"input":"x"}`,
	}
	for _, body := range stateful {
		_, _, err := Decode([]byte(body))
		if !errors.Is(err, ErrStateful) {
			t.Errorf("Decode(%s) err = %v, want ErrStateful", body, err)
		}
	}
	stateless := []string{
		`{"previous_response_id":null,"input":"x"}`,
		`{"previous_response_id":"","input":"x"}`,
		`{"conversation":null,"input":"x"}`,
	}
	for _, body := range stateless {
		if _, _, err := Decode([]byte(body)); err != nil {
			t.Errorf("Decode(%s) err = %v, want nil", body, err)
		}
	}
}

func TestEncodeFullWindowPreservesUnknownFields(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid JSON: %s", out)
	}
	// Top-level fields other than input are byte-identical.
	for _, frag := range []string{
		`"model":"gpt-x"`,
		`"instructions":"You are helpful."`,
		`"tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}]`,
		`"store":false`,
		`"metadata":{"k":"v"}`,
	} {
		if !strings.Contains(string(out), frag) {
			t.Errorf("output lost %s:\n%s", frag, out)
		}
	}
	// Unchanged items keep their bytes, including opaque reasoning.
	for _, frag := range []string{
		`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"think"}],"encrypted_content":"opaque=="}`,
		`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"parser.go\"}"}`,
		`{"type":"function_call_output","call_id":"call_1","output":"package parser"}`,
	} {
		if !strings.Contains(string(out), frag) {
			t.Errorf("unchanged item not byte-identical, missing %s:\n%s", frag, out)
		}
	}
	// Labels are added to text, and the instructions message is not repeated in input.
	if !strings.Contains(string(out), `"content":"[m00002] fix the bug in parser.go"`) {
		t.Errorf("user text not labelled:\n%s", out)
	}
	if strings.Contains(string(out), `"role":"system"`) {
		t.Errorf("instructions leaked into input:\n%s", out)
	}
	var env struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(out, &env); err != nil || len(env.Input) != 6 {
		t.Fatalf("input items = %d, err %v, want 6", len(env.Input), err)
	}
}

func TestEncodeDropsCallWithoutOutputAndOutputWithoutCall(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Keep the call (seq 5) but drop its output (seq 6).
	var noOutput []kernel.Message
	for _, m := range msgs {
		if m.Seq != 6 {
			noOutput = append(noOutput, m)
		}
	}
	out, err := Encode(req, noOutput)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"function_call"`) {
		t.Errorf("call without output was kept:\n%s", out)
	}
	// Keep the output (seq 6) but drop its call (seq 5).
	var noCall []kernel.Message
	for _, m := range msgs {
		if m.Seq != 5 {
			noCall = append(noCall, m)
		}
	}
	out, err = Encode(req, noCall)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"function_call_output"`) {
		t.Errorf("orphan output was kept:\n%s", out)
	}
}

func TestEncodeDropsOutputThatPrecedesItsCall(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Swap the call and its output so the output comes first. The first user
	// message keeps the window sendable.
	swapped := []kernel.Message{msgs[1], msgs[5], msgs[4]}
	out, err := Encode(req, swapped)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"call_id":"call_1"`) {
		t.Errorf("pair kept although the output precedes the call:\n%s", out)
	}
}

func TestEncodeFreshCompressRewriteUsesReplacedArgs(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Rewrite the call arguments, as a compression would not, to exercise the
	// fresh path: the rewritten text no longer matches the source.
	window := append([]kernel.Message(nil), msgs...)
	window[4].Text = `{"ranges":[{"startRef":"m00002","endRef":"m00003","summary":"s"}]}`
	out, err := Encode(req, window)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"arguments":"{\"ranges\":[{\"startRef\":\"m00002\"`) {
		t.Errorf("fresh call arguments missing:\n%s", out)
	}
	// Non-object arguments become {}.
	window[4].Text = "not json"
	out, err = Encode(req, window)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"arguments":"{}"`) {
		t.Errorf("non-object arguments not replaced with {}:\n%s", out)
	}
}

func TestEncodeFreshSummaryMessage(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	summary := kernel.Message{
		Seq: 99, Ref: "", Role: kernel.RoleUser, Kind: kernel.KindText, Text: "summary of earlier work",
	}
	out, err := Encode(req, append([]kernel.Message{summary}, msgs[1:]...))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `{"role":"user","content":"summary of earlier work"}`) {
		t.Errorf("fresh summary not emitted:\n%s", out)
	}
}

func TestEncodeStringInputBecomesArray(t *testing.T) {
	req, msgs := mustDecode(t, `{"model":"m","input":"hello"}`)
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatal(err)
	}
	want := `"input":[{"role":"user","content":"[m00001] hello"}]`
	if !strings.Contains(string(out), want) {
		t.Errorf("got %s, want fragment %s", out, want)
	}
}

func TestEncodeLabelsArrayContent(t *testing.T) {
	req, msgs := mustDecode(t, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"x"},{"type":"input_text","text":"look"}]}]}`)
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatal(err)
	}
	want := `"content":[{"type":"input_text","text":"[m00001] "},{"type":"input_image","image_url":"x"},{"type":"input_text","text":"look"}]`
	if !strings.Contains(string(out), want) {
		t.Errorf("got %s, want fragment %s", out, want)
	}
}

func TestEncodeEmptyWindowFails(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	if _, err := Encode(req, nil); err == nil {
		t.Error("empty window encoded")
	}
	// The instructions message alone is not sent as input, so it is not sendable.
	if _, err := Encode(req, msgs[:1]); err == nil {
		t.Error("instructions-only window encoded")
	}
}

func TestEncodeDropsReasoningWhoseFollowerIsGone(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Drop the call's output (seq 6). The call is then dropped, and the reasoning
	// item before it has no following item and must go too.
	var window []kernel.Message
	for _, m := range msgs {
		if m.Seq != 6 {
			window = append(window, m)
		}
	}
	out, err := Encode(req, window)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"rs_1"`) {
		t.Errorf("reasoning kept without its following item:\n%s", out)
	}
	// With the call kept and its output kept, the reasoning item stays.
	out, err = Encode(req, msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"rs_1"`) {
		t.Errorf("reasoning dropped although its following item is kept:\n%s", out)
	}
}

func TestEncodeNilRequest(t *testing.T) {
	if _, err := Encode(nil, nil); err == nil {
		t.Fatal("Encode(nil) returned no error")
	}
}
