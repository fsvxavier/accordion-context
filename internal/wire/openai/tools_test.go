package openai

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestInjectToolsAddsBothOnce(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"x":1}`)
	out, err := InjectTools(body)
	if err != nil {
		t.Fatal(err)
	}
	if !HasTool(out, ToolCompress) || !HasTool(out, ToolDecompress) || !HasTool(out, "lookup") {
		t.Fatalf("tools missing after inject: %s", out)
	}
	if !strings.Contains(string(out), `{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}`) {
		t.Errorf("existing tool not byte-identical: %s", out)
	}
	if !strings.Contains(string(out), `"model":"gpt-4o"`) || !strings.Contains(string(out), `"x":1`) {
		t.Errorf("other fields lost: %s", out)
	}
	again, err := InjectTools(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, out) {
		t.Errorf("InjectTools is not idempotent:\n%s\n%s", out, again)
	}
}

func TestInjectToolsWithoutToolsField(t *testing.T) {
	out, err := InjectTools([]byte(`{"model":"m","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !HasTool(out, ToolCompress) || !HasTool(out, ToolDecompress) {
		t.Fatalf("tools not added: %s", out)
	}
	if strings.Count(string(out), `"name":"compress"`) != 1 {
		t.Errorf("compress declared more than once: %s", out)
	}
}

func TestInjectToolsSkipsWhenPresent(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","function":{"name":"compress"}},{"type":"function","function":{"name":"decompress"}}]}`)
	out, err := InjectTools(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, body) {
		t.Errorf("body changed although both tools exist: %s", out)
	}
}

func TestInjectToolsErrors(t *testing.T) {
	if _, err := InjectTools([]byte(`[]`)); err == nil {
		t.Error("expected error for non-object body")
	}
	if _, err := InjectTools([]byte(`{"tools":"x"}`)); err == nil {
		t.Error("expected error for non-array tools")
	}
	if HasTool([]byte(`{bad`), ToolCompress) {
		t.Error("malformed body must report false")
	}
}

func TestToolDefinitionsAreFunctionTools(t *testing.T) {
	for _, def := range ToolDefinitions() {
		if !strings.Contains(string(def), `"type":"function"`) {
			t.Errorf("not a function tool: %s", def)
		}
	}
}

func TestParseCompressArgs(t *testing.T) {
	ok := []struct {
		name string
		in   string
		want CompressArgs
	}{
		{"plain", `{"ranges":[{"startRef":"m00002","endRef":"m00005","summary":"did it","topic":"t"}],"closeIntents":[3]}`,
			CompressArgs{Ranges: []CompressRange{{2, 5, "did it", "t"}}, CloseIntents: []int64{3}}},
		{"fenced", "```json\n{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"s\"}]}\n```",
			CompressArgs{Ranges: []CompressRange{{2, 5, "s", ""}}}},
		{"double encoded", `"{\"ranges\":[{\"startRef\":\"m00002\",\"endRef\":\"m00005\",\"summary\":\"s\"}]}"`,
			CompressArgs{Ranges: []CompressRange{{2, 5, "s", ""}}}},
		{"prose around", `Here you go: {"ranges":[{"startRef":"m7","endRef":"m9","summary":"s"}]} thanks`,
			CompressArgs{Ranges: []CompressRange{{7, 9, "s", ""}}}},
		{"alternate names and numeric refs", `{"ranges":[{"start":12,"end":"#14","text":"s","title":"T"}],"close_intents":["4"]}`,
			CompressArgs{Ranges: []CompressRange{{12, 14, "s", "T"}}, CloseIntents: []int64{4}}},
		{"bare array", `[{"startRef":"m00001","endRef":"m00001","summary":"x"}]`,
			CompressArgs{Ranges: []CompressRange{{1, 1, "x", ""}}}},
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
		"no json":          `nothing here`,
		"no ranges":        `{"ranges":[]}`,
		"end before start": `{"ranges":[{"startRef":"m5","endRef":"m4","summary":"s"}]}`,
		"empty summary":    `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"  "}]}`,
		"zero ref":         `{"ranges":[{"startRef":"m0","endRef":"m2","summary":"s"}]}`,
		"non numeric":      `{"ranges":[{"startRef":"mx","endRef":"m2","summary":"s"}]}`,
		"bad intents":      `{"ranges":[{"startRef":"m1","endRef":"m2","summary":"s"}],"closeIntents":"x"}`,
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
