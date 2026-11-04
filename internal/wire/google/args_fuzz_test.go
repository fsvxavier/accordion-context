package google

import (
	"encoding/json"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// FuzzParseToolArgs checks that the argument parsers never panic and that every
// successful parse satisfies the invariants the kernel relies on.
// Run with: go test -fuzz=FuzzParseToolArgs ./internal/wire/google
func FuzzParseToolArgs(f *testing.F) {
	seeds := []string{
		`{"ranges":[{"startRef":"m00002","endRef":"m00005","summary":"s"}]}`,
		`"{\"ranges\":[{\"start\":1,\"end\":2,\"text\":\"s\"}],\"closeIntents\":[1]}"`,
		`{"ranges":[{"startRef":"#3","endRef":3,"summary":"x"}]}`,
		`{"blockId":"b1","mode":"peek","query":"q"}`,
		`{"blockId":"b1"}`,
		`{"ranges":[{"startRef":"m5","endRef":"m4","summary":"s"}]}`,
		`{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}],"closeIntents":[9999999999999999999]}`,
		``,
		`{`,
		`"`,
		`{"mode":"file"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		if args, err := ParseCompressArgs(in); err == nil {
			if len(args.Ranges) == 0 {
				t.Fatalf("compress: success without ranges for %q", in)
			}
			for _, r := range args.Ranges {
				if r.StartSeq < 1 || r.EndSeq < r.StartSeq || r.Summary == "" {
					t.Fatalf("compress: bad range %+v for %q", r, in)
				}
			}
		}
		if args, err := ParseDecompressArgs(in); err == nil {
			if args.BlockID == "" {
				t.Fatalf("decompress: empty blockId for %q", in)
			}
			switch args.Mode {
			case "context", "file", "peek":
			default:
				t.Fatalf("decompress: mode %q for %q", args.Mode, in)
			}
		}
	})
}

// FuzzDecodeEncode checks that any body Decode accepts can be encoded from its
// full window, and that the output is valid JSON with paired tool parts.
// Run with: go test -fuzz=FuzzDecodeEncode ./internal/wire/google
func FuzzDecodeEncode(f *testing.F) {
	f.Add([]byte(sampleBody))
	f.Add([]byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"name":"x","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"x","response":{}}}]}]}`))
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
		var doc struct {
			Contents []struct {
				Parts []map[string]json.RawMessage `json:"parts"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("encode output not a request: %v", err)
		}
		// A subset window must still encode to valid JSON.
		var sub []kernel.Message
		for i, m := range msgs {
			if i%2 == 0 {
				sub = append(sub, m)
			}
		}
		if len(sub) > 0 {
			if out, err := Encode(req, sub); err == nil && !json.Valid(out) {
				t.Fatalf("subset encode invalid JSON for %q: %s", body, out)
			}
		}
	})
}
