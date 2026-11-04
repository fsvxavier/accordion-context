package anthropic

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Usage
		ok   bool
	}{
		{"top level", `{"id":"m","usage":{"input_tokens":120,"cache_read_input_tokens":9000,"cache_creation_input_tokens":300,"output_tokens":55}}`,
			Usage{InputTokens: 120, CacheReadInputTokens: 9000, CacheCreationInputTokens: 300, OutputTokens: 55}, true},
		{"message_start shape", `{"type":"message_start","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":0,"output_tokens":1}}}`,
			Usage{InputTokens: 7, OutputTokens: 1}, true},
		{"absent usage", `{"id":"m"}`, Usage{}, false},
		{"null fields", `{"usage":{"input_tokens":null,"cache_read_input_tokens":4}}`, Usage{CacheReadInputTokens: 4}, true},
	}
	for _, c := range cases {
		got, ok, err := ParseUsage([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v ok=%v, want %+v ok=%v", c.name, got, ok, c.want, c.ok)
		}
	}
	if _, _, err := ParseUsage([]byte(`{"usage":`)); err == nil {
		t.Error("expected error for malformed JSON")
	}
	u := Usage{InputTokens: 1, CacheReadInputTokens: 2, CacheCreationInputTokens: 3}
	if u.TotalInput() != 6 {
		t.Errorf("TotalInput = %d, want 6", u.TotalInput())
	}
}

const sseSample = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":25,"cache_read_input_tokens":1000,"output_tokens":1}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_c","name":"compress","input":{}}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"ranges\":[{\"startRef\":\"m1\","}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"endRef\":\"m4\",\"summary\":\"done\"}]}"}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":1}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_d","name":"decompress","input":{}}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"blockId\":\"b12\",\"mode\":\"peek\"}"}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":2}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_e","name":"grep","input":{}}}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

func TestStreamPassthrough(t *testing.T) {
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(sseSample))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != sseSample {
		t.Fatal("stream was not copied byte for byte")
	}
	wantUsage := Usage{InputTokens: 25, CacheReadInputTokens: 1000, OutputTokens: 42}
	if sum.Usage != wantUsage {
		t.Errorf("usage = %+v, want %+v", sum.Usage, wantUsage)
	}
	if !sum.Complete {
		t.Error("message_stop not seen")
	}
	if len(sum.Calls) != 2 {
		t.Fatalf("calls = %+v; want compress and decompress only", sum.Calls)
	}
	c := sum.Calls[0]
	if c.Name != ToolCompress || c.ID != "toolu_c" || c.Err != nil || c.Compress == nil {
		t.Fatalf("compress call wrong: %+v", c)
	}
	wantArgs := CompressArgs{Ranges: []CompressRange{{StartSeq: 1, EndSeq: 4, Summary: "done"}}}
	if !reflect.DeepEqual(*c.Compress, wantArgs) {
		t.Errorf("compress args = %+v, want %+v", *c.Compress, wantArgs)
	}
	d := sum.Calls[1]
	if d.Name != ToolDecompress || d.Compress != nil || d.Input != `{"blockId":"b12","mode":"peek"}` {
		t.Errorf("decompress call wrong: %+v", d)
	}
}

func TestStreamPassthroughBadCompressArgs(t *testing.T) {
	stream := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"x\",\"name\":\"compress\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"ranges\\\":\"}}\n" +
		"data: not json at all\n" +
		"data: {\"type\":\"content_block_stop\"\n" // unterminated: stream ends with the block open
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != stream {
		t.Fatal("malformed stream must still be copied verbatim")
	}
	if len(sum.Calls) != 1 || sum.Calls[0].Err == nil || sum.Calls[0].Compress != nil {
		t.Fatalf("expected one compress call with an error, got %+v", sum.Calls)
	}
	if sum.Complete {
		t.Error("Complete must be false without message_stop")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestStreamPassthroughWriteError(t *testing.T) {
	if _, err := StreamPassthrough(failingWriter{}, strings.NewReader(sseSample)); err == nil {
		t.Fatal("expected write error to propagate")
	}
}

func TestStreamPassthroughNoTrailingNewline(t *testing.T) {
	in := `data: {"type":"message_stop"}`
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != in || !sum.Complete {
		t.Fatalf("got %q complete=%v", dst.String(), sum.Complete)
	}
}
