package launcher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/project"
)

// TestMain doubles as a fake "acd-ctx serve" when the launcher spawns the
// test binary as its proxy (Options.ServeExe = os.Executable()).
func TestMain(m *testing.M) {
	if os.Getenv("LAUNCHER_TEST_SERVE") == "1" {
		os.Exit(fakeServe(os.Args))
	}
	os.Exit(m.Run())
}

func fakeServe(args []string) int {
	for i := range args {
		if args[i] != "--listen" || i+1 >= len(args) {
			continue
		}
		ln, err := net.Listen("tcp", args[i+1])
		if err != nil {
			return 1
		}
		version := os.Getenv("LAUNCHER_TEST_VERSION")
		mux := http.NewServeMux()
		mux.HandleFunc(pathHealth, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"ok":true}`)
		})
		mux.HandleFunc(pathStatus, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"version":"`+version+`"}`)
		})
		if err := http.Serve(ln, mux); err != nil {
			return 1
		}
		return 0
	}
	return 2
}

// syncBuffer is a writer safe for the client goroutine and the test to share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func shellArgs(script string) []string {
	return []string{"/bin/sh", "-c", script}
}

func fakeProfile() Profile {
	return Profile{
		Name:       "fake",
		Upstream:   "https://api.example.com",
		BaseURLEnv: "FAKE_BASE_URL",
		Env:        map[string]string{"FAKE_STATIC": "yes"},
	}
}

func healthServer(t *testing.T, version string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(pathHealth, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	mux.HandleFunc(pathStatus, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"`+version+`"}`)
	})
	srv := newTestServer(t, mux)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestRunReusesHealthyProxyOfSameVersion(t *testing.T) {
	addr := healthServer(t, "v1")
	dir := t.TempDir()
	prof := fakeProfile()
	out := &syncBuffer{}

	code, err := Run(context.Background(), Options{
		Args:      shellArgs(`printf '%s %s' "$FAKE_BASE_URL" "$FAKE_STATIC"; exit 3`),
		Profile:   &prof,
		Cwd:       dir,
		Version:   "v1",
		ProxyAddr: addr,
		ServeExe:  "/nonexistent/acd-ctx", // must not be spawned
		Stdin:     strings.NewReader(""),
		Stdout:    out,
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3 (client's own code)", code)
	}
	res, err := project.Resolve(dir, defaultMarkers)
	if err != nil {
		t.Fatal(err)
	}
	want := "http://" + addr + "/p/" + res.ID + "/ctx/https://api.example.com yes"
	if got := out.String(); got != want {
		t.Fatalf("client env = %q, want %q", got, want)
	}
}

func TestRunStartsOwnProxyOnVersionMismatchAndStopsIt(t *testing.T) {
	t.Setenv("LAUNCHER_TEST_SERVE", "1")
	t.Setenv("LAUNCHER_TEST_VERSION", "new")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	addr := healthServer(t, "old")
	prof := fakeProfile()
	out := &syncBuffer{}

	code, err := Run(context.Background(), Options{
		Args:      shellArgs(`printf '%s' "$FAKE_BASE_URL"`),
		Profile:   &prof,
		Cwd:       t.TempDir(),
		Version:   "new",
		ProxyAddr: addr,
		ServeExe:  exe,
		Stdin:     strings.NewReader(""),
		Stdout:    out,
		Stderr:    io.Discard,
	})
	if err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	u, err := url.Parse(out.String())
	if err != nil {
		t.Fatalf("base URL %q: %v", out.String(), err)
	}
	started := u.Host
	if started == addr {
		t.Fatalf("launcher reused the old-version proxy on %s", addr)
	}
	if healthy(context.Background(), started, "") {
		t.Fatalf("started proxy on %s still healthy after Run returned", started)
	}
}

func TestRunRefusesClientWithoutBaseURLVariable(t *testing.T) {
	code, err := Run(context.Background(), Options{
		Args:     []string{"opencode"},
		Cwd:      t.TempDir(),
		ServeExe: "/nonexistent/acd-ctx",
		Stderr:   io.Discard,
	})
	if !errors.Is(err, ErrNoBaseURL) {
		t.Fatalf("err = %v, want ErrNoBaseURL", err)
	}
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestRunRejectsUnknownClientAndEmptyArgs(t *testing.T) {
	if code, err := Run(context.Background(), Options{Args: []string{"nosuchclient"}, Cwd: t.TempDir()}); err == nil || code != 1 {
		t.Fatalf("unknown client = (%d, %v), want (1, error)", code, err)
	}
	if code, err := Run(context.Background(), Options{}); err == nil || code != 2 {
		t.Fatalf("no args = (%d, %v), want (2, error)", code, err)
	}
}

func TestRunReportsSignalDeathAsShellCode(t *testing.T) {
	addr := healthServer(t, "v1")
	prof := fakeProfile()
	code, err := Run(context.Background(), Options{
		Args:      shellArgs(`kill -TERM $$`),
		Profile:   &prof,
		Cwd:       t.TempDir(),
		Version:   "v1",
		ProxyAddr: addr,
		Stdin:     strings.NewReader(""),
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 128+15 {
		t.Fatalf("code = %d, want %d", code, 128+15)
	}
}

func TestSetEnvReplacesExistingKey(t *testing.T) {
	env := setEnv([]string{"A=1", "FAKE_BASE_URL=old", "B=2"}, "FAKE_BASE_URL", "new")
	want := []string{"A=1", "B=2", "FAKE_BASE_URL=new"}
	if strings.Join(env, "|") != strings.Join(want, "|") {
		t.Fatalf("setEnv = %v, want %v", env, want)
	}
}

func newTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}
