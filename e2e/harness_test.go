// Package e2e runs the real acd-ctx binary against a fake provider API. Nothing
// here touches the network: the upstream is an httptest server on loopback and
// the proxy listens on a free loopback port.
package e2e

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite" // driver for reading the events table directly
)

// buildBinary compiles ./cmd/acd-ctx into the test's temp dir.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "acd-ctx")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/acd-ctx")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/acd-ctx: %v\n%s", err, out)
	}
	return bin
}

// repoRoot is the module root; go test runs with the package directory (e2e/) as cwd.
func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// freeAddr returns a loopback address with a free port.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// syncBuf is a stderr sink that the process goroutine and the test can share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// server is one running acd-ctx serve process.
type server struct {
	t    *testing.T
	cmd  *exec.Cmd
	base string // http://127.0.0.1:port
	log  *syncBuf
	done chan error
}

// serveEnv is the environment of the proxy: HOME and XDG dirs inside home, and no ACD_CTX_* overrides.
func serveEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "ACD_CTX_") || strings.HasPrefix(kv, "HOME=") ||
			strings.HasPrefix(kv, "XDG_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"))
}

// fixture holds the paths shared by the restarts of one scenario.
type fixture struct {
	bin      string
	home     string
	db       string
	config   string
	token    string
	upstream string
}

// newFixture writes the configuration file and returns the paths of one scenario.
func newFixture(t *testing.T, bin, upstream, configYAML string) fixture {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "acd-ctx-config.yaml")
	if err := os.WriteFile(cfg, []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture{bin: bin, home: home, db: filepath.Join(home, "data", "acd-ctx.db"),
		config: cfg, token: filepath.Join(home, "plugin.token"), upstream: upstream}
}

// start launches serve on a free port and waits until its health endpoint answers.
func (fx fixture) start(t *testing.T) *server {
	t.Helper()
	addr := freeAddr(t)
	cmd := exec.Command(fx.bin, "serve", "--listen", addr, "--upstream", fx.upstream,
		"--db", fx.db, "--config", fx.config, "--plugin-token-file", fx.token)
	cmd.Dir = fx.home
	cmd.Env = serveEnv(fx.home)
	logs := &syncBuf{}
	cmd.Stderr = logs
	cmd.Stdout = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	s := &server{t: t, cmd: cmd, base: "http://" + addr, log: logs, done: make(chan error, 1)}
	go func() { s.done <- cmd.Wait() }()
	t.Cleanup(func() { s.kill() })

	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(s.base + "/__acd-ctx/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve did not become healthy at %s\nlog:\n%s", addr, logs.String())
		}
		select {
		case err := <-s.done:
			t.Fatalf("serve exited early: %v\nlog:\n%s", err, logs.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// stop sends SIGTERM and requires a clean exit (the proxy shuts down gracefully).
func (s *server) stop() {
	s.t.Helper()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		s.t.Fatalf("signal serve: %v", err)
	}
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Fatalf("serve exit: %v\nlog:\n%s", err, s.log.String())
		}
	case <-time.After(20 * time.Second):
		s.kill()
		s.t.Fatalf("serve did not stop within 20s\nlog:\n%s", s.log.String())
	}
}

func (s *server) kill() {
	if s.cmd.ProcessState == nil {
		_ = s.cmd.Process.Kill()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		}
	}
}

// logHasLevel reports whether a JSON log line with the given level was written.
func (s *server) logHasLevel(level string) []string {
	var out []string
	for _, line := range strings.Split(s.log.String(), "\n") {
		var rec struct {
			Level string `json:"level"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Level == level {
			out = append(out, line)
		}
	}
	return out
}

// openEvents reads the events table of the database (the proxy must be stopped).
func openEvents(t *testing.T, db string) []eventRow {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query(`SELECT session_id, event_seq, type, payload FROM events ORDER BY session_id, event_seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []eventRow
	for rows.Next() {
		var e eventRow
		if err := rows.Scan(&e.Session, &e.Seq, &e.Type, &e.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type eventRow struct {
	Session string
	Seq     int64
	Type    string
	Payload string
}
