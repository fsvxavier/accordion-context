package responses

import "testing"

func TestParseCompressArgsRecoveryForms(t *testing.T) {
	const want = "Read the parser."
	cases := map[string]string{
		"plain":        `{"ranges":[{"startRef":"m00002","endRef":"m00005","summary":"Read the parser."}]}`,
		"fenced":       "```json\n{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"Read the parser.\"}]}\n```",
		"prose":        `Here you go: {"ranges":[{"startRef":"m00002","endRef":"m00005","summary":"Read the parser."}]} thanks`,
		"double":       `"{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"Read the parser.\"}]}"`,
		"bare array":   `[{"startRef":"m00002","endRef":"m00005","summary":"Read the parser."}]`,
		"alt names":    `{"ranges":[{"start":"2","end":5,"text":"Read the parser."}]}`,
		"numeric refs": `{"ranges":[{"startRef":2,"endRef":5,"summary":"Read the parser.","topic":"x"}]}`,
		"hash refs":    `{"ranges":[{"startRef":"#2","endRef":"m5","summary":"Read the parser."}]}`,
	}
	for name, in := range cases {
		args, err := ParseCompressArgs([]byte(in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		r := args.Ranges
		if len(r) != 1 || r[0].StartSeq != 2 || r[0].EndSeq != 5 || r[0].Summary != want {
			t.Errorf("%s: ranges = %+v", name, r)
		}
	}
}

func TestParseCompressArgsCloseIntents(t *testing.T) {
	args, err := ParseCompressArgs([]byte(`{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s","topic":"t"}],"close_intents":[7,"8"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(args.CloseIntents) != 2 || args.CloseIntents[0] != 7 || args.CloseIntents[1] != 8 {
		t.Errorf("close intents = %v", args.CloseIntents)
	}
	if args.Ranges[0].Topic != "t" {
		t.Errorf("topic = %q", args.Ranges[0].Topic)
	}
}

func TestParseCompressArgsRejects(t *testing.T) {
	cases := map[string]string{
		"empty":           ``,
		"no json":         `hello`,
		"no ranges":       `{"ranges":[]}`,
		"end before":      `{"ranges":[{"startRef":"m5","endRef":"m4","summary":"s"}]}`,
		"empty summary":   `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"   "}]}`,
		"zero ref":        `{"ranges":[{"startRef":"m0","endRef":"m2","summary":"s"}]}`,
		"bad ref":         `{"ranges":[{"startRef":"mx","endRef":"m2","summary":"s"}]}`,
		"ref overflow":    `{"ranges":[{"startRef":"m99999999999999999999","endRef":"m2","summary":"s"}]}`,
		"close not array": `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}],"closeIntents":3}`,
		"summary number":  `{"ranges":[{"startRef":"m1","endRef":"m2","summary":3}]}`,
	}
	for name, in := range cases {
		if _, err := ParseCompressArgs([]byte(in)); err == nil {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestParseDecompressArgs(t *testing.T) {
	args, err := ParseDecompressArgs([]byte(`{"blockId":"b12","mode":"PEEK","query":"q"}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.BlockID != "b12" || args.Mode != "peek" || args.Query != "q" {
		t.Errorf("got %+v", args)
	}
	args, err = ParseDecompressArgs([]byte(`{"id":" b1 "}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.BlockID != "b1" || args.Mode != "context" {
		t.Errorf("defaults: got %+v", args)
	}
	for name, in := range map[string]string{
		"missing id": `{"mode":"file"}`,
		"bad mode":   `{"blockId":"b1","mode":"all"}`,
		"not object": `[1]`,
		"empty":      ``,
	} {
		if _, err := ParseDecompressArgs([]byte(in)); err == nil {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestParseRef(t *testing.T) {
	ok := map[string]int64{"m00042": 42, "m42": 42, "M7": 7, "#3": 3, "9": 9}
	for in, want := range ok {
		if got, err := parseRef(in); err != nil || got != want {
			t.Errorf("parseRef(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "m", "0", "-1", "m1.5", "x9"} {
		if _, err := parseRef(in); err == nil {
			t.Errorf("parseRef(%q) accepted", in)
		}
	}
}
