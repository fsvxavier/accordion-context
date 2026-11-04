package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// opaqueInput is an input that mixes modelled items with item types the codec does
// not model. The opaque items include a call_id that matches a real call, so a
// codec that paired them as tool results would be caught.
const opaqueInput = `{"model":"gpt-x","instructions":"sys","input":[
 {"role":"user","content":"first"},
 {"type":"compaction","encrypted_content":"AAAA=="},
 {"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{}"},
 {"type":"local_shell_call_output","call_id":"call_1","output":{"stdout":"x","summary":3}},
 {"type":"function_call_output","call_id":"call_1","output":"ok"},
 {"type":"web_search_call","id":"ws_1","status":"completed"},
 {"role":"user","content":"second"},
 {"type":"compaction_trigger"}
]}`

// rawItems returns the elements of the input array of a request body, byte for byte.
func rawItems(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var v struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("encoded body is not valid JSON: %v\n%s", err, body)
	}
	return v.Input
}

func TestDecodeUnknownItemsAreOpaque(t *testing.T) {
	_, msgs, err := Decode([]byte(opaqueInput))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	var kinds []kernel.Kind
	for _, m := range msgs {
		kinds = append(kinds, m.Kind)
		if m.Kind == kernel.KindOpaque {
			if m.ToolCallID != "" || m.ToolName != "" {
				t.Errorf("opaque %s carries a call identity: %+v", m.Ref, m)
			}
			if !strings.HasPrefix(m.Text, "opaque ") || !strings.Contains(m.Text, "sha256:") {
				t.Errorf("opaque text is not a fingerprint: %q", m.Text)
			}
			if m.Tokens < 1 {
				t.Errorf("opaque %s has no size estimate", m.Ref)
			}
		}
	}
	want := []kernel.Kind{
		kernel.KindText,       // first
		kernel.KindOpaque,     // compaction
		kernel.KindToolCall,   // function_call
		kernel.KindOpaque,     // local_shell_call_output
		kernel.KindToolResult, // function_call_output
		kernel.KindOpaque,     // web_search_call
		kernel.KindText,       // second
		kernel.KindOpaque,     // compaction_trigger
	}
	// The instructions message comes first, so the kinds are preceded by one text.
	if len(kinds) != len(want)+1 || kinds[0] != kernel.KindText {
		t.Fatalf("kinds = %v", kinds)
	}
	for i, k := range want {
		if kinds[i+1] != k {
			t.Fatalf("kind %d = %s, want %s (all %v)", i, kinds[i+1], k, kinds)
		}
	}
}

func TestEncodeFullWindowKeepsOpaqueItemsByteForByte(t *testing.T) {
	req, msgs, err := Decode([]byte(opaqueInput))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(req, msgs)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	in := rawItems(t, []byte(opaqueInput))
	got := rawItems(t, out)
	if len(got) != len(in) {
		t.Fatalf("items = %d, want %d:\n%s", len(got), len(in), out)
	}
	for i, it := range in {
		if !isOpaqueJSON(string(it)) {
			continue // modelled items may get their reference label
		}
		if !bytes.Equal(got[i], it) {
			t.Errorf("item %d changed:\n got %s\nwant %s", i, got[i], it)
		}
	}
}

func TestEncodeRefusesOpaqueItemNotInRequest(t *testing.T) {
	req, msgs, err := Decode([]byte(opaqueInput))
	if err != nil {
		t.Fatal(err)
	}
	for i := range msgs {
		if msgs[i].Kind == kernel.KindOpaque {
			msgs[i].Text = "opaque compaction sha256:00000000000000000000000000000000"
			break
		}
	}
	if _, err := Encode(req, msgs); err == nil {
		t.Fatal("Encode sent a window with an opaque item the request does not have")
	}
}

// Without the tool pairs, the opaque items keep their order and their places among
// the messages that remain.
func TestEncodeOpaqueStaysBetweenItsNeighbours(t *testing.T) {
	req, msgs, err := Decode([]byte(opaqueInput))
	if err != nil {
		t.Fatal(err)
	}
	var window []kernel.Message
	for _, m := range msgs {
		if m.Role == kernel.RoleSystem || m.Kind == kernel.KindToolCall || m.Kind == kernel.KindToolResult {
			continue // a window with no tool pairs, as a compacted one may be
		}
		window = append(window, m)
	}
	out, err := Encode(req, window)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, it := range rawItems(t, out) {
		var v struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(it, &v)
		types = append(types, v.Type)
	}
	want := []string{"", "compaction", "local_shell_call_output", "web_search_call", "", "compaction_trigger"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("item types = %q, want %q", types, want)
	}
}

// FuzzOpaqueRoundTrip builds requests from a script of item kinds, mixing modelled
// and opaque items. Decode must accept them, and a full-window Encode must return
// every opaque item's bytes, in order, with no item added or removed among them.
func FuzzOpaqueRoundTrip(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Add([]byte{2, 2, 2})
	f.Add([]byte{4, 0, 4, 1, 3})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, script []byte) {
		if len(script) > 64 {
			script = script[:64]
		}
		var items []string
		for i, b := range script {
			n := int(b) % 7
			switch n {
			case 0:
				items = append(items, fmt.Sprintf(`{"role":"user","content":"u%d"}`, i))
			case 1:
				items = append(items, fmt.Sprintf(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a%d"}]}`, i))
			case 2:
				items = append(items, fmt.Sprintf(`{"type":"compaction","encrypted_content":"enc%d=="}`, i))
			case 3:
				items = append(items, `{"type":"compaction_trigger"}`)
			case 4:
				// An unknown shape under a known name: the codec must not read it.
				items = append(items, fmt.Sprintf(`{"type":"local_shell_call_output","call_id":"c%d","output":{"summary":%d}}`, i, i))
			case 5:
				items = append(items, fmt.Sprintf(`{"type":"web_search_call","id":"ws%d","status":"completed"}`, i))
			case 6:
				items = append(items, fmt.Sprintf(`{"type":"reasoning","summary":[{"type":"summary_text","text":"r%d"}],"encrypted_content":"x"}`, i))
			}
		}
		if len(items) == 0 {
			return
		}
		body := `{"model":"m","input":[` + strings.Join(items, ",") + `]}`
		req, msgs, err := Decode([]byte(body))
		if err != nil {
			t.Fatalf("Decode: %v\n%s", err, body)
		}
		out, err := Encode(req, msgs)
		if err != nil {
			// The only legal refusal is an input with nothing sendable; a window of
			// reasoning items alone is dropped, as before.
			return
		}
		var wantOpaque, gotOpaque []string
		for _, it := range items {
			if isOpaqueJSON(it) {
				wantOpaque = append(wantOpaque, it)
			}
		}
		for _, it := range rawItems(t, out) {
			if isOpaqueJSON(string(it)) {
				gotOpaque = append(gotOpaque, string(it))
			}
		}
		if strings.Join(wantOpaque, "\n") != strings.Join(gotOpaque, "\n") {
			t.Fatalf("opaque items changed\n got %v\nwant %v", gotOpaque, wantOpaque)
		}
	})
}

// isOpaqueJSON reports whether an item is one of the types the fuzz target treats
// as opaque, by its type field alone.
func isOpaqueJSON(item string) bool {
	var v struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(item), &v) != nil {
		return false
	}
	switch v.Type {
	case "compaction", "compaction_trigger", "local_shell_call_output", "web_search_call":
		return true
	}
	return false
}
