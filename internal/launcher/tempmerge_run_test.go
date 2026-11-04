//go:build unix

package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/project"
)

// runOpencodeWithUserConfig runs the fake opencode with OPENCODE_CONFIG set to
// the given user config content (or unset when userCfg is nil). It returns the
// config the client saw and the launcher's stderr.
func runOpencodeWithUserConfig(t *testing.T, userCfg []byte) (cfg map[string]any, stderr string, userPath string) {
	t.Helper()
	rec, _ := fakeOpencode(t, "exit 0")
	addr := healthServer(t, "v1")
	cwd := t.TempDir()
	if userCfg != nil {
		userPath = filepath.Join(t.TempDir(), "user-opencode.json")
		if err := os.WriteFile(userPath, userCfg, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("OPENCODE_CONFIG", userPath)
	} else {
		t.Setenv("OPENCODE_CONFIG", "")
		_ = os.Unsetenv("OPENCODE_CONFIG")
	}
	errBuf := &syncBuffer{}
	code, err := Run(context.Background(), Options{
		Args:      []string{"opencode"},
		Cwd:       cwd,
		Version:   "v1",
		ProxyAddr: addr,
		ServeExe:  "/nonexistent/acd-ctx",
		Stdin:     strings.NewReader(""),
		Stdout:    io.Discard,
		Stderr:    errBuf,
	})
	if err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	res, err := project.Resolve(cwd, defaultMarkers)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(rec, "config.json"))
	if err != nil {
		t.Fatalf("client did not record its config: %v", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config %q is not JSON: %v", raw, err)
	}
	wantBase := "http://" + addr + "/p/" + res.ID + "/ctx/https://api.anthropic.com/v1"
	if got := dig(cfg, "provider", "anthropic", "options", "baseURL"); got != wantBase {
		t.Fatalf("baseURL = %v, want %q", got, wantBase)
	}
	return cfg, errBuf.String(), userPath
}

// TestRunOpencodeKeepsUserConfigAndSetsProxyBaseURL checks that a user config
// already named by OPENCODE_CONFIG is kept as the base: its unrelated keys
// survive, its different baseURL is replaced by the proxy one, and the user's
// file is left untouched.
func TestRunOpencodeKeepsUserConfigAndSetsProxyBaseURL(t *testing.T) {
	user := []byte(`{"theme":"dark","provider":{"anthropic":{"options":{"baseURL":"https://example.invalid/v1","timeout":5000}}}}`)
	cfg, stderr, userPath := runOpencodeWithUserConfig(t, user)
	if got := cfg["theme"]; got != "dark" {
		t.Fatalf("theme = %v, want dark (user key dropped)", got)
	}
	if got := dig(cfg, "provider", "anthropic", "options", "timeout"); got != float64(5000) {
		t.Fatalf("timeout = %v, want 5000 (sibling key dropped)", got)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	after, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, user) {
		t.Fatalf("user config was modified: %s", after)
	}
}

// TestRunOpencodeInvalidUserConfigWarnsAndUsesTemplate checks that an
// unparseable user file gives a one-line warning and the profile config alone.
func TestRunOpencodeInvalidUserConfigWarnsAndUsesTemplate(t *testing.T) {
	cfg, stderr, userPath := runOpencodeWithUserConfig(t, []byte(`{"theme": "dark",`))
	if len(cfg) != 1 || cfg["provider"] == nil {
		t.Fatalf("config = %v, want the profile template only", cfg)
	}
	if strings.Count(strings.TrimSpace(stderr), "\n") != 0 || !strings.Contains(stderr, "acd-ctx: warning: OPENCODE_CONFIG="+userPath) {
		t.Fatalf("stderr = %q, want one warning naming OPENCODE_CONFIG and its path", stderr)
	}
}

// TestRunOpencodeUnreadableUserConfigWarnsAndUsesTemplate points
// OPENCODE_CONFIG at a directory, which cannot be read as a file.
func TestRunOpencodeUnreadableUserConfigWarnsAndUsesTemplate(t *testing.T) {
	rec, _ := fakeOpencode(t, "exit 0")
	addr := healthServer(t, "v1")
	t.Setenv("OPENCODE_CONFIG", t.TempDir())
	errBuf := &syncBuffer{}
	code, err := Run(context.Background(), Options{
		Args:      []string{"opencode"},
		Cwd:       t.TempDir(),
		Version:   "v1",
		ProxyAddr: addr,
		ServeExe:  "/nonexistent/acd-ctx",
		Stdin:     strings.NewReader(""),
		Stdout:    io.Discard,
		Stderr:    errBuf,
	})
	if err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	raw, err := os.ReadFile(filepath.Join(rec, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg) != 1 || cfg["provider"] == nil {
		t.Fatalf("config = %s, want the profile template only (%v)", raw, err)
	}
	if !strings.Contains(errBuf.String(), "acd-ctx: warning: OPENCODE_CONFIG=") {
		t.Fatalf("stderr = %q, want a warning", errBuf.String())
	}
}
