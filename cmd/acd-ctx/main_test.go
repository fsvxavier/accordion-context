package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func get(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// startServe runs serve on a free loopback port with a temp database, and waits
// until the proxy health endpoint answers. It returns the base URL, the stderr
// buffer and a channel with serve's exit code.
func startServe(t *testing.T, dir string, extra ...string) (string, *bytes.Buffer, chan int) {
	t.Helper()
	addr := freeAddr(t)
	var stderr bytes.Buffer
	done := make(chan int, 1)
	args := append([]string{
		"--listen", addr,
		"--db", filepath.Join(dir, "acd-ctx.db"),
		"--config", filepath.Join(dir, "missing-config.yaml"),
	}, extra...)
	go func() { done <- serve(args, &stderr) }()

	base := "http://" + addr
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if code, _ := get(t, base+"/__acd-ctx/health", ""); code == http.StatusOK {
			return base, &stderr, done
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = interrupt()
	t.Fatalf("serve did not come up: %s", stderr.String())
	return "", nil, nil
}

// stopServe interrupts serve and checks that it exits cleanly.
func stopServe(t *testing.T, done chan int, stderr *bytes.Buffer) {
	t.Helper()
	if err := interrupt(); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit code %d: %s", code, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop after interrupt")
	}
}

// TestServeMountsPluginProtocol checks that /__acd-ctx/plugin/ answers only with
// the per-run token, that the token file is 0600, and that the proxy routes
// still work next to it.
func TestServeMountsPluginProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stops serve with an interrupt signal, which needs a unix process")
	}
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "tok", "plugin.token")
	base, stderr, done := startServe(t, dir, "--plugin-token-file", tokenFile)

	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 32 {
		t.Fatalf("token too short: %d", len(token))
	}
	fi, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token file mode = %o, want 0600", mode)
	}

	if code, body := get(t, base+"/__acd-ctx/plugin/manifest", token); code != http.StatusOK || !strings.Contains(body, `"acd-ctx-plugin"`) {
		t.Fatalf("manifest with token: %d %s", code, body)
	}
	if code, _ := get(t, base+"/__acd-ctx/plugin/manifest", ""); code != http.StatusUnauthorized {
		t.Fatalf("manifest without token: %d, want 401", code)
	}
	if code, _ := get(t, base+"/__acd-ctx/plugin/manifest", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("manifest with wrong token: %d, want 401", code)
	}
	if code, _ := get(t, base+"/__acd-ctx/plugin/nope", token); code != http.StatusNotFound {
		t.Fatalf("unknown plugin path: %d, want 404", code)
	}
	if code, body := get(t, base+"/__acd-ctx/health", ""); code != http.StatusOK || !strings.Contains(body, "ok") {
		t.Fatalf("proxy health changed: %d %s", code, body)
	}
	stopServe(t, done, stderr)
}

// TestServeWithoutPluginTokenServesProxy checks the fail-open path: when the
// token file cannot be written, serve logs it and still runs the proxy, and the
// plugin prefix is not mounted.
func TestServeWithoutPluginTokenServesProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stops serve with an interrupt signal, which needs a unix process")
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, stderr, done := startServe(t, dir, "--plugin-token-file", filepath.Join(blocker, "plugin.token"))

	if !strings.Contains(stderr.String(), "plugin protocol disabled") {
		t.Fatalf("missing warning in log: %s", stderr.String())
	}
	if code, _ := get(t, base+"/__acd-ctx/plugin/manifest", "anything"); code == http.StatusOK {
		t.Fatal("plugin endpoints answered although the token file failed")
	}
	if code, _ := get(t, base+"/__acd-ctx/health", ""); code != http.StatusOK {
		t.Fatalf("proxy health: %d", code)
	}
	stopServe(t, done, stderr)
}

func TestDefaultTokenName(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8787": "plugin.token",
		"127.0.0.1:9000": "plugin-9000.token",
		"bad":            "plugin.token",
	} {
		if got := defaultTokenName(in); got != want {
			t.Errorf("defaultTokenName(%q)=%q, want %q", in, got, want)
		}
	}
}
