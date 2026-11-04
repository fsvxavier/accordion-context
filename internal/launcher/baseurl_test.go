package launcher

import (
	"reflect"
	"strings"
	"testing"
)

func TestArgvWithFlagPosition(t *testing.T) {
	argv := []string{"client", "chat", "--fast"}
	flags := []string{"--api-base", "http://127.0.0.1:1/x"}

	before := ArgvWith(argv, flags, false)
	wantBefore := []string{"client", "--api-base", "http://127.0.0.1:1/x", "chat", "--fast"}
	if !reflect.DeepEqual(before, wantBefore) {
		t.Fatalf("before = %q, want %q", before, wantBefore)
	}
	after := ArgvWith(argv, flags, true)
	wantAfter := []string{"client", "chat", "--fast", "--api-base", "http://127.0.0.1:1/x"}
	if !reflect.DeepEqual(after, wantAfter) {
		t.Fatalf("after = %q, want %q", after, wantAfter)
	}
	if !reflect.DeepEqual(argv, []string{"client", "chat", "--fast"}) {
		t.Fatalf("input argv modified: %q", argv)
	}
	if got := ArgvWith([]string{"client"}, nil, false); !reflect.DeepEqual(got, []string{"client"}) {
		t.Fatalf("no flags = %q", got)
	}
	if ArgvWith(nil, flags, false) != nil {
		t.Fatal("empty argv should yield nil")
	}
}

func TestProfileFlagPositionDefaultsToBefore(t *testing.T) {
	if (Profile{}).FlagsAfter() {
		t.Fatal("default flag position must be before")
	}
	if !(Profile{FlagPosition: "after"}).FlagsAfter() {
		t.Fatal("after not honored")
	}
}

func TestExpandFlagSplitsBeforeSubstitution(t *testing.T) {
	got, err := ExpandFlag("--api-base {url}", map[string]string{"url": "http://a b/c"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"--api-base", "http://a b/c"}) {
		t.Fatalf("tokens = %q: a value with a space must stay one argument", got)
	}
	got, err = ExpandFlag("--api-base={url}", map[string]string{"url": "http://x"})
	if err != nil || !reflect.DeepEqual(got, []string{"--api-base=http://x"}) {
		t.Fatalf("joined form = %q, %v", got, err)
	}
}

func TestRenderRejectsUnknownPlaceholder(t *testing.T) {
	if _, err := Render("{url}/{prot}", map[string]string{"url": "u"}); err == nil {
		t.Fatal("unknown placeholder accepted")
	}
	// JSON braces are not placeholders.
	got, err := Render(`{"base_url": "{url}", "x": {}}`, map[string]string{"url": "U"})
	if err != nil || got != `{"base_url": "U", "x": {}}` {
		t.Fatalf("render = %q, %v", got, err)
	}
}

func TestIgnoresBaseURLQuirk(t *testing.T) {
	if (Profile{}).IgnoresBaseURL() {
		t.Fatal("no quirks must not ignore the base URL")
	}
	if !(Profile{Quirks: map[string]any{"ignores_base_url": true}}).IgnoresBaseURL() {
		t.Fatal("ignores_base_url: true not honored")
	}
	if (Profile{Quirks: map[string]any{"ignores_base_url": "yes"}}).IgnoresBaseURL() {
		t.Fatal("non-bool quirk must not count")
	}
}

func TestRenderTempConfigAndPathFlag(t *testing.T) {
	tc := TempConfig{Template: `{"base_url": "{url}", "port": {port}}`}
	got, err := RenderTempConfig(tc, "http://127.0.0.1:9/p", "9", "tok")
	if err != nil || got != `{"base_url": "http://127.0.0.1:9/p", "port": 9}` {
		t.Fatalf("RenderTempConfig = %q, %v", got, err)
	}
	args, err := PathFlagArgs(TempConfig{PathFlag: "--config {path}"}, "/tmp/x.json")
	if err != nil || !reflect.DeepEqual(args, []string{"--config", "/tmp/x.json"}) {
		t.Fatalf("PathFlagArgs = %q, %v", args, err)
	}
	if ConfigExt("env") != ".env" || ConfigExt("json") != ".json" {
		t.Fatal("ConfigExt wrong")
	}
}

func TestValidateFlagAndTempConfigShapes(t *testing.T) {
	base := func() string {
		return "profiles:\n  - name: x\n    upstream: https://api.example.com/v1\n"
	}
	bad := map[string]string{
		"flag without url":         "    base_url_flag: --api-base\n",
		"flag unknown placeholder": "    base_url_flag: --api-base {ur}\n",
		"bad flag_position":        "    base_url_flag: --api-base {url}\n    flag_position: middle\n",
		"no path selector":         "    temp_config: {format: json, template: '{\"b\": \"{url}\"}'}\n",
		"two path selectors":       "    temp_config: {path_env: C, path_flag: '--c {path}', format: json, template: '{url}'}\n",
		"bad format":               "    temp_config: {path_env: C, format: xml, template: '{url}'}\n",
		"no content":               "    temp_config: {path_env: C, format: json}\n",
		"two contents":             "    temp_config: {path_env: C, format: json, template: '{url}', merge_from: /x.json, key: a}\n",
		"template without url":     "    temp_config: {path_env: C, format: json, template: 'hello'}\n",
		"merge without key":        "    temp_config: {path_env: C, format: json, merge_from: /x.json}\n",
		"key with template":        "    temp_config: {path_env: C, format: json, template: '{url}', key: a}\n",
		"bad env key":              "    temp_config: {path_env: C, format: env, merge_from: /x.env, key: 1BAD}\n",
		"empty key segment":        "    temp_config: {path_env: C, format: json, merge_from: /x.json, key: a..b}\n",
		"unknown prefer":           "    integration: {prefer: [magic]}\n",
	}
	for name, body := range bad {
		doc := base() + body
		if _, err := LoadProfiles(strings.NewReader(doc)); err == nil {
			t.Fatalf("%s: accepted:\n%s", name, doc)
		}
	}
	if _, err := LoadProfiles(strings.NewReader("name: x\ndetect:\n  version_regex: '('\n")); err == nil {
		t.Fatal("invalid detect.version_regex accepted")
	}
}
