package config

import (
	"strings"
	"testing"
)

func loadWindow(t *testing.T, f fixture, env map[string]string, flags map[string]string) (Result, error) {
	t.Helper()
	return Load(Options{GlobalPath: f.global, Root: f.root, LookupEnv: envOf(env), Flags: flags})
}

func TestWindowDefaults(t *testing.T) {
	f := newFixture(t)
	res, err := loadWindow(t, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.WindowLimit != 0 || len(res.Config.WindowModels) != 0 {
		t.Fatalf("defaults = %d %v, want derive-from-model and no entries", res.Config.WindowLimit, res.Config.WindowModels)
	}
	if e := entry(t, res, "window.context_limit"); e.Value != "0" || e.Source != "default" {
		t.Fatalf("window.context_limit = %+v", e)
	}
	if e := entry(t, res, "window.models"); e.Value != "" || e.Source != "default" {
		t.Fatalf("window.models = %+v", e)
	}
}

func TestWindowFromGlobalFile(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "window:\n  context_limit: 80000\n  models:\n    claude-: 120000\n    gpt-4o: 100000\n")
	res, err := loadWindow(t, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.WindowLimit != 80000 {
		t.Fatalf("WindowLimit = %d", res.Config.WindowLimit)
	}
	if got := res.Config.WindowModels; got["claude-"] != 120000 || got["gpt-4o"] != 100000 || len(got) != 2 {
		t.Fatalf("WindowModels = %v", got)
	}
	if e := entry(t, res, "window.models"); e.Value != "claude-=120000,gpt-4o=100000" || !strings.HasPrefix(e.Source, "global:") {
		t.Fatalf("window.models entry = %+v", e)
	}
}

// Model maps merge across layers: a later layer replaces one prefix and keeps the rest.
func TestWindowModelsMergeAcrossLayers(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "window:\n  models:\n    claude-: 120000\n    gpt-4o: 100000\n")
	writeFile(t, f.localPath(), "window:\n  models:\n    claude-: 150000\n")
	res, err := loadWindow(t, f, map[string]string{"ACD_CTX_WINDOW_MODELS": "gpt-4o=50000,o3=190000"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"claude-": 150000, "gpt-4o": 50000, "o3": 190000}
	if got := res.Config.WindowModels; len(got) != len(want) {
		t.Fatalf("WindowModels = %v, want %v", got, want)
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("WindowModels[%s] = %d, want %d (all %v)", k, got[k], v, got)
			}
		}
	}
	if e := entry(t, res, "window.models"); !strings.HasPrefix(e.Source, "env:ACD_CTX_WINDOW_MODELS") {
		t.Fatalf("source of merged map = %q, want the last layer that set it", e.Source)
	}
}

func TestWindowModelKeysAreLowerCase(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "window:\n  models:\n    Claude-Opus: 300000\n")
	res, err := loadWindow(t, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.WindowModels["claude-opus"] != 300000 {
		t.Fatalf("WindowModels = %v, want lower-case key", res.Config.WindowModels)
	}
}

func TestWindowAllowedInLocalAndProjects(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.localPath(), "window:\n  context_limit: 90000\n")
	res, err := loadWindow(t, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.WindowLimit != 90000 {
		t.Fatalf("local window.context_limit not applied: %d", res.Config.WindowLimit)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "window") {
			t.Fatalf("window key wrongly dropped by local scope: %s", w)
		}
	}
	if e := entry(t, res, "window.context_limit"); !strings.HasPrefix(e.Source, "local:") {
		t.Fatalf("source = %q, want local", e.Source)
	}

	writeFile(t, f.global, "projects:\n  "+f.root+":\n    window:\n      models:\n        gpt-5: 250000\n")
	res, err = loadWindow(t, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.WindowModels["gpt-5"] != 250000 {
		t.Fatalf("projects override of window.models not applied: %v", res.Config.WindowModels)
	}
}

func TestWindowFlagWinsOverEnvAndFile(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "window:\n  context_limit: 80000\n")
	res, err := loadWindow(t, f,
		map[string]string{"ACD_CTX_WINDOW_CONTEXT_LIMIT": "120000"},
		map[string]string{"window.context_limit": "150000", "window.models": "claude-=160000"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.WindowLimit != 150000 {
		t.Fatalf("WindowLimit = %d, want flag 150000", res.Config.WindowLimit)
	}
	if e := entry(t, res, "window.context_limit"); e.Source != "flag" {
		t.Fatalf("source = %q, want flag", e.Source)
	}
	if res.Config.WindowModels["claude-"] != 160000 {
		t.Fatalf("flag window.models = %v", res.Config.WindowModels)
	}
}

func TestWindowEnvOverridesFile(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.global, "window:\n  context_limit: 80000\n")
	res, err := loadWindow(t, f, map[string]string{"ACD_CTX_WINDOW_CONTEXT_LIMIT": "120000"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.WindowLimit != 120000 {
		t.Fatalf("WindowLimit = %d, want env 120000", res.Config.WindowLimit)
	}
	if e := entry(t, res, "window.context_limit"); e.Source != "env:ACD_CTX_WINDOW_CONTEXT_LIMIT" {
		t.Fatalf("source = %q", e.Source)
	}
}

func TestWindowValidation(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		env     map[string]string
		flags   map[string]string
		wantErr string
	}{
		{name: "zero derives from model", yaml: "window:\n  context_limit: 0\n"},
		{name: "minimum accepted", yaml: "window:\n  context_limit: 1000\n"},
		{name: "below minimum", yaml: "window:\n  context_limit: 999\n", wantErr: "window.context_limit"},
		{name: "negative", yaml: "window:\n  context_limit: -5\n", wantErr: "window.context_limit"},
		{name: "model window too small", yaml: "window:\n  models:\n    claude-: 500\n", wantErr: "window.models"},
		{name: "env malformed pair", env: map[string]string{"ACD_CTX_WINDOW_MODELS": "claude-"}, wantErr: "prefix=tokens"},
		{name: "env not a number", env: map[string]string{"ACD_CTX_WINDOW_CONTEXT_LIMIT": "big"}, wantErr: "want integer"},
		{name: "flag too small", flags: map[string]string{"window.context_limit": "10"}, wantErr: "window.context_limit"},
		{name: "scalar instead of mapping", yaml: "window:\n  models: 5\n", wantErr: "window.models"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.yaml != "" {
				writeFile(t, f.global, c.yaml)
			}
			_, err := loadWindow(t, f, c.env, c.flags)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, c.wantErr)
			}
		})
	}
}
