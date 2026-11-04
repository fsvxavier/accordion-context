package openai

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// sseBody streams text, then a compress call whose arguments arrive in three
// deltas, a decompress call on tool index 1, a finish_reason, a usage chunk, and
// the [DONE] sentinel. A malformed line sits in the middle.
const sseBody = `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","content":"Ok"},"finish_reason":null}],"usage":null}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"compress","arguments":""}}]},"finish_reason":null}]}

data: {not json

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ranges\":[{\"startRef\":\"m00002\","}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"endRef\":\"m00005\",\"summary\":\"done\"}]}"}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"decompress","arguments":"{\"blockId\":\"b3\",\"mode\":\"peek\",\"query\":\"x\"}"}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_tokens_details":{"cached_tokens":64}}}

data: [DONE]

`

func TestStreamPassthroughCopiesAndDetectsCalls(t *testing.T) {
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(sseBody))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != sseBody {
		t.Fatalf("stream not copied byte for byte")
	}
	if !sum.Complete {
		t.Error("Complete = false")
	}
	if !sum.HasUsage || sum.Usage != (Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105, CachedTokens: 64}) {
		t.Errorf("usage = %+v has=%v", sum.Usage, sum.HasUsage)
	}
	if got := sum.Usage.UncachedPromptTokens(); got != 36 {
		t.Errorf("UncachedPromptTokens = %d, want 36", got)
	}
	if len(sum.Calls) != 2 {
		t.Fatalf("calls = %d, want 2: %+v", len(sum.Calls), sum.Calls)
	}
	c := sum.Calls[0]
	if c.ID != "call_a" || c.Name != "compress" || c.Err != nil || c.Compress == nil {
		t.Fatalf("compress call = %+v", c)
	}
	want := CompressArgs{Ranges: []CompressRange{{StartSeq: 2, EndSeq: 5, Summary: "done"}}}
	if !reflect.DeepEqual(*c.Compress, want) {
		t.Errorf("compress args = %+v, want %+v", *c.Compress, want)
	}
	d := sum.Calls[1]
	if d.Name != "decompress" || d.Index != 1 || d.Decompress == nil ||
		*d.Decompress != (DecompressArgs{BlockID: "b3", Mode: "peek", Query: "x"}) {
		t.Errorf("decompress call = %+v", d)
	}
}

func TestStreamPassthroughTruncatedAndBadArgs(t *testing.T) {
	// No [DONE] and no finish_reason: the stream ends in the middle of a call.
	src := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"compress","arguments":"{\"ranges\":3"}}]}}]}` + "\n"
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != src {
		t.Error("truncated stream not copied")
	}
	if sum.Complete {
		t.Error("Complete must be false without [DONE] or finish_reason")
	}
	if len(sum.Calls) != 1 || sum.Calls[0].Err == nil || sum.Calls[0].Compress != nil {
		t.Fatalf("calls = %+v", sum.Calls)
	}
}

func TestStreamPassthroughIgnoresOtherTools(t *testing.T) {
	src := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"grep","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"
	sum, err := StreamPassthrough(&bytes.Buffer{}, strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Calls) != 0 {
		t.Errorf("non-kernel tool reported: %+v", sum.Calls)
	}
	if !sum.Complete {
		t.Error("finish_reason must mark the stream complete")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestStreamPassthroughWriteError(t *testing.T) {
	if _, err := StreamPassthrough(failingWriter{}, strings.NewReader(sseBody)); err == nil {
		t.Error("expected the write error to be returned")
	}
}

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Usage
		ok   bool
	}{
		{"full", `{"id":"x","usage":{"prompt_tokens":120,"completion_tokens":9,"total_tokens":129,"prompt_tokens_details":{"cached_tokens":100}}}`,
			Usage{PromptTokens: 120, CompletionTokens: 9, TotalTokens: 129, CachedTokens: 100}, true},
		{"no details", `{"usage":{"prompt_tokens":7}}`, Usage{PromptTokens: 7}, true},
		{"null details", `{"usage":{"prompt_tokens":7,"prompt_tokens_details":null}}`, Usage{PromptTokens: 7}, true},
		{"absent", `{"id":"x"}`, Usage{}, false},
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
	if got := (Usage{PromptTokens: 2, CachedTokens: 5}).UncachedPromptTokens(); got != 0 {
		t.Errorf("UncachedPromptTokens must not be negative, got %d", got)
	}
}
