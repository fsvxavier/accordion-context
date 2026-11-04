package launcher

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// dig returns the value at the given object path, or nil.
func dig(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[p]
	}
	return cur
}

func TestMergeTemplateJSONDeepMergeTemplateWins(t *testing.T) {
	base := []byte(`{"keep":true,"provider":{"other":{"k":1},"anthropic":{"options":{"baseURL":"https://old","timeout":9}}}}`)
	out, err := MergeTemplate("json", base, `{"provider":{"anthropic":{"options":{"baseURL":"http://proxy"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeDoc("json", out)
	if err != nil {
		t.Fatal(err)
	}
	if doc["keep"] != true {
		t.Fatalf("keep dropped: %s", out)
	}
	if got := dig(doc, "provider", "other", "k"); got != json.Number("1") {
		t.Fatalf("provider.other dropped: %s", out)
	}
	if got := dig(doc, "provider", "anthropic", "options", "baseURL"); got != "http://proxy" {
		t.Fatalf("baseURL = %v, want template value: %s", got, out)
	}
	if got := dig(doc, "provider", "anthropic", "options", "timeout"); got != json.Number("9") {
		t.Fatalf("timeout dropped: %s", out)
	}
}

func TestMergeTemplateEmptyBaseIsTemplate(t *testing.T) {
	out, err := MergeTemplate("json", nil, `{"a":{"b":"x"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"b": "x"`) {
		t.Fatalf("out = %s", out)
	}
}

func TestMergeTemplateRejectsInvalidBase(t *testing.T) {
	for _, tc := range []struct{ format, base string }{
		{"json", `{"a":`},
		{"json", `{} trailing`},
		{"json", `[1,2]`},
		{"yaml", "- a\n- b\n"},
		{"toml", "a = [\n"},
	} {
		if _, err := MergeTemplate(tc.format, []byte(tc.base), `{"x":1}`); err == nil {
			t.Fatalf("%s %q: no error", tc.format, tc.base)
		}
	}
}

func TestMergeTemplateYAMLAndTOML(t *testing.T) {
	out, err := MergeTemplate("yaml", []byte("theme: dark\nprovider:\n  url: old\n"), "provider:\n  url: http://proxy\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "theme: dark") || !strings.Contains(string(out), "url: http://proxy") {
		t.Fatalf("yaml out = %s", out)
	}
	out, err = MergeTemplate("toml", []byte("theme = \"dark\"\n[provider]\nurl = \"old\"\n"), "[provider]\nurl = \"http://proxy\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "theme") || !strings.Contains(string(out), `url = 'http://proxy'`) && !strings.Contains(string(out), `url = "http://proxy"`) {
		t.Fatalf("toml out = %s", out)
	}
}

func TestMergeTemplateEnvAndINI(t *testing.T) {
	out, err := MergeTemplate("env", []byte("KEEP=1\nBASE=old\n"), "BASE=http://proxy\nNEW=2\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "KEEP=1\nBASE=http://proxy\nNEW=2\n"; string(out) != want {
		t.Fatalf("env out = %q, want %q", out, want)
	}
	out, err = MergeTemplate("ini", []byte("[core]\nname = mine\nurl = old\n"), "[core]\nurl = http://proxy\n")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("name = mine")) || !bytes.Contains(out, []byte("url = http://proxy")) || bytes.Contains(out, []byte("old")) {
		t.Fatalf("ini out = %q", out)
	}
}
