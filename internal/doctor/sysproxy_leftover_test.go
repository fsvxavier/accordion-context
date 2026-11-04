package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// A leftover state file from a dead run is a WARN that names the restore command.
// doctor reports it and changes nothing.
func TestSystemLeftoverIsWarn(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	dir := sysproxy.StateDir(db)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := `{"owner":"acd-ctx","version":1,"mode":"system-proxy","backend":"gnome","pid":999999999,"created":"2026-01-01T00:00:00Z"}`
	p := filepath.Join(dir, "system-proxy-999999999.json")
	if err := os.WriteFile(p, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	c := checkSystemLeftovers(db, "", nil)
	if c.Status != Warn {
		t.Fatalf("status = %s, want WARN: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "attach --restore") || !strings.Contains(c.Detail, "stale") {
		t.Fatalf("detail = %q", c.Detail)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("doctor must not remove the state file: %v", err)
	}
}

func TestSystemLeftoverNonePasses(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	if c := checkSystemLeftovers(db, "", nil); c.Status != Pass {
		t.Fatalf("status = %s: %s", c.Status, c.Detail)
	}
}

// probeRunner answers the read-only firewall probes from a set of present
// tables. Any other command fails the test: doctor must only read.
type probeRunner struct {
	t       *testing.T
	present map[string]bool
	cmds    []string
}

func (p *probeRunner) Run(_ context.Context, c sysproxy.Command) (string, error) {
	p.cmds = append(p.cmds, c.String())
	switch {
	case c.Name == "nft" && len(c.Args) == 4 && c.Args[0] == "list":
		if p.present["nft"] {
			return "table inet acd_ctx {\n}\n", nil
		}
	case (c.Name == "iptables" || c.Name == "ip6tables") && len(c.Args) >= 5 && c.Args[4] == "-S":
		if p.present[c.Name] {
			return "-N ACD_CTX_OUT\n", nil
		}
	default:
		p.t.Fatalf("doctor ran a command that is not a read-only probe: %s", c)
	}
	return "", &sysproxy.ExitError{Command: c, Code: 1, Output: "absent"}
}

func TestFirewallLeftoversWarnAndNameRemoval(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	r := &probeRunner{t: t, present: map[string]bool{"nft": true, "ip6tables": true}}
	c := checkSystemLeftovers(db, "", r)
	if c.Status != Warn {
		t.Fatalf("status = %s: %s", c.Status, c.Detail)
	}
	for _, want := range []string{"nft table inet acd_ctx is present", "sudo nft delete table inet acd_ctx", "ip6tables chain ACD_CTX_OUT is present"} {
		if !strings.Contains(c.Detail, want) {
			t.Fatalf("detail misses %q: %s", want, c.Detail)
		}
	}
	if strings.Contains(c.Detail, "iptables chain ACD_CTX_OUT is present") && !strings.Contains(c.Detail, "ip6tables") {
		t.Fatalf("unexpected v4 chain: %s", c.Detail)
	}
}

func TestFirewallAbsentPassesAndOnlyReads(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	r := &probeRunner{t: t}
	if c := checkSystemLeftovers(db, "", r); c.Status != Pass {
		t.Fatalf("status = %s: %s", c.Status, c.Detail)
	}
	if len(r.cmds) != 3 {
		t.Fatalf("probes = %v, want nft plus the two chains", r.cmds)
	}
}

// Without a runner the probes do not run, and the check says so.
func TestNilRunnerSkipsFirewallProbes(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	c := checkSystemLeftovers(db, "", nil)
	if c.Status != Pass || !strings.Contains(c.Detail, "not checked") {
		t.Fatalf("status = %s: %s", c.Status, c.Detail)
	}
}

// A helper process that serves a stale transparent state is named in the
// detail, so the user knows the redirect is being restored by it.
func TestWatchdogLeftoverIsNamed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	dir := sysproxy.StateDir(db)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "transparent-999999999.json")
	stale := `{"owner":"acd-ctx","version":1,"mode":"transparent","firewall":"nft","nft_table":"acd_ctx","pid":999999999,"created":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(statePath, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	procRoot := t.TempDir()
	abs, _ := filepath.Abs(statePath)
	if err := os.MkdirAll(filepath.Join(procRoot, "4242"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := "acd-ctx\x00" + sysproxy.WatchdogArg + "\x00--state\x00" + abs + "\x00--parent-pid\x00999999999\x00"
	if err := os.WriteFile(filepath.Join(procRoot, "4242", "cmdline"), []byte(cmd), 0o644); err != nil {
		t.Fatal(err)
	}
	c := checkSystemLeftovers(db, procRoot, nil)
	if c.Status != Warn || !strings.Contains(c.Detail, "watchdog pid 4242 is still running") {
		t.Fatalf("status = %s: %s", c.Status, c.Detail)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("doctor must not remove the state file: %v", err)
	}
}
