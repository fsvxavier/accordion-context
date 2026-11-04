package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/store"
)

func newGuardServer(t *testing.T, listenHost string, allowed []string) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acd-ctx.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	u, _ := url.Parse("https://api.anthropic.com")
	s, err := New(Config{Upstream: u, Store: st, Version: "test", ListenHost: listenHost,
		AllowedHosts: allowed, GlobalPath: filepath.Join(t.TempDir(), "absent.yaml")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestAllowedHostsGuardIsExact covers the container mode: the guard accepts
// exactly the configured Host names, so a DNS-rebinding name is refused and
// the loopback defaults are not added back.
func TestAllowedHostsGuardIsExact(t *testing.T) {
	s := newGuardServer(t, "0.0.0.0", []string{"localhost", "127.0.0.1", "[::1]", "host.docker.internal:8787"})
	tests := []struct {
		host string
		ok   bool
	}{
		{"localhost:8787", true},
		{"127.0.0.1:8787", true},
		{"[::1]:8787", true},
		{"[::1]", true},
		{"host.docker.internal:8787", true},
		{"HOST.docker.internal:8787", true},
		{"evil.com", false},
		{"evil.com:8787", false},
		{"0.0.0.0:8787", false}, // the listen host is not added to an explicit list
		{"127.0.0.2:8787", false},
		{"", false},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, "/__acd-ctx/health", nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		want := http.StatusForbidden
		if tc.ok {
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Errorf("Host %q: status %d, want %d", tc.host, rec.Code, want)
		}
	}
}

func TestDefaultGuardUnchangedWithoutAllowedHosts(t *testing.T) {
	s := newGuardServer(t, "10.1.2.3", nil)
	for host, ok := range map[string]bool{"localhost:8787": true, "10.1.2.3:8787": true, "evil.com": false} {
		req := httptest.NewRequest(http.MethodGet, "/__acd-ctx/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if got := rec.Code == http.StatusOK; got != ok {
			t.Errorf("default guard, Host %q: status %d", host, rec.Code)
		}
	}
}

func TestNewRejectsWildcardAllowedHost(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "acd-ctx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := url.Parse("https://api.anthropic.com")
	if _, err := New(Config{Upstream: u, Store: st, AllowedHosts: []string{"*"}}); err == nil {
		t.Fatal("wildcard allowed host accepted")
	}
}

func TestCheckListen(t *testing.T) {
	hosts := []string{"host.docker.internal"}
	tests := []struct {
		name  string
		addr  string
		allow bool
		hosts []string
		ok    bool
	}{
		{"loopback v4", "127.0.0.1:8787", false, nil, true},
		{"loopback v6", "[::1]:8787", false, nil, true},
		{"localhost", "localhost:8787", false, nil, true},
		{"wildcard without opt-in", "0.0.0.0:8787", false, hosts, false},
		{"opt-in without hosts", "0.0.0.0:8787", true, nil, false},
		{"hosts without opt-in", "0.0.0.0:8787", false, hosts, false},
		{"opt-in with hosts", "0.0.0.0:8787", true, hosts, true},
		{"malformed address stays refused", "0.0.0.0", true, hosts, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckListen(tc.addr, tc.allow, tc.hosts)
			if (err == nil) != tc.ok {
				t.Fatalf("CheckListen(%q, %v, %v) = %v", tc.addr, tc.allow, tc.hosts, err)
			}
		})
	}
}

func TestRunListenRefusesWithoutOptIn(t *testing.T) {
	h := http.NotFoundHandler()
	if err := RunListen(context.Background(), "0.0.0.0:0", false, []string{"localhost"}, h); err == nil {
		t.Fatal("RunListen bound a wildcard address without opt-in")
	}
	if err := RunListen(context.Background(), "0.0.0.0:0", true, nil, h); err == nil {
		t.Fatal("RunListen bound a wildcard address without allowed hosts")
	}
}

func TestNormalizeHosts(t *testing.T) {
	got, err := NormalizeHosts([]string{" Host.Docker.Internal:8787 ", "[::1]", "::1", "", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"host.docker.internal", "::1", "::1", "localhost"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"*", "*.example.com", "a/b", "a b"} {
		if _, err := NormalizeHosts([]string{bad}); err == nil {
			t.Errorf("NormalizeHosts(%q) accepted", bad)
		}
	}
}
