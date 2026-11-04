package sysproxy

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

func ips(t *testing.T, in ...string) []netip.Addr {
	t.Helper()
	var out []netip.Addr
	for _, s := range in {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

const wantRulesetSet = `create table inet acd_ctx
flush table inet acd_ctx
table inet acd_ctx {
	set mitm_ipv4 {
		type ipv4_addr
		elements = { 1.1.1.1, 93.184.216.34 }
	}

	chain output {
		type nat hook output priority -100; policy accept;
		meta skuid 0 accept comment "acd-ctx"
		ip daddr 127.0.0.0/8 accept comment "acd-ctx"
		meta skuid 1000 meta l4proto tcp tcp dport 443 ip daddr @mitm_ipv4 redirect to :8443 comment "acd-ctx"
	}
}
`

const wantRulesetAll443 = `create table inet acd_ctx
flush table inet acd_ctx
table inet acd_ctx {

	chain output {
		type nat hook output priority -100; policy accept;
		meta skuid 0 accept comment "acd-ctx"
		ip daddr 127.0.0.0/8 accept comment "acd-ctx"
		meta skuid 1000 meta nfproto ipv4 meta l4proto tcp tcp dport 443 redirect to :8443 comment "acd-ctx"
	}
}
`

func TestRulesetGolden(t *testing.T) {
	p := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443,
		IPv4: ips(t, "93.184.216.34", "1.1.1.1", "93.184.216.34")}
	got, err := p.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if got != wantRulesetSet {
		t.Fatalf("ruleset mismatch\n--- got\n%s--- want\n%s", got, wantRulesetSet)
	}
	all := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, AllDestinations: true}
	got, err = all.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if got != wantRulesetAll443 {
		t.Fatalf("all-443 ruleset mismatch\n--- got\n%s--- want\n%s", got, wantRulesetAll443)
	}
	preview, _ := NFTPlan{TargetUID: 1000, IPv4: ips(t, "1.1.1.1")}.Ruleset()
	if !strings.Contains(preview, "redirect to :<port>") {
		t.Fatalf("preview without a port must show a placeholder:\n%s", preview)
	}
}

func TestUpdateScriptGolden(t *testing.T) {
	p := NFTPlan{TargetUID: 1000, RedirectPort: 8443, IPv4: ips(t, "93.184.216.34", "1.1.1.1")}
	want := "flush set inet acd_ctx mitm_ipv4\nadd element inet acd_ctx mitm_ipv4 { 1.1.1.1, 93.184.216.34 }\n"
	if got := p.UpdateScript(); got != want {
		t.Fatalf("update script = %q", got)
	}
	empty := NFTPlan{TargetUID: 1000, RedirectPort: 8443}
	if _, err := empty.Ruleset(); err == nil {
		t.Fatal("a set plan without addresses must not render")
	}
	if got := (NFTPlan{TargetUID: 1000, AllDestinations: true}).UpdateScript(); got != "" {
		t.Fatalf("all-443 has no set to update, got %q", got)
	}
}

func TestPlanValidation(t *testing.T) {
	if err := (NFTPlan{TargetUID: 0, ProxyUID: 0, IPv4: ips(t, "1.1.1.1")}).Validate(); err == nil {
		t.Fatal("target equal to proxy uid must be refused")
	}
	if err := (NFTPlan{TargetUID: -1, ProxyUID: 0, IPv4: ips(t, "1.1.1.1")}).Validate(); err == nil {
		t.Fatal("negative uid must be refused")
	}
	if err := (NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 70000, IPv4: ips(t, "1.1.1.1")}).Validate(); err == nil {
		t.Fatal("bad port must be refused")
	}
}

func TestCanApplyTransparent(t *testing.T) {
	withCap := "Name:\tacd\nCapEff:\t0000000000001000\n" // bit 12 = CAP_NET_ADMIN
	noCap := "Name:\tacd\nCapEff:\t0000000000000000\n"
	if !CanApplyTransparent(0, "") {
		t.Fatal("root must be allowed")
	}
	if !CanApplyTransparent(1000, withCap) {
		t.Fatal("CAP_NET_ADMIN must be allowed")
	}
	if CanApplyTransparent(1000, noCap) || CanApplyTransparent(1000, "") {
		t.Fatal("unprivileged user must be refused")
	}
}

// fakeNFT models the single table: present or absent, and its ruleset text.
type fakeNFT struct {
	present bool
	text    string
	cmds    []string
	stdins  []string
	// appearOnApply makes an untagged table appear just before the first
	// ruleset is applied: the race between the check and the create.
	appearOnApply bool
	// mu orders the commands of an in-process helper with the test that
	// drives it: closing a pipe carries no happens-before edge.
	mu sync.Mutex
}

// reset clears the recorded commands under the lock.
func (f *fakeNFT) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = nil
}

func (f *fakeNFT) Run(_ context.Context, c Command) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, strings.Join(append([]string{c.Name}, c.Args...), " "))
	if c.Name != "nft" {
		return "", errors.New("unexpected command " + c.Name)
	}
	switch {
	case len(c.Args) == 2 && c.Args[0] == "-f":
		f.stdins = append(f.stdins, c.Stdin)
		if f.appearOnApply {
			f.appearOnApply = false
			f.present = true
			f.text = "table inet acd_ctx {\n\tchain other {\n\t}\n}\n"
		}
		if strings.HasPrefix(c.Stdin, "create table inet acd_ctx\n") {
			// create fails on an existing table, and the whole script is refused.
			if f.present {
				return "", &ExitError{Command: c, Code: 1, Output: "Error: table already exists"}
			}
			f.present = true
			f.text = c.Stdin // the rules carry the owner tag, as the real table does
		}
		return "", nil
	case len(c.Args) == 4 && c.Args[0] == "list":
		if !f.present {
			return "", &ExitError{Command: c, Code: 1, Output: "No such file or directory"}
		}
		return f.text, nil
	case len(c.Args) == 4 && c.Args[0] == "delete":
		f.present = false
		f.text = ""
		return "", nil
	}
	return "", errors.New("unexpected nft args " + strings.Join(c.Args, " "))
}

func linuxEnv(r Runner, euid int) Env {
	e := Env{GOOS: "linux", Runner: r, Geteuid: func() int { return euid },
		ProcStatus: func() string { return "" },
		Alive:      func(int, uint64) bool { return true },
		LookPath:   func(string) bool { return true }}
	e.Spawn = helperSpawner(e)
	return e
}

func TestTransparentStartStopArgv(t *testing.T) {
	dir := t.TempDir()
	f := &fakeNFT{}
	plan := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	a, err := StartTransparent(context.Background(), linuxEnv(f, 0), dir, plan, FirewallNFT)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	want := []string{
		"nft list table inet acd_ctx",
		"nft -f -",
		"nft list table inet acd_ctx",
	}
	if strings.Join(f.cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("start argv = %q", f.cmds)
	}
	if f.stdins[0] != mustRuleset(t, plan) {
		t.Fatal("applied ruleset differs from the rendered one")
	}
	if err := a.SetDestinations(context.Background(), ips(t, "2.2.2.2"), nil); err != nil {
		t.Fatal(err)
	}
	if got := f.stdins[len(f.stdins)-1]; got != "flush set inet acd_ctx mitm_ipv4\nadd element inet acd_ctx mitm_ipv4 { 2.2.2.2 }\n" {
		t.Fatalf("set update script = %q", got)
	}
	f.cmds = nil
	notes, err := a.Stop(RestoreContext2(t))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got, want := strings.Join(f.cmds, "|"), "nft list table inet acd_ctx|nft delete table inet acd_ctx|nft list table inet acd_ctx"; got != want {
		t.Fatalf("stop argv = %q", got)
	}
	if f.present || len(notes) == 0 {
		t.Fatalf("table not removed: present=%v notes=%v", f.present, notes)
	}
	if paths, _ := listStates(dir); len(paths) != 0 {
		t.Fatalf("state file kept: %v", paths)
	}
}

func mustRuleset(t *testing.T, p NFTPlan) string {
	t.Helper()
	s, err := p.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// RestoreContext2 returns a context that the test cancels.
func RestoreContext2(t *testing.T) context.Context {
	ctx, cancel := RestoreContext()
	t.Cleanup(cancel)
	return ctx
}

func TestTransparentRefusesExistingTable(t *testing.T) {
	dir := t.TempDir()
	f := &fakeNFT{present: true, text: "table inet acd_ctx {\n}\n"}
	plan := NFTPlan{TargetUID: 1000, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	if _, err := StartTransparent(context.Background(), linuxEnv(f, 0), dir, plan, FirewallNFT); err == nil {
		t.Fatal("second transparent session must refuse an existing table")
	}
	for _, c := range f.cmds {
		if strings.Contains(c, "-f") {
			t.Fatalf("an existing table must not be overwritten: %v", f.cmds)
		}
	}
}

// TestForeignTableIsNotDeleted: a table without the owner tag belongs to someone
// else. Restore must not delete it, and must keep the state file for the user.
func TestForeignTableIsNotDeleted(t *testing.T) {
	dir := t.TempDir()
	f := &fakeNFT{}
	plan := NFTPlan{TargetUID: 1000, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	a, err := StartTransparent(context.Background(), linuxEnv(f, 0), dir, plan, FirewallNFT)
	if err != nil {
		t.Fatal(err)
	}
	keepHelperDown(t, a)
	// Someone replaced the table's content: the tag is gone.
	f.text = "table inet acd_ctx {\n\tchain other {\n\t}\n}\n"
	f.cmds = nil
	if _, err := a.Stop(RestoreContext2(t)); err == nil {
		t.Fatal("restore of a foreign table must report an error")
	}
	for _, c := range f.cmds {
		if strings.HasPrefix(c, "nft delete") {
			t.Fatalf("foreign table deleted: %v", f.cmds)
		}
	}
	if !f.present {
		t.Fatal("foreign table removed")
	}
	if paths, _ := listStates(dir); len(paths) != 1 {
		t.Fatalf("state file must stay when restore is refused, got %v", paths)
	}
}

func TestTransparentNeedsPrivilegeAndLinux(t *testing.T) {
	plan := NFTPlan{TargetUID: 1000, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	f := &fakeNFT{}
	_, err := StartTransparent(context.Background(), linuxEnv(f, 1000), t.TempDir(), plan, FirewallNFT)
	var need *ErrNeedPrivilege
	if !errors.As(err, &need) {
		t.Fatalf("unprivileged start: %v", err)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("unprivileged start ran commands: %v", f.cmds)
	}
	e := linuxEnv(f, 0)
	e.GOOS = "darwin"
	if _, err := StartTransparent(context.Background(), e, t.TempDir(), plan, FirewallNFT); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("darwin: %v", err)
	}
}

func TestRestoreRefusesForeignTableName(t *testing.T) {
	if _, err := restoreNFT(context.Background(), &fakeNFT{}, "other_table"); err == nil {
		t.Fatal("restore must refuse a table it did not create")
	}
}
