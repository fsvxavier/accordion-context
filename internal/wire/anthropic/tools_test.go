package anthropic

import (
	"reflect"
	"testing"
)

func TestParseRef(t *testing.T) {
	ok := map[string]int64{"m00042": 42, "m42": 42, "42": 42, "#42": 42, " M7 ": 7, "1": 1}
	for in, want := range ok {
		got, err := ParseRef(in)
		if err != nil || got != want {
			t.Errorf("ParseRef(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "m", "0", "m0", "-3", "+3", "4.0", "abc", "m1x", "99999999999999999999"} {
		if _, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q): expected error", in)
		}
	}
}

const summaryText = "The user asked for X; the agent changed foo.go:42."

func TestParseCompressArgs(t *testing.T) {
	canonical := `{"ranges":[{"startRef":"m00340","endRef":"m00412","summary":"` + summaryText + `","topic":"matcher"}],"closeIntents":[7,9]}`
	full := CompressArgs{
		Ranges:       []CompressRange{{StartSeq: 340, EndSeq: 412, Summary: summaryText, Topic: "matcher"}},
		CloseIntents: []int64{7, 9},
	}
	noTopic := CompressArgs{
		Ranges:       []CompressRange{{StartSeq: 340, EndSeq: 412, Summary: summaryText}},
		CloseIntents: []int64{7, 9},
	}
	bareList := CompressArgs{
		Ranges: []CompressRange{{StartSeq: 340, EndSeq: 412, Summary: summaryText, Topic: "matcher"}},
	}
	cases := []struct {
		name string
		in   string
		want CompressArgs
	}{
		{"canonical", canonical, full},
		{"fenced json", "```json\n" + canonical + "\n```", full},
		{"fenced no tag", "```\n" + canonical + "\n```", full},
		{"prose around", "Here is the call: " + canonical + " thanks", full},
		{"double encoded", `"` + escapeForJSONString(canonical) + `"`, full},
		{"surrounding ws", "  \n\t" + canonical + "\n ", full},
		{"snake alt names", `{"ranges":[{"start_ref":"m00340","end_ref":"m00412","text":"` + summaryText + `","title":"matcher"}],"close_intents":[7,"#9"]}`, full},
		{"plain alt names", `{"range":[{"from":"340","to":412,"content":"` + summaryText + `","label":"matcher"}],"intents":["7","9"]}`, full},
		{"numeric refs", `{"ranges":[{"startRef":340,"endRef":412,"summary":"` + summaryText + `"}],"closeIntents":[7,9]}`, noTopic},
		{"bare ranges list", `[{"startRef":"m00340","endRef":"m00412","summary":"` + summaryText + `","topic":"matcher"}]`, bareList},
	}
	for _, c := range cases {
		got, err := ParseCompressArgs([]byte(c.in))
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestParseCompressArgsErrors(t *testing.T) {
	cases := map[string]string{
		"empty":           ``,
		"no json":         `please compress`,
		"no ranges":       `{"ranges":[]}`,
		"missing ranges":  `{"closeIntents":[1]}`,
		"missing endRef":  `{"ranges":[{"startRef":"m1","summary":"s"}]}`,
		"missing start":   `{"ranges":[{"endRef":"m1","summary":"s"}]}`,
		"end before":      `{"ranges":[{"startRef":"m9","endRef":"m3","summary":"s"}]}`,
		"empty summary":   `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"   "}]}`,
		"no summary":      `{"ranges":[{"startRef":"m1","endRef":"m2"}]}`,
		"bad ref":         `{"ranges":[{"startRef":"xyz","endRef":"m2","summary":"s"}]}`,
		"ref zero":        `{"ranges":[{"startRef":"m0","endRef":"m2","summary":"s"}]}`,
		"summary type":    `{"ranges":[{"startRef":"m1","endRef":"m2","summary":["s"]}]}`,
		"close not array": `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}],"closeIntents":"7"}`,
		"close bad id":    `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}],"closeIntents":[true]}`,
		"scalar":          `42`,
	}
	for name, in := range cases {
		if _, err := ParseCompressArgs([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// FuzzParseCompressArgs checks that arbitrary model output never panics and that
// every accepted result satisfies the invariants the session relies on.
func FuzzParseCompressArgs(f *testing.F) {
	seeds := []string{
		`{"ranges":[{"startRef":"m00340","endRef":"m00412","summary":"done"}],"closeIntents":[7]}`,
		"```json\n{\"ranges\":[{\"startRef\":1,\"endRef\":2,\"summary\":\"x\"}]}\n```",
		`"{\"ranges\":[{\"start\":\"m1\",\"end\":\"m1\",\"text\":\"t\"}]}"`,
		`[{"from":"#3","to":"#4","content":"c"}]`,
		`{"ranges":[{"startRef":"m99999999999999999999","endRef":"m1","summary":"s"}]}`,
		`{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}],"closeIntents":[1e3]}`,
		`{}`, ``, `[`, `"`, "```", `{"ranges":null}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		args, err := ParseCompressArgs(in)
		if err != nil {
			return
		}
		if len(args.Ranges) == 0 {
			t.Fatal("accepted args without ranges")
		}
		for _, r := range args.Ranges {
			if r.StartSeq < 1 || r.EndSeq < r.StartSeq {
				t.Fatalf("invalid range bounds %+v", r)
			}
			if r.Summary == "" {
				t.Fatal("accepted empty summary")
			}
		}
		for _, id := range args.CloseIntents {
			if id < 1 {
				t.Fatalf("invalid intent id %d", id)
			}
		}
	})
}

// escapeForJSONString turns a JSON document into the body of a JSON string literal.
func escapeForJSONString(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			out = append(out, '\\', c)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
