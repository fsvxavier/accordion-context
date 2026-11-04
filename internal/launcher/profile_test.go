package launcher

import (
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
