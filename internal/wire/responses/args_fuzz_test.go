package responses

import "testing"

// FuzzParseToolArguments checks that the tool-call argument parsers never panic
// and that every successful parse satisfies the invariants the kernel relies on.
// Run with: go test -fuzz=FuzzParseToolArguments ./internal/wire/responses
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
