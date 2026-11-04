package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuiltinProfilesMatchClientCommands(t *testing.T) {
	cases := map[string]string{
		"claude":               "ANTHROPIC_BASE_URL",
		"/usr/local/bin/Codex": "OPENAI_BASE_URL",
		"gemini.exe":           "GOOGLE_GEMINI_BASE_URL",
	}
	for cmd, wantEnv := range cases {
		p, ok := ProfileFor(cmd)
		if !ok {
			t.Fatalf("ProfileFor(%q) not found", cmd)
		}
		if p.BaseURLEnv != wantEnv {
			t.Fatalf("ProfileFor(%q).BaseURLEnv = %q, want %q", cmd, p.BaseURLEnv, wantEnv)
		}
	}
	oc, ok := ProfileFor("opencode")
	if !ok || oc.BaseURLEnv != "" {
		t.Fatalf("opencode profile = (%+v, %v), want no base URL variable", oc, ok)
	}
	if _, ok := ProfileFor("nosuchclient"); ok {
		t.Fatal("ProfileFor(unknown) found a profile")
	}
}

func TestBaseURLShape(t *testing.T) {
	p, _ := ProfileFor("claude")
	got := BaseURL("127.0.0.1:8787", "abc123", p)
	want := "http://127.0.0.1:8787/p/abc123/ctx/https://api.anthropic.com"
	if got != want {
		t.Fatalf("BaseURL = %q, want %q", got, want)
	}
}

func TestMatchProfilePrefersFirstMatch(t *testing.T) {
	user := []Profile{{Name: "mine", Match: []string{"claude"}, Upstream: "https://gw.example.com", BaseURLEnv: "MINE_URL"}}
	p, ok := MatchProfile(append(user, BuiltinProfiles()...), "claude")
	if !ok || p.Name != "mine" {
		t.Fatalf("MatchProfile = (%q, %v), want user profile", p.Name, ok)
	}
}

func TestLoadProfiles(t *testing.T) {
	doc := `
profiles:
  - name: my-client
    match: [my-client]
    upstream: https://api.example.com/v1
    base_url_env: MY_CLIENT_BASE_URL
    env:
      MY_CLIENT_MODE: proxy
`
	ps, err := LoadProfiles(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if len(ps) != 1 || ps[0].BaseURLEnv != "MY_CLIENT_BASE_URL" || ps[0].Env["MY_CLIENT_MODE"] != "proxy" {
		t.Fatalf("parsed = %+v", ps)
	}

	if ps, err := LoadProfiles(strings.NewReader("")); err != nil || len(ps) != 0 {
		t.Fatalf("empty doc = (%v, %v), want no profiles", ps, err)
	}

	bad := map[string]string{
		"unknown key":    "profiles:\n  - name: x\n    bogus: 1\n",
		"no name":        "profiles:\n  - upstream: https://a.example.com\n    base_url_env: X\n",
		"bad upstream":   "profiles:\n  - name: x\n    upstream: ftp://a\n    base_url_env: X\n",
		"bad env name":   "profiles:\n  - name: x\n    upstream: https://a.example.com\n    base_url_env: 1BAD\n",
		"bad static env": "profiles:\n  - name: x\n    env:\n      BAD-NAME: v\n",
	}
	for name, doc := range bad {
		if _, err := LoadProfiles(strings.NewReader(doc)); err == nil {
			t.Fatalf("%s: LoadProfiles accepted invalid document", name)
		}
	}
}

func TestOpenCodeProfilesWriteTempConfigWithProxyURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	cases := []struct {
		cmd, provider, upstream string
	}{
		{"opencode", "anthropic", "https://api.anthropic.com/v1"},
		{"opencode-openai", "openai", "https://api.openai.com/v1"},
	}
	for _, c := range cases {
		p, ok := ProfileFor(c.cmd)
		if !ok {
			t.Fatalf("ProfileFor(%q) not found", c.cmd)
		}
		if p.BaseURLEnv != "" || p.TempConfig == nil || p.Upstream != c.upstream {
			t.Fatalf("%s profile = %+v, want temp config and upstream %s", c.cmd, p, c.upstream)
		}
		if err := validate(p); err != nil {
			t.Fatalf("%s: validate: %v", c.cmd, err)
		}
		if p.TempConfig.PathEnv != "OPENCODE_CONFIG" || p.TempConfig.Format != "json" {
			t.Fatalf("%s temp config = %+v", c.cmd, *p.TempConfig)
		}

		proxyURL := BaseURL("127.0.0.1:8787", "abc123", p)
		content, err := RenderTempConfig(*p.TempConfig, proxyURL, "8787", "")
		if err != nil {
			t.Fatalf("%s: RenderTempConfig: %v", c.cmd, err)
		}
		path := filepath.Join(t.TempDir(), "opencode.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]map[string]map[string]map[string]string
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s: config is not JSON: %v\n%s", c.cmd, err, data)
		}
		if got := doc["provider"][c.provider]["options"]["baseURL"]; got != proxyURL {
			t.Fatalf("%s: baseURL = %q, want %q", c.cmd, got, proxyURL)
		}
		if env := setEnv(nil, p.TempConfig.PathEnv, path); !slices.Contains(env, "OPENCODE_CONFIG="+path) {
			t.Fatalf("%s: env = %q, want OPENCODE_CONFIG set to the temp file", c.cmd, env)
		}
	}
}

// The claude profile turns on Claude Code's hint headers, which carry the
// compaction signals that the proxy detects.
func TestClaudeProfileSetsGatewayHintHeaders(t *testing.T) {
	p, ok := ProfileFor("claude")
	if !ok {
		t.Fatal("ProfileFor(claude) not found")
	}
	if got := p.Env["CLAUDE_CODE_GATEWAY_HINT_HEADERS"]; got != "1" {
		t.Fatalf("CLAUDE_CODE_GATEWAY_HINT_HEADERS = %q, want %q", got, "1")
	}
}
