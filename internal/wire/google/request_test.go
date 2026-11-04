package google

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// sampleBody has a systemInstruction, text turns, a parallel functionCall pair
// with a thoughtSignature (an unknown part member), functionResponses, an
// inlineData part, and unknown top-level fields.
const sampleBody = `{"model":"models/gemini-2.5-pro","x_custom":{"a":1},` +
	`"systemInstruction":{"parts":[{"text":"sys prompt"}]},` +
	`"contents":[` +
	`{"role":"user","parts":[{"text":"first question"}]},` +
	`{"role":"model","parts":[{"text":"looking"},` +
	`{"functionCall":{"name":"read_file","args":{"path":"a.go"}},"thoughtSignature":"sig1"},` +
	`{"functionCall":{"name":"grep","args":{}}}]},` +
	`{"role":"user","parts":[` +
	`{"functionResponse":{"name":"read_file","response":{"output":"file body"}}},` +
	`{"functionResponse":{"name":"grep","response":{"output":"no match"}}}]},` +
	`{"role":"user","parts":[{"text":"now"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}` +
	`]}`

func mustDecode(t *testing.T, body string) (*Request, []kernel.Message) {
	t.Helper()
	req, msgs, err := Decode([]byte(body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return req, msgs
}

// pick returns the window messages with the given seqs, in order.
func pick(msgs []kernel.Message, seqs ...int64) []kernel.Message {
	var out []kernel.Message
	for _, s := range seqs {
		for _, m := range msgs {
			if m.Seq == s {
				out = append(out, m)
			}
		}
	}
	return out
}

func TestDecodeKindsAndLinking(t *testing.T) {
	_, msgs := mustDecode(t, sampleBody)
	type row struct {
		role kernel.Role
		kind kernel.Kind
		name string
		id   string
		text string
	}
	want := []row{
		{kernel.RoleSystem, kernel.KindText, "", "", "sys prompt"},
		{kernel.RoleUser, kernel.KindText, "", "", "first question"},
		{kernel.RoleAssistant, kernel.KindText, "", "", "looking"},
		{kernel.RoleAssistant, kernel.KindToolCall, "read_file", "gc:read_file:1", `{"path":"a.go"}`},
		{kernel.RoleAssistant, kernel.KindToolCall, "grep", "gc:grep:1", "{}"},
		{kernel.RoleTool, kernel.KindToolResult, "read_file", "gc:read_file:1", `{"output":"file body"}`},
		{kernel.RoleTool, kernel.KindToolResult, "grep", "gc:grep:1", `{"output":"no match"}`},
		{kernel.RoleUser, kernel.KindText, "", "", "now"},
		{kernel.RoleUser, kernel.KindText, "", "", "[inlineData]"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("got %d messages, want %d", len(msgs), len(want))
	}
	for i, w := range want {
		m := msgs[i]
		if m.Seq != int64(i+1) || m.Ref != kernel.FormatRef(int64(i+1)) {
			t.Errorf("msg %d: seq/ref %d %q", i, m.Seq, m.Ref)
		}
		got := row{m.Role, m.Kind, m.ToolName, m.ToolCallID, m.Text}
		if got != w {
			t.Errorf("msg %d: got %+v, want %+v", i, got, w)
		}
	}
}

func TestDecodeSynthesizedIDsCountPerName(t *testing.T) {
	body := `{"contents":[` +
		`{"role":"model","parts":[{"functionCall":{"name":"f","args":{}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"f","response":{}}}]},` +
		`{"role":"model","parts":[{"functionCall":{"name":"f","args":{}}},{"functionCall":{"name":"g","args":{}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"g","response":{}}},{"functionResponse":{"name":"f","response":{}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"h","response":{}}}]}]}`
	_, msgs := mustDecode(t, body)
	var calls, results []string
	for _, m := range msgs {
		switch m.Kind {
		case kernel.KindToolCall:
			calls = append(calls, m.ToolCallID)
		case kernel.KindToolResult:
			results = append(results, m.ToolCallID)
		}
	}
	if want := []string{"gc:f:1", "gc:f:2", "gc:g:1"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %v, want %v", calls, want)
	}
	if want := []string{"gc:f:1", "gc:g:1", "gc:f:2", ""}; !reflect.DeepEqual(results, want) {
		t.Errorf("results %v, want %v (orphan result has empty id)", results, want)
	}
}

func TestDecodeErrors(t *testing.T) {
	for name, body := range map[string]string{
		"not object":    `[1]`,
		"no contents":   `{"model":"x"}`,
		"bad role":      `{"contents":[{"role":"tool","parts":[]}]}`,
		"parts not arr": `{"contents":[{"role":"user","parts":"x"}]}`,
		"bad text":      `{"contents":[{"role":"user","parts":[{"text":1,"thought":"yes"}]}]}`,
	} {
		if _, _, err := Decode([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestEncodeFullWindowKeepsBytes(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Labels are added to user and model text, so compare after removing them.
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid JSON: %s", out)
	}
	for _, keep := range []string{`"x_custom":{"a":1}`, `"thoughtSignature":"sig1"`,
		`{"inlineData":{"mimeType":"image/png","data":"AAAA"}}`, `"args":{"path":"a.go"}`,
		`"response":{"output":"no match"}`, `"systemInstruction":{"parts":[{"text":"sys prompt"}]}`} {
		if !strings.Contains(string(out), keep) {
			t.Errorf("output lost %s:\n%s", keep, out)
		}
	}
	if !strings.Contains(string(out), `"text":"[m00003] looking"`) {
		t.Errorf("text part not labeled:\n%s", out)
	}
}

func TestEncodeDropsUnansweredCall(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// Keep the call to read_file but not its response: the call must go.
	win := pick(msgs, 1, 2, 3, 4, 5, 8, 9)
	out, err := Encode(req, win)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "functionCall") {
		t.Errorf("unanswered calls kept:\n%s", out)
	}
	if strings.Contains(string(out), "functionResponse") {
		t.Errorf("orphan responses kept:\n%s", out)
	}
	if !strings.Contains(string(out), `"text":"[m00003] looking"`) {
		t.Errorf("text lost:\n%s", out)
	}
}

func TestEncodeKeepsAnsweredPairOnly(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	// read_file pair kept, grep call kept without its response: grep call goes.
	// The system instruction is not in the window, so it is not sent.
	win := pick(msgs, 2, 4, 6, 9)
	out, err := Encode(req, win)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"name":"read_file"`) || strings.Contains(s, `"name":"grep"`) {
		t.Errorf("pairing wrong:\n%s", s)
	}
	if strings.Contains(s, "systemInstruction") {
		t.Errorf("system instruction not in window but still sent:\n%s", s)
	}
}

func TestEncodeFreshMessages(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	win := []kernel.Message{
		{Role: kernel.RoleUser, Kind: kernel.KindText, Text: "summary of earlier work"},
		{Role: kernel.RoleAssistant, Kind: kernel.KindToolCall, ToolName: "compress", ToolCallID: "fresh-1", Text: `{"ranges":[]}`},
		{Role: kernel.RoleTool, Kind: kernel.KindToolResult, ToolName: "compress", ToolCallID: "fresh-1", Text: "ok"},
		{Role: kernel.RoleAssistant, Kind: kernel.KindReasoning, Text: "hidden"},
	}
	win = append(win, pick(msgs, 8)...)
	out, err := Encode(req, win)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Contents []struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	// fresh user text, fresh model call, fresh user result, then the kept user turn.
	if len(doc.Contents) != 4 {
		t.Fatalf("contents %d, want 4:\n%s", len(doc.Contents), out)
	}
	if doc.Contents[3].Parts[0]["text"] != "[m00008] now" {
		t.Errorf("kept turn: %+v", doc.Contents[3])
	}
	if doc.Contents[0].Role != "user" || doc.Contents[0].Parts[0]["text"] != "summary of earlier work" {
		t.Errorf("fresh text: %+v", doc.Contents[0])
	}
	call := doc.Contents[1].Parts[0]["functionCall"].(map[string]any)
	if doc.Contents[1].Role != "model" || call["name"] != "compress" || call["args"].(map[string]any)["ranges"] == nil {
		t.Errorf("fresh call: %+v", doc.Contents[1])
	}
	resp := doc.Contents[2].Parts[0]["functionResponse"].(map[string]any)
	if resp["name"] != "compress" || resp["response"].(map[string]any)["content"] != "ok" {
		t.Errorf("fresh result: %+v", doc.Contents[2])
	}
	if strings.Contains(string(out), "hidden") {
		t.Errorf("reasoning sent back")
	}
}

func TestEncodeLabelsPlaceholderWithSeparatePart(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	out, err := Encode(req, pick(msgs, 9))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `{"text":"[m00009] "},{"inlineData"`) {
		t.Errorf("placeholder label not a separate part:\n%s", out)
	}
}

func TestEncodeEmptyWindowFails(t *testing.T) {
	req, _ := mustDecode(t, sampleBody)
	if _, err := Encode(req, nil); err == nil {
		t.Error("expected error for empty window")
	}
}

func TestEncodeIsValidJSONAfterDropsRoundTrip(t *testing.T) {
	req, msgs := mustDecode(t, sampleBody)
	out, err := Encode(req, pick(msgs, 2, 3, 6))
	if err != nil {
		t.Fatal(err)
	}
	req2, msgs2, err := Decode(out)
	if err != nil {
		t.Fatalf("re-decode: %v\n%s", err, out)
	}
	if len(msgs2) == 0 || req2 == nil {
		t.Fatal("empty re-decode")
	}
	if bytes.Contains(out, []byte("functionCall")) {
		t.Errorf("unpaired call kept:\n%s", out)
	}
}
