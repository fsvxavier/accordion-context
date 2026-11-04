package responses

import (
	"bytes"
	"strings"
	"testing"
)

// sseBody is a Responses stream: a message item, a compress call whose arguments
// arrive in deltas and are closed by a done event, a decompress call that only
// has deltas, an unrelated function call, a malformed data line, and the usage
// carried by response.completed.
const sseBody = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","usage":null}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"compress","arguments":"","status":"in_progress"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"ranges\":[{\"startRef\":\"m00002\","}

data: {not json

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"\"endRef\":\"m00005\",\"summary\":\"done\"}]}"}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1,"arguments":"{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"done\"}]}"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"compress","arguments":"{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"done\"}]}","status":"completed"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_2","call_id":"call_2","name":"decompress","arguments":""}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_2","output_index":2,"delta":"{\"blockId\":\"b3\","}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_2","output_index":2,"delta":"\"mode\":\"peek\",\"query\":\"x\"}"}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":3,"item":{"type":"function_call","id":"fc_3","call_id":"call_3","name":"read_file","arguments":""}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_3","output_index":3,"delta":"{\"path\":\"a.go\"}"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":100},"output_tokens":9,"total_tokens":129}}}

`

func TestStreamPassthroughCopiesAndDetectsCalls(t *testing.T) {
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(sseBody))
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != sseBody {
		t.Fatal("stream was not copied byte for byte")
	}
	if !sum.Complete {
		t.Error("response.completed not seen")
	}
	if !sum.HasUsage || sum.Usage.InputTokens != 120 || sum.Usage.CachedTokens != 100 || sum.Usage.OutputTokens != 9 {
		t.Errorf("usage = %+v (has %v)", sum.Usage, sum.HasUsage)
	}
	if len(sum.Calls) != 2 {
		t.Fatalf("got %d calls, want 2 (read_file must not be reported): %+v", len(sum.Calls), sum.Calls)
	}

	c := sum.Calls[0]
	if c.Name != ToolCompress || c.CallID != "call_1" || c.ItemID != "fc_1" || c.OutputIndex != 1 {
		t.Fatalf("compress call identity: %+v", c)
	}
	if c.Err != nil || c.Compress == nil {
		t.Fatalf("compress args: err=%v args=%+v", c.Err, c.Compress)
	}
	r := c.Compress.Ranges
	if len(r) != 1 || r[0].StartSeq != 2 || r[0].EndSeq != 5 || r[0].Summary != "done" {
		t.Errorf("compress ranges = %+v", r)
	}

	d := sum.Calls[1]
	if d.Name != ToolDecompress || d.Decompress == nil || d.Err != nil {
		t.Fatalf("decompress call: %+v", d)
	}
	if d.Decompress.BlockID != "b3" || d.Decompress.Mode != "peek" || d.Decompress.Query != "x" {
		t.Errorf("decompress args = %+v", d.Decompress)
	}
}

func TestStreamPassthroughReportsArgumentErrors(t *testing.T) {
	stream := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"c","name":"compress","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"ranges\":"}

data: {"type":"response.completed","response":{"usage":null}}

`
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Calls) != 1 || sum.Calls[0].Err == nil || sum.Calls[0].Compress != nil {
		t.Fatalf("truncated compress args not reported: %+v", sum.Calls)
	}
	if sum.HasUsage {
		t.Error("null usage reported as present")
	}
}

func TestStreamPassthroughWithoutTerminalEvent(t *testing.T) {
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(`data: {"type":"response.output_text.delta","delta":"hi"}`+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Complete {
		t.Error("stream without terminal event reported complete")
	}
	if dst.Len() == 0 {
		t.Error("final line without newline was not copied")
	}
}

func TestStreamPassthroughIncompleteIsTerminal(t *testing.T) {
	var dst bytes.Buffer
	sum, err := StreamPassthrough(&dst, strings.NewReader(`data: {"type":"response.incomplete","response":{"status":"incomplete"}}`+"\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Complete {
		t.Error("response.incomplete not treated as terminal")
	}
}
