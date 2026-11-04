package launcher

import (
	"encoding/json"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func TestMergeJSONSetsNestedKeyAndKeepsOthers(t *testing.T) {
	src := []byte(`{"model": "m-1", "api": {"timeout": 30}}`)
	orig := append([]byte(nil), src...)
	out, err := MergeConfig("json", src, "api.base_url", "http://127.0.0.1:9/p")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not json: %v\n%s", err, out)
	}
	api := got["api"].(map[string]any)
	if got["model"] != "m-1" || api["base_url"] != "http://127.0.0.1:9/p" || api["timeout"] != float64(30) {
		t.Fatalf("merged = %v", got)
	}
	if string(src) != string(orig) {
		t.Fatal("MergeConfig modified its source")
	}
	if _, err := MergeConfig("json", []byte(`{"api": 1}`), "api.base_url", "x"); err == nil {
		t.Fatal("non-object parent accepted")
	}
	if out, err := MergeConfig("json", nil, "base_url", "x"); err != nil || !strings.Contains(string(out), `"base_url": "x"`) {
		t.Fatalf("empty source = %q, %v", out, err)
	}
}

func TestMergeYAML(t *testing.T) {
	out, err := MergeConfig("yaml", []byte("model: m\napi:\n  timeout: 5\n"), "api.base_url", "http://u")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	api := got["api"].(map[string]any)
	if got["model"] != "m" || api["base_url"] != "http://u" || api["timeout"] != 5 {
		t.Fatalf("merged = %v", got)
	}
}

func TestMergeTOMLSetsNestedKeyAndKeepsOthers(t *testing.T) {
	src := []byte("model = \"m-1\"\n# dropped on merge\n[api]\ntimeout = 30\n")
	orig := append([]byte(nil), src...)
	out, err := MergeConfig("toml", src, "api.base_url", "http://127.0.0.1:9/p")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := toml.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not toml: %v\n%s", err, out)
	}
	api := got["api"].(map[string]any)
	if got["model"] != "m-1" || api["base_url"] != "http://127.0.0.1:9/p" || api["timeout"] != int64(30) {
		t.Fatalf("merged = %v\n%s", got, out)
	}
	if string(src) != string(orig) {
		t.Fatal("MergeConfig modified its source")
	}
	if _, err := MergeConfig("toml", []byte("api = 1\n"), "api.base_url", "x"); err == nil {
		t.Fatal("non-table parent accepted")
	}
	if _, err := MergeConfig("toml", []byte("api = [\n"), "api.base_url", "x"); err == nil {
		t.Fatal("malformed toml accepted")
	}
	out, err = MergeConfig("toml", nil, "base_url", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal(out, &got); err != nil || got["base_url"] != "x" {
		t.Fatalf("empty source = %q, %v", out, err)
	}
}

func TestMergeEnvReplacesAndAppends(t *testing.T) {
	src := []byte("# keep me\nexport API_BASE=http://old\nOTHER=1\nAPI_BASE=http://older\n")
	out, err := MergeConfig("env", src, "API_BASE", "http://new")
	if err != nil {
		t.Fatal(err)
	}
	want := "# keep me\nAPI_BASE=http://new\nOTHER=1\nAPI_BASE=http://new\n"
	if string(out) != want {
		t.Fatalf("env merge = %q, want %q", out, want)
	}
	out, _ = MergeConfig("env", []byte("OTHER=1\n"), "API_BASE", "u")
	if string(out) != "OTHER=1\nAPI_BASE=u\n" {
		t.Fatalf("env append = %q", out)
	}
}

func TestMergeINI(t *testing.T) {
	src := []byte("; top\n[api]\n; comment\nbase_url = http://old\ntimeout = 3\n\n[ui]\ntheme = dark\n")
	out, err := MergeConfig("ini", src, "api.base_url", "http://new")
	if err != nil {
		t.Fatal(err)
	}
	want := "; top\n[api]\n; comment\nbase_url = http://new\ntimeout = 3\n\n[ui]\ntheme = dark\n"
	if string(out) != want {
		t.Fatalf("ini replace = %q", out)
	}
	// Missing key in an existing section goes right after its header.
	out, _ = MergeConfig("ini", []byte("[api]\ntimeout = 3\n"), "api.base_url", "u")
	if string(out) != "[api]\nbase_url = u\ntimeout = 3\n" {
		t.Fatalf("ini insert = %q", out)
	}
	// Missing section is appended; a global key goes first.
	out, _ = MergeConfig("ini", []byte("[ui]\ntheme = dark\n"), "api.base_url", "u")
	if string(out) != "[ui]\ntheme = dark\n\n[api]\nbase_url = u\n" {
		t.Fatalf("ini append section = %q", out)
	}
	out, _ = MergeConfig("ini", []byte("[ui]\n"), "base_url", "u")
	if string(out) != "base_url = u\n[ui]\n" {
		t.Fatalf("ini global = %q", out)
	}
}

func TestMergeConfigRejectsUnknownFormat(t *testing.T) {
	if _, err := MergeConfig("xml", []byte("<a/>"), "a", "b"); err == nil {
		t.Fatal("xml merge accepted")
	}
}
