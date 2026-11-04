package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeIpt models iptables and ip6tables in the nat table: user chains with their
// rules, and the jump rules in OUTPUT. It answers the commands acd-ctx sends,
// with the exit codes iptables uses (1 for a missing chain or rule).
type fakeIpt struct {
	chains map[string][]string // "<bin> <chain>" -> rules, as -S lines
	jumps  map[string][]string // bin -> OUTPUT rules, as argument strings
	cmds   []string
}

func newFakeIpt() *fakeIpt {
	return &fakeIpt{chains: map[string][]string{}, jumps: map[string][]string{}}
}

// quoteComment renders the comment argument the way iptables -S lists it.
func quoteComment(args []string) string {
	s := strings.Join(args, " ")
	return strings.Replace(s, "--comment "+OwnerTag, `--comment "`+OwnerTag+`"`, -1)
}

func (f *fakeIpt) Run(_ context.Context, c Command) (string, error) {
	f.cmds = append(f.cmds, c.String())
	if c.Name != "iptables" && c.Name != "ip6tables" {
		return "", errors.New("unexpected command " + c.Name)
	}
	a := c.Args
	if len(a) < 5 || a[0] != "-w" || a[2] != "-t" || a[3] != "nat" {
		return "", errors.New("unexpected argv " + c.String())
	}
	op, rest := a[4], a[5:]
	miss := &ExitError{Command: c, Code: 1, Output: "No chain/target/match by that name."}
	key := func(chain string) string { return c.Name + " " + chain }
	switch op {
	case "-S":
		rules, ok := f.chains[key(rest[0])]
		if !ok {
			return "", miss
		}
		out := "-N " + rest[0] + "\n"
		for _, r := range rules {
			out += r + "\n"
		}
		return out, nil
	case "-N":
		if _, ok := f.chains[key(rest[0])]; ok {
			return "", &ExitError{Command: c, Code: 1, Output: "Chain already exists."}
		}
		f.chains[key(rest[0])] = nil
	case "-A":
		k := key(rest[0])
		if _, ok := f.chains[k]; !ok {
			return "", miss
		}
		f.chains[k] = append(f.chains[k], "-A "+rest[0]+" "+quoteComment(rest[1:]))
	case "-F":
		k := key(rest[0])
		if _, ok := f.chains[k]; !ok {
			return "", miss
		}
		f.chains[k] = nil
	case "-X":
		k := key(rest[0])
		if _, ok := f.chains[k]; !ok {
			return "", miss
		}
		delete(f.chains, k)
	case "-I":
		// rest: OUTPUT <pos> <rule...>
		f.jumps[c.Name] = append([]string{strings.Join(rest[2:], " ")}, f.jumps[c.Name]...)
	case "-C", "-D":
		rule := strings.Join(rest[1:], " ")
		idx := -1
		for i, j := range f.jumps[c.Name] {
			if j == rule {
				idx = i
				break
			}
		}
		if idx < 0 {
			return "", miss
		}
		if op == "-D" {
			f.jumps[c.Name] = append(f.jumps[c.Name][:idx], f.jumps[c.Name][idx+1:]...)
		}
	default:
		return "", fmt.Errorf("fake: unknown op %s", op)
	}
	return "", nil
}

// stateOf is the set of chains and jumps, for assertions.
func (f *fakeIpt) present(bin string) bool {
	_, ok := f.chains[bin+" "+iptChain]
	return ok || len(f.jumps[bin]) > 0
}

func iptPlan(t *testing.T, all bool, v6 bool) NFTPlan {
	p := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, AllDestinations: all, RedirectIPv6: v6}
	if !all {
		p.IPv4 = ips(t, "93.184.216.34", "1.1.1.1")
		if v6 {
			p.IPv6 = ips(t, "2001:db8::2", "2001:db8::1")
		}
	}
	return p
}

// wantIptScriptSet is the exact argv of the IPv4 chain, one command per line,
// for the set mode.
const wantIptScriptSet = `iptables -w 5 -t nat -N ACD_CTX_OUT
iptables -w 5 -t nat -A ACD_CTX_OUT -m owner --uid-owner 0 -m comment --comment acd-ctx -j RETURN
iptables -w 5 -t nat -A ACD_CTX_OUT -d 127.0.0.0/8 -m comment --comment acd-ctx -j RETURN
iptables -w 5 -t nat -A ACD_CTX_OUT -d 1.1.1.1/32 -p tcp --dport 443 -m comment --comment acd-ctx -j REDIRECT --to-ports 8443
iptables -w 5 -t nat -A ACD_CTX_OUT -d 93.184.216.34/32 -p tcp --dport 443 -m comment --comment acd-ctx -j REDIRECT --to-ports 8443
iptables -w 5 -t nat -I OUTPUT 1 -m owner --uid-owner 1000 -m comment --comment acd-ctx -j ACD_CTX_OUT
`

// wantIptScriptAll is the IPv6 chain of the all-443 mode: the loopback return
// and one redirect for every destination of the uid.
const wantIptScriptAll6 = `ip6tables -w 5 -t nat -N ACD_CTX_OUT
ip6tables -w 5 -t nat -A ACD_CTX_OUT -m owner --uid-owner 0 -m comment --comment acd-ctx -j RETURN
ip6tables -w 5 -t nat -A ACD_CTX_OUT -d ::1/128 -m comment --comment acd-ctx -j RETURN
ip6tables -w 5 -t nat -A ACD_CTX_OUT -p tcp --dport 443 -m comment --comment acd-ctx -j REDIRECT --to-ports 8443
ip6tables -w 5 -t nat -I OUTPUT 1 -m owner --uid-owner 1000 -m comment --comment acd-ctx -j ACD_CTX_OUT
`

func scriptText(cs []Command) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.String())
		b.WriteByte('\n')
	}
	return b.String()
}

func TestIptablesScriptGolden(t *testing.T) {
	if got := scriptText(iptPlan(t, false, false).iptScript(false)); got != wantIptScriptSet {
		t.Fatalf("set-mode argv:\n%s--- want\n%s", got, wantIptScriptSet)
	}
	if got := scriptText(iptPlan(t, true, true).iptScript(true)); got != wantIptScriptAll6 {
		t.Fatalf("all-443 ip6 argv:\n%s--- want\n%s", got, wantIptScriptAll6)
	}
}

func TestIptablesIPv6SetModeUsesAAAAOnly(t *testing.T) {
	p := iptPlan(t, false, true)
	got := scriptText(p.iptScript(true))
	for _, want := range []string{"-d 2001:db8::1/128", "-d 2001:db8::2/128"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ip6 chain misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "1.1.1.1") {
		t.Fatalf("an IPv4 address leaked into the ip6 chain:\n%s", got)
	}
}

// TestIptablesStartStopArgv: the restore sequence is -S (ownership), -C then
// -D for the jump, -F, -X, and a final -S that must fail.
func TestIptablesStartStopArgv(t *testing.T) {
	f := newFakeIpt()
	e := linuxEnv(f, 0)
	dir := t.TempDir()
	a, err := StartTransparent(context.Background(), e, dir, iptPlan(t, false, false), FirewallIptables)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !f.present("iptables") {
		t.Fatal("chain and jump not installed")
	}
	f.cmds = nil
	if _, err := a.Stop(RestoreContext2(t)); err != nil {
		t.Fatalf("stop: %v", err)
	}
	want := []string{
		"iptables -w 5 -t nat -S ACD_CTX_OUT",
		"iptables -w 5 -t nat -C OUTPUT -m owner --uid-owner 1000 -m comment --comment acd-ctx -j ACD_CTX_OUT",
		"iptables -w 5 -t nat -D OUTPUT -m owner --uid-owner 1000 -m comment --comment acd-ctx -j ACD_CTX_OUT",
		"iptables -w 5 -t nat -C OUTPUT -m owner --uid-owner 1000 -m comment --comment acd-ctx -j ACD_CTX_OUT",
		"iptables -w 5 -t nat -F ACD_CTX_OUT",
		"iptables -w 5 -t nat -X ACD_CTX_OUT",
		"iptables -w 5 -t nat -S ACD_CTX_OUT",
	}
	if got := strings.Join(f.cmds, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("stop argv:\n%s\n--- want\n%s", got, strings.Join(want, "\n"))
	}
	if f.present("iptables") {
		t.Fatal("chain or jump left behind")
	}
	if left, _ := listStates(dir); len(left) != 0 {
		t.Fatalf("state kept: %v", left)
	}
}

// Both families: the IPv6 chain is installed and removed with the IPv4 one.
func TestIptablesDualStackStartAndRestore(t *testing.T) {
	f := newFakeIpt()
	e := linuxEnv(f, 0)
	a, err := StartTransparent(context.Background(), e, t.TempDir(), iptPlan(t, false, true), FirewallIptables)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !f.present("iptables") || !f.present("ip6tables") {
		t.Fatalf("both families must be installed: %v %v", f.chains, f.jumps)
	}
	if _, err := a.Stop(RestoreContext2(t)); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if f.present("iptables") || f.present("ip6tables") {
		t.Fatalf("leftovers after restore: %v %v", f.chains, f.jumps)
	}
}

// A chain with a rule that does not carry the owner tag belongs to someone else.
// The restore refuses before it removes anything, and the state file stays.
func TestIptablesRestoreRefusesForeignChain(t *testing.T) {
	f := newFakeIpt()
	e := linuxEnv(f, 0)
	dir := t.TempDir()
	a, err := StartTransparent(context.Background(), e, dir, iptPlan(t, false, false), FirewallIptables)
	if err != nil {
		t.Fatal(err)
	}
	keepHelperDown(t, a)
	f.chains["iptables "+iptChain] = append(f.chains["iptables "+iptChain], "-A ACD_CTX_OUT -p tcp -j ACCEPT")
	f.cmds = nil
	if _, err := a.Stop(RestoreContext2(t)); err == nil {
		t.Fatal("restore of a foreign chain must report an error")
	}
	for _, c := range f.cmds {
		if strings.Contains(c, " -D ") || strings.Contains(c, " -F ") || strings.Contains(c, " -X ") {
			t.Fatalf("foreign chain touched: %v", f.cmds)
		}
	}
	if !f.present("iptables") {
		t.Fatal("foreign chain removed")
	}
	if left, _ := listStates(dir); len(left) != 1 {
		t.Fatalf("state must stay when restore is refused: %v", left)
	}
}

// Running the restore twice is safe: the second run finds nothing and changes nothing.
func TestIptablesRestoreIsIdempotent(t *testing.T) {
	f := newFakeIpt()
	e := linuxEnv(f, 0)
	st := newTransparentState(e, FirewallIptables)
	st.Chain, st.UID = iptChain, 1000
	if _, err := beginTransparent(e, t.TempDir(), st, iptPlan(t, false, false), FirewallIptables); err != nil {
		t.Fatal(err)
	}
	for _, c := range iptPlan(t, false, false).iptScript(false) {
		if _, err := f.Run(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := restoreIptables(context.Background(), f, st); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	f.cmds = nil
	notes, err := restoreIptables(context.Background(), f, st)
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "already absent") {
		t.Fatalf("second restore notes = %v", notes)
	}
	if len(f.cmds) != 1 {
		t.Fatalf("second restore ran more than the probe: %v", f.cmds)
	}
}

// A chain that already exists is refused before anything changes.
func TestIptablesStartRefusesExistingChain(t *testing.T) {
	f := newFakeIpt()
	f.chains["iptables "+iptChain] = nil
	e := linuxEnv(f, 0)
	if _, err := StartTransparent(context.Background(), e, t.TempDir(), iptPlan(t, false, false), FirewallIptables); err == nil {
		t.Fatal("an existing chain must refuse the start")
	}
	for _, c := range f.cmds {
		if strings.Contains(c, " -N ") || strings.Contains(c, " -I ") {
			t.Fatalf("existing chain changed: %v", f.cmds)
		}
	}
}

// SetDestinations rewrites the set-mode chain with the new addresses: flush,
// then the same rules, in order.
func TestIptablesSetDestinationsRefills(t *testing.T) {
	f := newFakeIpt()
	e := linuxEnv(f, 0)
	a, err := StartTransparent(context.Background(), e, t.TempDir(), iptPlan(t, false, false), FirewallIptables)
	if err != nil {
		t.Fatal(err)
	}
	f.cmds = nil
	if err := a.SetDestinations(context.Background(), ips(t, "2.2.2.2"), nil); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.cmds, "\n")
	if !strings.HasPrefix(got, "iptables -w 5 -t nat -F ACD_CTX_OUT\n") || !strings.Contains(got, "-d 2.2.2.2/32") {
		t.Fatalf("refill argv:\n%s", got)
	}
	if strings.Contains(got, "1.1.1.1") {
		t.Fatalf("old address kept:\n%s", got)
	}
	_, _ = a.Stop(RestoreContext2(t))
}

func TestIptOwnedParsing(t *testing.T) {
	ok := []string{
		"-N ACD_CTX_OUT\n-A ACD_CTX_OUT -m owner --uid-owner 0 -m comment --comment \"acd-ctx\" -j RETURN\n",
		"-N ACD_CTX_OUT\n",
		"-N ACD_CTX_OUT\n-A ACD_CTX_OUT -d 1.1.1.1/32 -p tcp --dport 443 -m comment --comment \"acd-ctx\" -j REDIRECT --to-ports 8443\n",
	}
	for _, l := range ok {
		if !iptOwned(l) {
			t.Fatalf("owned listing refused:\n%s", l)
		}
	}
	bad := []string{
		"", // no chain line
		"-N ACD_CTX_OUT\n-A ACD_CTX_OUT -p tcp -j ACCEPT\n",
		"-N ACD_CTX_OUT\n-A ACD_CTX_OUT -m comment --comment \"acd-ctx-evil\" -j RETURN\n",
		"-N ACD_CTX_OUT\n-P ACD_CTX_OUT ACCEPT\n",
		"-N ACD_CTX_OUT\n-A ACD_CTX_OUTX -m comment --comment \"acd-ctx\" -j RETURN\n",
	}
	for _, l := range bad {
		if iptOwned(l) {
			t.Fatalf("foreign listing accepted:\n%s", l)
		}
	}
}

func TestChooseFirewallOrder(t *testing.T) {
	has := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		name string
		env  Env
		want string
		err  bool
	}{
		{"nft and iptables: nft first", Env{LookPath: has("nft", "iptables")}, FirewallNFT, false},
		{"only iptables", Env{LookPath: has("iptables")}, FirewallIptables, false},
		{"nothing", Env{LookPath: has()}, "", true},
		{"no lookup at all", Env{}, "", true},
	}
	for _, c := range cases {
		got, err := ChooseFirewall(c.env)
		if (err != nil) != c.err || got != c.want {
			t.Fatalf("%s: got %q, %v", c.name, got, err)
		}
		if err != nil && !errors.Is(err, ErrNoFirewall) {
			t.Fatalf("%s: err = %v", c.name, err)
		}
	}
}

// The dry-run preview for iptables lists both families.
func TestIptablesPreviewListsBothFamilies(t *testing.T) {
	text, err := TransparentPreview(FirewallIptables, iptPlan(t, false, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"iptables -w 5 -t nat -N ACD_CTX_OUT", "ip6tables -w 5 -t nat -N ACD_CTX_OUT", "-d 2001:db8::1/128"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview misses %q:\n%s", want, text)
		}
	}
}

// The start refuses an unknown redirect tool before any command runs.
func TestUnknownFirewallRefused(t *testing.T) {
	f := newFakeIpt()
	if _, err := StartTransparent(context.Background(), linuxEnv(f, 0), t.TempDir(), iptPlan(t, false, false), "pf"); err == nil {
		t.Fatal("unknown tool accepted")
	}
	if len(f.cmds) != 0 {
		t.Fatalf("unknown tool ran commands: %v", f.cmds)
	}
}
