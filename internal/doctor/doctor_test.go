package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/launcher"
	"github.com/fsvxavier/accordion-context/internal/mitm"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// fakeProxy serves the proxy's health and status endpoints on loopback.
func fakeProxy(t *testing.T, version string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/__acd-ctx/health":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/__acd-ctx/status":
			_, _ = w.Write([]byte(`{"status":"ok","version":"` + version + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// base returns options that touch nothing outside t.TempDir().
func base(t *testing.T) Options {
	dir := t.TempDir()
	return Options{
		Cwd:              dir,
		GlobalConfigPath: filepath.Join(dir, "config", "config.yaml"),
		DBPath:           filepath.Join(dir, "data", "acd-ctx.db"),
		CADir:            filepath.Join(dir, "ca"),
		Env:              []string{"PATH=/usr/bin"},
		Now:              time.Now,
	}
}

func find(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, r.Checks)
	return Check{}
}

func TestReadOnlyDoesNotCreateDatabaseOrCA(t *testing.T) {
	o := base(t)
	r, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if c := find(t, r, "database"); c.Status != Warn {
		t.Fatalf("database = %+v, want WARN", c)
	}
	if _, err := os.Stat(o.DBPath); !os.IsNotExist(err) {
		t.Fatalf("doctor created the database: %v", err)
	}
	if _, err := os.Stat(o.CADir); !os.IsNotExist(err) {
		t.Fatalf("doctor created the CA dir: %v", err)
	}
	if c := find(t, r, "ca-cert"); c.Status != Warn {
		t.Fatalf("ca-cert = %+v, want WARN", c)
	}
}

func TestCAHealthyThenKeyModeAndExpiry(t *testing.T) {
	o := base(t)
	if _, err := mitm.LoadOrCreateCA(o.CADir, mitm.CAOptions{Domains: []string{"api.example.test"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "ca-cert"); c.Status != Pass {
		t.Fatalf("ca-cert = %+v", c)
	}
	if c := find(t, r, "ca-key"); c.Status != Pass {
		t.Fatalf("ca-key = %+v", c)
	}
	if c := find(t, r, "ca-dir"); c.Status != Pass {
		t.Fatalf("ca-dir = %+v", c)
	}

	if err := os.Chmod(filepath.Join(o.CADir, "ca.key"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "ca-key"); c.Status != Warn {
		t.Fatalf("loose key mode = %+v, want WARN", c)
	}

	// A CA valid for one hour is expired two hours later.
	o2 := base(t)
	if _, err := mitm.LoadOrCreateCA(o2.CADir, mitm.CAOptions{Validity: time.Hour}); err != nil {
		t.Fatal(err)
	}
	o2.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	r, _ = Run(context.Background(), o2)
	if c := find(t, r, "ca-cert"); c.Status != Fail {
		t.Fatalf("expired CA = %+v, want FAIL", c)
	}
	if r.ExitCode != ExitFail || r.Summary != Fail {
		t.Fatalf("summary %s exit %d, want FAIL/1", r.Summary, r.ExitCode)
	}
}

func TestCAKeyMismatchFails(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	if _, err := mitm.LoadOrCreateCA(dirA, mitm.CAOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := mitm.LoadOrCreateCA(dirB, mitm.CAOptions{}); err != nil {
		t.Fatal(err)
	}
	keyB, err := os.ReadFile(filepath.Join(dirB, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "ca.key"), keyB, 0o600); err != nil {
		t.Fatal(err)
	}
	o := base(t)
	o.CADir = dirA
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "ca-key"); c.Status != Fail || !strings.Contains(c.Detail, "does not match") {
		t.Fatalf("mismatched key = %+v", c)
	}
}

func TestProxyHealthAndVersion(t *testing.T) {
	o := base(t)
	o.ProxyAddr = fakeProxy(t, "1.2.3")
	o.Version = "1.2.3"
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "proxy"); c.Status != Pass {
		t.Fatalf("healthy proxy = %+v", c)
	}

	o.Version = "9.9.9"
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "proxy"); c.Status != Warn || !strings.Contains(c.Detail, "expected") {
		t.Fatalf("version mismatch = %+v", c)
	}

	o.ProxyAddr = freeLoopback(t)
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "proxy"); c.Status != Warn || !strings.Contains(c.Detail, "no proxy") {
		t.Fatalf("absent proxy = %+v, want WARN", c)
	}

	o.ProxyAddr = "203.0.113.7:8787"
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "proxy"); c.Status != Fail {
		t.Fatalf("non-loopback probe = %+v, want FAIL", c)
	}
}

func freeLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestForeignBaseURLAndDoubleCompression(t *testing.T) {
	o := base(t)
	other := fakeProxy(t, "x")
	o.ProxyAddr = freeLoopback(t)
	o.Env = []string{
		"OPENAI_BASE_URL=https://corp.example/v1",
		"ANTHROPIC_BASE_URL=http://" + other + "/ctx/https://api.anthropic.com",
	}
	r, _ := Run(context.Background(), o)
	c := find(t, r, "base-urls")
	if c.Status != Warn {
		t.Fatalf("base-urls = %+v, want WARN", c)
	}
	if !strings.Contains(c.Detail, "corp.example") || !strings.Contains(c.Detail, "another acd-ctx") {
		t.Fatalf("detail misses findings: %s", c.Detail)
	}

	o.Env = []string{"ACD_CTX_NATIVE=claude-plugin"}
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "base-urls"); c.Status != Warn || !strings.Contains(c.Detail, "native plugin") {
		t.Fatalf("native plugin = %+v", c)
	}
}

func TestNoProxyBypassWarns(t *testing.T) {
	o := base(t)
	o.Env = []string{"NO_PROXY=.anthropic.com,intranet.test"}
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "no-proxy"); c.Status != Warn {
		t.Fatalf("no-proxy = %+v, want WARN", c)
	}
	o.Env = []string{"NO_PROXY=intranet.test"}
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "no-proxy"); c.Status != Pass {
		t.Fatalf("no-proxy = %+v, want PASS", c)
	}
}

func TestRejectionsFromLiveServer(t *testing.T) {
	o := base(t)
	o.Rejections = func() []mitm.Rejection {
		return []mitm.Rejection{{Host: "api.anthropic.com", Count: 2, Last: time.Now(), Reason: "unknown_ca"}}
	}
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "ca-rejections"); c.Status != Fail || !strings.Contains(c.Detail, "unknown_ca") {
		t.Fatalf("recent rejection = %+v", c)
	}
	o.Rejections = func() []mitm.Rejection {
		return []mitm.Rejection{{Host: "x.test", Count: 1, Last: time.Now().Add(-48 * time.Hour), Reason: "bad_certificate"}}
	}
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "ca-rejections"); c.Status != Warn {
		t.Fatalf("old rejection = %+v, want WARN", c)
	}
	o.Rejections = nil
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "ca-rejections"); c.Status != Pass || !strings.Contains(c.Detail, "not inspected") {
		t.Fatalf("no live server = %+v", c)
	}
}

func TestIntegrationForClient(t *testing.T) {
	o := base(t)
	o.Client = "claude"
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "integration"); c.Status != Pass || !strings.Contains(c.Detail, "ANTHROPIC_BASE_URL") {
		t.Fatalf("claude = %+v", c)
	}
	o.Client = "unknown-console"
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "integration"); c.Status != Warn || !strings.Contains(c.Detail, "console") {
		t.Fatalf("unknown = %+v", c)
	}
}

func TestIntegrationDescribesFlagAndTempConfigDegrees(t *testing.T) {
	o := base(t)
	o.Client = "flagged-cli"
	o.Profiles = []launcher.Profile{{
		Name:        "flagged",
		Match:       []string{"flagged-cli"},
		Upstream:    "https://api.example.com/v1",
		BaseURLFlag: "--api-base {url}",
		TempConfig:  &launcher.TempConfig{PathFlag: "--config {path}", Format: "json", Template: `{"base_url": "{url}"}`},
	}}
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "integration"); c.Status != Pass || !strings.Contains(c.Detail, "base-url-flag") || !strings.Contains(c.Detail, "temp-config") {
		t.Fatalf("flag+temp profile = %+v", c)
	}
	o.Profiles[0].Quirks = map[string]any{"ignores_base_url": true}
	r, _ = Run(context.Background(), o)
	if c := find(t, r, "integration"); c.Status != Pass || !strings.Contains(c.Detail, "ignores base URLs") {
		t.Fatalf("ignores_base_url profile = %+v", c)
	}
}

func TestDatabaseChecks(t *testing.T) {
	o := base(t)
	if err := os.MkdirAll(filepath.Dir(o.DBPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.DBPath, []byte("not a database at all, just noise padding"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "database"); c.Status != Fail {
		t.Fatalf("corrupt database = %+v, want FAIL", c)
	}

	o2 := base(t)
	if err := os.MkdirAll(filepath.Dir(o2.DBPath), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(o2.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureProject(context.Background(), "p1", t.TempDir(), "global", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	r, _ = Run(context.Background(), o2)
	if c := find(t, r, "database"); c.Status != Pass || !strings.Contains(c.Detail, "1 project(s)") {
		t.Fatalf("healthy database = %+v", c)
	}
}

func TestWriteJSONAndSummaryExitCodes(t *testing.T) {
	o := base(t)
	r, _ := Run(context.Background(), o)
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("json: %v", err)
	}
	if back.Summary != r.Summary || len(back.Checks) != len(r.Checks) {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if r.Summary != Warn || r.ExitCode != ExitWarn {
		t.Fatalf("fresh install summary %s exit %d, want WARN/%d", r.Summary, r.ExitCode, ExitWarn)
	}
	var text bytes.Buffer
	Write(&text, r)
	if !strings.Contains(text.String(), "summary: WARN") {
		t.Fatalf("text output: %s", text.String())
	}
}

func TestNoProxyWildcardAndPortWarn(t *testing.T) {
	for _, np := range []string{"*", "api.anthropic.com:443", "localhost,*"} {
		o := base(t)
		o.Env = []string{"NO_PROXY=" + np}
		r, _ := Run(context.Background(), o)
		if c := find(t, r, "no-proxy"); c.Status != Warn {
			t.Fatalf("NO_PROXY=%q: %+v, want WARN", np, c)
		}
	}
}

func TestCAKeyWithoutCertIsNotPass(t *testing.T) {
	o := base(t)
	if _, err := mitm.LoadOrCreateCA(o.CADir, mitm.CAOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(o.CADir, "ca.crt")); err != nil {
		t.Fatal(err)
	}
	r, _ := Run(context.Background(), o)
	if c := find(t, r, "ca-key"); c.Status == Pass {
		t.Fatalf("ca-key = %+v, want not PASS without ca.crt", c)
	}
}

func TestReportNeverEchoesBaseURLCredentials(t *testing.T) {
	o := base(t)
	o.Env = []string{"ANTHROPIC_BASE_URL=https://user:s3cr3tpw@gw.example/v1?key=s3cr3tkey", "NO_PROXY=x"}
	r, _ := Run(context.Background(), o)
	var buf bytes.Buffer
	Write(&buf, r)
	_ = WriteJSON(&buf, r)
	if strings.Contains(buf.String(), "s3cr3t") {
		t.Fatalf("report leaks a credential:\n%s", buf.String())
	}
}
