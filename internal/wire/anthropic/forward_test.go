package anthropic

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSSEReaderSplitsEvents(t *testing.T) {
	in := "\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\n" +
		"event: content_block_delta\ndata: {\"a\":\ndata: 1}\n\n" +
		"data: last without blank line"
	r := NewSSEReader(strings.NewReader(in))
	ev, err := r.Next()
	if err != nil || ev.Name != "ping" || string(ev.Data) != `{"type":"ping"}` {
		t.Fatalf("first event = %+v, %v", ev, err)
	}
	ev, err = r.Next()
	if err != nil || ev.Name != "content_block_delta" || string(ev.Data) != "{\"a\":\n1}" {
		t.Fatalf("multi-line data = %q, %v", ev.Data, err)
	}
	if !bytes.HasSuffix(ev.Raw, []byte("\n\n")) {
		t.Fatalf("raw must keep the blank terminator: %q", ev.Raw)
	}
	ev, err = r.Next()
	if err != nil || string(ev.Data) != "last without blank line" {
		t.Fatalf("unterminated event = %q, %v", ev.Data, err)
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestForwarderHoldsMessageEndAndRewritesStop(t *testing.T) {
	var out bytes.Buffer
	f := NewForwarder(&out)
	f.BeginRound()
	feed := func(name, data string) {
		t.Helper()
		if err := f.Feed(SSEEvent{Name: name, Data: []byte(data), Raw: []byte("event: " + name + "\ndata: " + data + "\n\n")}); err != nil {
			t.Fatal(err)
		}
	}
	feed("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":3}}}`)
	feed("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"compress","input":{}}}`)
	feed("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"ranges\":[]}"}}`)
	feed("content_block_stop", `{"type":"content_block_stop","index":0}`)
	feed("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`)
	feed("message_stop", `{"type":"message_stop"}`)
	if strings.Contains(out.String(), "compress") || strings.Contains(out.String(), "message_delta") {
		t.Fatalf("internal block or held end written early: %s", out.String())
	}
	res := f.EndRound()
	if len(res.Internal) != 1 || res.Internal[0].Name != "compress" || string(res.Internal[0].Input) != `{"ranges":[]}` {
		t.Fatalf("internal calls = %+v", res.Internal)
	}
	if res.StopReason != "tool_use" || res.Usage.InputTokens != 3 || res.Usage.OutputTokens != 4 {
		t.Fatalf("round = %+v", res)
	}
	if err := f.Finish(true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `"stop_reason":"end_turn"`) || strings.Contains(got, `"tool_use"`) {
		t.Fatalf("stop reason not rewritten: %s", got)
	}
	if !strings.Contains(got, "event: message_stop") {
		t.Fatalf("message_stop missing: %s", got)
	}
}
