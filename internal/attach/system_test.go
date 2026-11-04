package attach

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// failRunner fails the test on any command: the refusals below must happen
// before a single command runs.
type failRunner struct{ t *testing.T }

func (f failRunner) Run(_ context.Context, c sysproxy.Command) (string, error) {
	f.t.Fatalf("unexpected command: %s", c)
	return "", nil
}

func sysOptions(t *testing.T, sys *SystemOptions) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		Args:    nil,
		DBPath:  filepath.Join(dir, "acd-ctx.db"),
		CADir:   filepath.Join(dir, "ca"),
		Version: "test",
		System:  sys,
		Cwd:     dir,
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
	}
}

func TestSystemDegreesRefuseWithoutConsent(t *testing.T) {
	env := &sysproxy.Env{GOOS: "linux", Runner: failRunner{t}}
	o := sysOptions(t, &SystemOptions{Yes: false, Env: env})
	o.SystemProxy = true
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("system-proxy without --yes: %v", err)
	}
	o = sysOptions(t, &SystemOptions{Yes: false, Env: env})
	o.Transparent = true
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("transparent without --yes: %v", err)
	}
}

func TestSystemProxyWithoutDesktopBackendFailsBeforeAnyCommand(t *testing.T) {
	env := &sysproxy.Env{
		GOOS:   "linux",
		Getenv: func(string) string { return "XFCE" },
		Runner: failRunner{t},
	}
	o := sysOptions(t, &SystemOptions{Yes: true, Env: env})
	o.SystemProxy = true
	o.MITMDomains = []string{"api.example.com"}
	_, err := Run(context.Background(), o)
	if !errors.Is(err, sysproxy.ErrNoBackend) {
		t.Fatalf("err = %v, want ErrNoBackend", err)
	}
}

func TestTransparentDryRunPrintsRulesAndChangesNothing(t *testing.T) {
	env := &sysproxy.Env{
		GOOS:    "linux",
		Runner:  failRunner{t},
		Geteuid: func() int { return 0 },
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		},
	}
	o := sysOptions(t, &SystemOptions{Yes: true, Apply: false, UID: 1000, Env: env})
	o.Transparent = true
	o.MITMDomains = []string{"api.example.com"}
	out := o.Stdout.(*bytes.Buffer)
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Degree != DegreeTransparent {
		t.Fatalf("degree = %q", res.Degree)
	}
	for _, want := range []string{"meta skuid 1000", "ip daddr @mitm_ipv4", "elements = { 203.0.113.7 }"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("ruleset misses %q:\n%s", want, out.String())
		}
	}
}

func TestTransparentWithoutPrivilegeNeedsRootAndNamesCommand(t *testing.T) {
	env := &sysproxy.Env{
		GOOS:       "linux",
		Runner:     failRunner{t},
		Geteuid:    func() int { return 1000 },
		ProcStatus: func() string { return "CapEff:\t0000000000000000\n" },
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		},
	}
	o := sysOptions(t, &SystemOptions{Yes: true, Apply: true, UID: 1000, Env: env})
	o.Transparent = true
	o.MITMDomains = []string{"api.example.com"}
	_, err := Run(context.Background(), o)
	var need *sysproxy.ErrNeedPrivilege
	if !errors.As(err, &need) {
		t.Fatalf("err = %v, want ErrNeedPrivilege", err)
	}
	if len(need.Commands) != 1 || need.Commands[0] != "sudo acd-ctx attach --transparent --yes --apply --transparent-uid 1000" {
		t.Fatalf("commands = %q", need.Commands)
	}
}

func TestTransparentRejectsCommandAndRequiresUID(t *testing.T) {
	env := &sysproxy.Env{GOOS: "linux", Runner: failRunner{t}, Geteuid: func() int { return 0 }}
	o := sysOptions(t, &SystemOptions{Yes: true, UID: 1000, Env: env})
	o.Transparent = true
	o.Args = []string{"claude"}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "does not run a command") {
		t.Fatalf("transparent with a command: %v", err)
	}
	o = sysOptions(t, &SystemOptions{Yes: true, UID: -1, Env: env})
	o.Transparent = true
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "transparent-uid") {
		t.Fatalf("transparent without a uid: %v", err)
	}
}

// fakeGNOME is a gsettings store for the end-to-end test.
type fakeGNOME struct {
	mu   chan struct{}
	vals map[string]string
}

func (f *fakeGNOME) Run(_ context.Context, c sysproxy.Command) (string, error) {
	f.mu <- struct{}{}
	defer func() { <-f.mu }()
	if c.Name != "gsettings" || len(c.Args) < 3 {
		return "", errors.New("unexpected command " + c.String())
	}
	k := c.Args[1] + " " + c.Args[2]
	switch c.Args[0] {
	case "get":
		v, ok := f.vals[k]
		if !ok {
			return "", &sysproxy.ExitError{Command: c, Code: 1}
		}
		return v + "\n", nil
	case "set":
		f.vals[k] = c.Args[3]
		return "", nil
	}
	return "", errors.New("unexpected verb")
}

func (f *fakeGNOME) get(k string) string {
	f.mu <- struct{}{}
	defer func() { <-f.mu }()
	return f.vals[k]
}

// TestSystemProxyEndToEnd runs the real listener, its peer check against this
// process's own /proc entry, the MITM probe, and the restore on exit.
func TestSystemProxyEndToEnd(t *testing.T) {
	if runtimeGOOSForTest() != "linux" {
		t.Skip("peer check and fake /proc are Linux only")
	}
	const portKey = "org.gnome.system.proxy.https port"
	g := &fakeGNOME{mu: make(chan struct{}, 1), vals: map[string]string{
		"org.gnome.system.proxy mode":         "'none'",
		"org.gnome.system.proxy ignore-hosts": "['localhost']",
		"org.gnome.system.proxy.https host":   "''",
		portKey:                               "0",
	}}
	stateDir := t.TempDir()
	env := &sysproxy.Env{
		GOOS:    "linux",
		Getenv:  func(string) string { return "GNOME" },
		Runner:  g,
		Geteuid: func() int { return os.Geteuid() },
		Alive:   func(int, uint64) bool { return true },
	}
	o := sysOptions(t, &SystemOptions{Yes: true, Env: env, StateDir: stateDir})
	o.SystemProxy = true
	o.MITMDomains = []string{"api.example.com"}
	o.CADir = filepath.Join(t.TempDir(), "ca")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, o)
		done <- err
	}()

	deadline := time.Now().Add(20 * time.Second)
	for g.get(portKey) == "0" {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("system proxy was never set")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := g.get("org.gnome.system.proxy.https host"); got != "'127.0.0.1'" {
		t.Fatalf("host = %s", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := g.get(portKey); got != "0" {
		t.Fatalf("port not restored: %s", got)
	}
	if got := g.get("org.gnome.system.proxy mode"); got != "'none'" {
		t.Fatalf("mode not restored: %s", got)
	}
	entries, _ := os.ReadDir(stateDir)
	if len(entries) != 0 {
		t.Fatalf("state files left behind: %v", entries)
	}
}

func runtimeGOOSForTest() string { return goruntime.GOOS }
