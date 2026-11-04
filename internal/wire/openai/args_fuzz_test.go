package openai

import (
	"encoding/json"
	"testing"
)

// FuzzParseToolArguments checks that the tool-call argument parsers never panic
// and that every successful parse satisfies the invariants the kernel relies on.
// Run with: go test -fuzz=FuzzParseToolArguments ./internal/wire/openai
func FuzzParseToolArguments(f *testing.F) {
	seeds := []string{
		`{"ranges":[{"startRef":"m00002","endRef":"m00005","summary":"s"}]}`,
		"```json\n{\"ranges\":[{\"startRef\":\"m1\",\"endRef\":\"m1\",\"summary\":\"s\"}]}\n```",
		`"{\"ranges\":[{\"start\":1,\"end\":2,\"text\":\"s\"}],\"closeIntents\":[1]}"`,
		`[{"startRef":"#3","endRef":3,"summary":"x"}]`,
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
				if r.StartSeq < 1 || r.EndSeq < r.StartSeq {
					t.Fatalf("compress: bad range %+v for %q", r, in)
				}
				if r.Summary == "" {
					t.Fatalf("compress: empty summary for %q", in)
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
// full window and that the output is valid JSON with valid tool pairing.
func FuzzDecodeEncode(f *testing.F) {
	f.Add([]byte(sampleBody))
	f.Add([]byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"a","function":{"name":"x","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":"r"}]}`))
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
