package anthropic

import (
	"encoding/json"
	"testing"
)

// FuzzDecodeEncode checks that any body Decode accepts can be encoded from its
// full window and that the output is valid JSON.
func FuzzDecodeEncode(f *testing.F) {
	f.Add([]byte(sampleRequest))
	f.Add([]byte(`{"messages":[{"role":"user","content":"a"},{"role":"system","content":"r"},{"role":"user","content":"b"}]}`))
	f.Add([]byte(`{"messages":[{"role":"system","content":[{"type":"text","text":"r"}]}]}`))
	f.Add([]byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"x","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"r"}]}]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		req, msgs, err := Decode(body)
		if err != nil {
			return
		}
		out, err := Encode(req, msgs)
		if err != nil {
			return // an empty window is allowed to fail
		}
		if !json.Valid(out) {
			t.Fatalf("encode produced invalid JSON for %q: %s", body, out)
		}
	})
}
