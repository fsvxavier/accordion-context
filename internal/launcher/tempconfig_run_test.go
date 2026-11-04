//go:build unix

package launcher

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/project"
)

// fakeOpencode puts a fake "opencode" first on PATH. It records the
// OPENCODE_CONFIG path and a copy of that file, then runs body. It returns the
// record directory and the TMPDIR the launcher's temp dir is created in, so a
// test can check that nothing is left behind.
func fakeOpencode(t *testing.T, body string) (rec, tmp string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	tmp = t.TempDir()
	t.Setenv("TMPDIR", tmp)
	bin := t.TempDir()
	rec = t.TempDir()
	script := "#!/bin/sh\n" +
		`printf '%s' "$OPENCODE_CONFIG" > "` + rec + `/path"` + "\n" +
		`cp "$OPENCODE_CONFIG" "` + rec + `/config.json"` + "\n" +
		body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return rec, tmp
}

func assertTempDirEmpty(t *testing.T, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp dir %s not cleaned: %d entries", tmp, len(entries))
	}
}

// TestRunOpencodeUsesTempConfigThroughProxy checks that opencode, which has no
// base URL variable, is pointed at the proxy through OPENCODE_CONFIG, and that
// the file is removed when the run ends.
func TestRunOpencodeUsesTempConfigThroughProxy(t *testing.T) {
	rec, tmp := fakeOpencode(t, "exit 0")
	addr := healthServer(t, "v1")
	cwd := t.TempDir()

	code, err := Run(context.Background(), Options{
		Args:      []string{"opencode"},
		Cwd:       cwd,
		Version:   "v1",
		ProxyAddr: addr,
		ServeExe:  "/nonexistent/acd-ctx", // must not be spawned
		Stdin:     strings.NewReader(""),
		Stdout:    io.Discard,
		Stderr:    io.Discard,
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
	var cfg struct {
		Provider struct {
			Anthropic struct {
				Options struct {
					BaseURL string `json:"baseURL"`
				} `json:"options"`
			} `json:"anthropic"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config %q is not JSON: %v", raw, err)
	}
	want := "http://" + addr + "/p/" + res.ID + "/ctx/https://api.anthropic.com/v1"
	if got := cfg.Provider.Anthropic.Options.BaseURL; got != want {
		t.Fatalf("baseURL = %q, want %q", got, want)
	}

	path, err := os.ReadFile(filepath.Join(rec, "path"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(path), tmp+string(os.PathSeparator)) {
		t.Fatalf("OPENCODE_CONFIG = %q, want a file in %s", path, tmp)
	}
	if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
		t.Fatalf("temp config still present after Run: %v", err)
	}
	assertTempDirEmpty(t, tmp)
}

// TestRunRemovesOpencodeTempConfigOnSignal sends SIGTERM to the launcher while
// the client runs. The client exits with 42 after the signal is forwarded, and
// the temp config must still be removed.
func TestRunRemovesOpencodeTempConfigOnSignal(t *testing.T) {
	_, tmp := fakeOpencode(t, `trap 'exit 42' TERM; echo ready; while :; do sleep 0.05; done`)
	addr := healthServer(t, "v1")
	out := &syncBuffer{}
	type result struct {
		code int
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		code, err := Run(context.Background(), Options{
			Args:      []string{"opencode"},
			Cwd:       t.TempDir(),
			Version:   "v1",
			ProxyAddr: addr,
			Stdin:     strings.NewReader(""),
			Stdout:    out,
			Stderr:    io.Discard,
		})
		resc <- result{code, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "ready") {
		if time.Now().After(deadline) {
			t.Fatal("client never reported ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-resc:
		if r.err != nil || r.code != 42 {
			t.Fatalf("Run = (%d, %v), want (42, nil)", r.code, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after forwarded signal")
	}
	assertTempDirEmpty(t, tmp)
}
