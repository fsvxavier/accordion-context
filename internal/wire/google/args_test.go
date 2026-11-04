package google

import (
	"reflect"
	"testing"
)

func TestParseCompressArgs(t *testing.T) {
	ok := []struct {
		name string
		in   string
		want CompressArgs
	}{
		{"object", `{"ranges":[{"startRef":"m00002","endRef":"m00005","summary":"did it","topic":"t"}],"closeIntents":[3]}`,
			CompressArgs{Ranges: []CompressRange{{2, 5, "did it", "t"}}, CloseIntents: []int64{3}}},
		{"double encoded", `"{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"s\"}]}"`,
			CompressArgs{Ranges: []CompressRange{{2, 5, "s", ""}}}},
		{"alternate names and numeric refs", `{"ranges":[{"start":12,"end":"#14","text":"s","title":"T"}],"close_intents":["4"]}`,
			CompressArgs{Ranges: []CompressRange{{12, 14, "s", "T"}}, CloseIntents: []int64{4}}},
	}
	for _, c := range ok {
		got, err := ParseCompressArgs([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
	bad := map[string]string{
		"empty":            ``,
		"array":            `[{"startRef":"m1","endRef":"m1","summary":"s"}]`,
		"no ranges":        `{"ranges":[]}`,
		"end before start": `{"ranges":[{"startRef":"m5","endRef":"m4","summary":"s"}]}`,
		"empty summary":    `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"  "}]}`,
		"zero ref":         `{"ranges":[{"startRef":"m0","endRef":"m2","summary":"s"}]}`,
		"non numeric":      `{"ranges":[{"startRef":"mx","endRef":"m2","summary":"s"}]}`,
		"bad intents":      `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}],"closeIntents":"x"}`,
		"trailing":         `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}]} x`,
	}
	for name, in := range bad {
		if _, err := ParseCompressArgs([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseDecompressArgs(t *testing.T) {
	got, err := ParseDecompressArgs([]byte(`{"blockId":"b3","mode":"PEEK","query":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := (DecompressArgs{BlockID: "b3", Mode: "peek", Query: "x"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	got, err = ParseDecompressArgs([]byte(`{"blockId":"b9"}`))
	if err != nil || got.Mode != "context" {
		t.Errorf("default mode: %+v, %v", got, err)
	}
	for name, in := range map[string]string{
		"missing id": `{"mode":"file"}`,
		"bad mode":   `{"blockId":"b1","mode":"all"}`,
		"not object": `[1]`,
		"id type":    `{"blockId":7}`,
	} {
		if _, err := ParseDecompressArgs([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseRef(t *testing.T) {
	for in, want := range map[string]int64{"m00042": 42, "#7": 7, "12": 12} {
		if got, err := ParseRef(in); err != nil || got != want {
			t.Errorf("ParseRef(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "m0", "x1", "-3"} {
		if _, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q): expected error", in)
		}
	}
}
