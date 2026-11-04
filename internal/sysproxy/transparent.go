package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// NFTTable is the nftables table acd-ctx creates. An underscore keeps the name
// a valid unquoted identifier. The owner tag OwnerTag is carried by the rule
// comments, and restore checks for it before it deletes anything.
const NFTTable = "acd_ctx"

// Destination set names, filled from the mitm domains.
const (
	nftSet4 = "mitm_ipv4"
	nftSet6 = "mitm_ipv6"
)

// Redirect tools. nft is preferred; iptables is the fallback when nft is absent.
const (
	FirewallNFT      = "nft"
	FirewallIptables = "iptables"
)

// ErrUnsupported means the degree is not available on this platform.
var ErrUnsupported = errors.New("not supported on this platform")

// ErrNoFirewall means neither nft nor iptables is installed.
var ErrNoFirewall = errors.New("transparent mode needs nft or iptables; neither was found")

// NFTPlan is the redirect of one user's TCP 443 traffic to the local listeners.
// Traffic of ProxyUID is accepted before any redirect, so the proxy's own
// upstream connections never loop back. The same plan renders the nft ruleset
// and the iptables/ip6tables chain.
type NFTPlan struct {
	TargetUID       int
	ProxyUID        int
	RedirectPort    int // 0 renders a placeholder in previews
	AllDestinations bool
	IPv4            []netip.Addr
	IPv6            []netip.Addr
	// RedirectIPv6 adds the IPv6 rules. The caller must have bound [::1] on
	// RedirectPort, or the redirected IPv6 connections would be refused.
	RedirectIPv6 bool
}

// Validate checks the plan before rules are rendered or applied.
func (p NFTPlan) Validate() error {
	if p.TargetUID < 0 || p.ProxyUID < 0 {
		return errors.New("sysproxy: uid must not be negative")
	}
	if p.TargetUID == p.ProxyUID {
		return fmt.Errorf("sysproxy: target uid %d is the proxy's own uid; choose the uid of the client", p.TargetUID)
	}
	if p.RedirectPort < 0 || p.RedirectPort > 65535 {
		return fmt.Errorf("sysproxy: invalid redirect port %d", p.RedirectPort)
	}
	if !p.AllDestinations && len(p.IPv4) == 0 {
		return errors.New("sysproxy: no IPv4 address resolved for the MITM domains")
	}
	return nil
}

// sortedAddrs returns the unique addresses of one family in p, sorted, as text.
// Loopback, unspecified and multicast destinations are never redirected: the
// router refuses them, and a poisoned DNS answer must not turn the rule into a
// way to cut the client off from local services.
func sortedAddrs(addrs []netip.Addr, v6 bool) []string {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if (v6 && !a.Is6()) || (!v6 && !a.Is4()) {
			continue
		}
		if a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	s := make([]string, len(out))
	for i, a := range out {
		s[i] = a.String()
	}
	return s
}

func (p NFTPlan) redirectTarget() string {
	if p.RedirectPort == 0 {
		return "<port>"
	}
	return strconv.Itoa(p.RedirectPort)
}

// Ruleset renders the nft script that creates the table. It is applied with
// `nft -f -`, which is atomic. The first line is `create table`, which fails
// when the table already exists: a table that appeared after the check is
// refused, never merged into. The table is then flushed (a no-op on a new
// table) and filled.
func (p NFTPlan) Ruleset() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "create table inet %s\n", NFTTable)
	fmt.Fprintf(&b, "flush table inet %s\n", NFTTable)
	fmt.Fprintf(&b, "table inet %s {\n", NFTTable)
	if !p.AllDestinations {
		fmt.Fprintf(&b, "\tset %s {\n\t\ttype ipv4_addr\n", nftSet4)
		if ips := sortedAddrs(p.IPv4, false); len(ips) > 0 {
			fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(ips, ", "))
		}
		b.WriteString("\t}\n")
		if p.RedirectIPv6 {
			fmt.Fprintf(&b, "\tset %s {\n\t\ttype ipv6_addr\n", nftSet6)
			if ips := sortedAddrs(p.IPv6, true); len(ips) > 0 {
				fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(ips, ", "))
			}
			b.WriteString("\t}\n")
		}
	}
	b.WriteString("\n\tchain output {\n")
	b.WriteString("\t\ttype nat hook output priority -100; policy accept;\n")
	fmt.Fprintf(&b, "\t\tmeta skuid %d accept comment %q\n", p.ProxyUID, OwnerTag)
	// Local services on 127.0.0.0/8 and ::1 are never redirected: the router
	// would refuse them, which would break the client's own localhost HTTPS.
	fmt.Fprintf(&b, "\t\tip daddr 127.0.0.0/8 accept comment %q\n", OwnerTag)
	if p.RedirectIPv6 {
		fmt.Fprintf(&b, "\t\tip6 daddr ::1 accept comment %q\n", OwnerTag)
	}
	port := p.redirectTarget()
	if p.AllDestinations {
		fmt.Fprintf(&b, "\t\tmeta skuid %d meta nfproto ipv4 meta l4proto tcp tcp dport 443 redirect to :%s comment %q\n",
			p.TargetUID, port, OwnerTag)
		if p.RedirectIPv6 {
			fmt.Fprintf(&b, "\t\tmeta skuid %d meta nfproto ipv6 meta l4proto tcp tcp dport 443 redirect to :%s comment %q\n",
				p.TargetUID, port, OwnerTag)
		}
	} else {
		fmt.Fprintf(&b, "\t\tmeta skuid %d meta l4proto tcp tcp dport 443 ip daddr @%s redirect to :%s comment %q\n",
			p.TargetUID, nftSet4, port, OwnerTag)
		if p.RedirectIPv6 {
			fmt.Fprintf(&b, "\t\tmeta skuid %d meta l4proto tcp tcp dport 443 ip6 daddr @%s redirect to :%s comment %q\n",
				p.TargetUID, nftSet6, port, OwnerTag)
		}
	}
	b.WriteString("\t}\n}\n")
	return b.String(), nil
}

// UpdateScript renders the nft script that replaces the destination sets with
// the plan's addresses. It returns "" when there are no sets.
func (p NFTPlan) UpdateScript() string {
	if p.AllDestinations {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "flush set inet %s %s\n", NFTTable, nftSet4)
	if ips := sortedAddrs(p.IPv4, false); len(ips) > 0 {
		fmt.Fprintf(&b, "add element inet %s %s { %s }\n", NFTTable, nftSet4, strings.Join(ips, ", "))
	}
	if p.RedirectIPv6 {
		fmt.Fprintf(&b, "flush set inet %s %s\n", NFTTable, nftSet6)
		if ips := sortedAddrs(p.IPv6, true); len(ips) > 0 {
			fmt.Fprintf(&b, "add element inet %s %s { %s }\n", NFTTable, nftSet6, strings.Join(ips, ", "))
		}
	}
	return b.String()
}

// CanApplyTransparent reports whether the process may change firewall rules:
// it is root, or it holds CAP_NET_ADMIN (bit 12 of CapEff in /proc/self/status).
func CanApplyTransparent(euid int, status string) bool {
	if euid == 0 {
		return true
	}
	for _, line := range strings.Split(status, "\n") {
		if hex, ok := strings.CutPrefix(line, "CapEff:"); ok {
			v, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 64)
			return err == nil && v&(1<<12) != 0
		}
	}
	return false
}

// Privileged reports whether this process may install transparent rules.
func (e Env) Privileged() bool { return CanApplyTransparent(e.euid(), e.procStatus()) }

// Euid returns the effective uid, or -1 when the Env does not know it.
func (e Env) Euid() int { return e.euid() }

// hasCommand reports whether a command is installed. A nil LookPath reports none.
func hasCommand(e Env, name string) bool {
	return e.LookPath != nil && e.LookPath(name)
}

func (e Env) procStatus() string {
	if e.ProcStatus != nil {
		return e.ProcStatus()
	}
	data, err := os.ReadFile(filepath.Join(e.procRoot(), "self", "status"))
	if err != nil {
		return ""
	}
	return string(data)
}

// ChooseFirewall returns the redirect tool for this machine: nft when it is
// installed, otherwise iptables.
func ChooseFirewall(e Env) (string, error) {
	switch {
	case hasCommand(e, "nft"):
		return FirewallNFT, nil
	case hasCommand(e, "iptables"):
		return FirewallIptables, nil
	}
	return "", ErrNoFirewall
}

// ResolveDestinations resolves hosts to unique IPv4 and IPv6 addresses. A host
// that fails is reported in the error, and the addresses of the others are
// still returned.
func ResolveDestinations(ctx context.Context, e Env, hosts []string) (v4, v6 []netip.Addr, err error) {
	seen := map[netip.Addr]bool{}
	var errs []error
	for _, h := range hosts {
		addrs, rerr := e.resolve(ctx, h)
		if rerr != nil {
			errs = append(errs, fmt.Errorf("resolve %s: %w", h, rerr))
			continue
		}
		for _, a := range addrs {
			a = a.Unmap()
			if seen[a] || !(a.Is4() || a.Is6()) {
				continue
			}
			seen[a] = true
			if a.Is4() {
				v4 = append(v4, a)
			} else {
				v6 = append(v6, a)
			}
		}
	}
	slices.SortFunc(v4, func(a, b netip.Addr) int { return a.Compare(b) })
	slices.SortFunc(v6, func(a, b netip.Addr) int { return a.Compare(b) })
	return v4, v6, errors.Join(errs...)
}

// ResolveIPv4 is ResolveDestinations for the IPv4 addresses only.
func ResolveIPv4(ctx context.Context, e Env, hosts []string) ([]netip.Addr, error) {
	v4, _, err := ResolveDestinations(ctx, e, hosts)
	return v4, err
}

// resolve asks the injected resolver, or DNS. Both families come back; the
// callers keep the one they need.
func (e Env) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if e.Resolve != nil {
		return e.Resolve(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// TransparentPreview renders what StartTransparent would install with fw, for
// a dry run. Nothing is changed.
func TransparentPreview(fw string, p NFTPlan) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if fw != FirewallIptables {
		return p.Ruleset()
	}
	var b strings.Builder
	for _, v6 := range []bool{false, true} {
		if v6 && !p.RedirectIPv6 {
			continue
		}
		for _, c := range p.iptScript(v6) {
			b.WriteString(c.String())
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// StartTransparent installs the redirect with fw (FirewallNFT or
// FirewallIptables). It refuses when the table or chain already exists, so a
// second run cannot take over the first one's rules, and it starts the
// watchdog before the first change.
func StartTransparent(ctx context.Context, e Env, dir string, p NFTPlan, fw string) (*Active, error) {
	if e.goos() != "linux" {
		return nil, fmt.Errorf("transparent mode: %w (Linux only)", ErrUnsupported)
	}
	if !e.Privileged() {
		return nil, &ErrNeedPrivilege{}
	}
	r, err := e.runner()
	if err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.RedirectPort == 0 {
		return nil, errors.New("sysproxy: redirect port must be set before applying")
	}
	switch fw {
	case FirewallNFT:
		return startNFT(ctx, e, r, dir, p)
	case FirewallIptables:
		return startIptables(ctx, e, r, dir, p)
	}
	return nil, fmt.Errorf("sysproxy: unknown redirect tool %q", fw)
}

// SetDestinations replaces the destination sets. An empty family keeps
// the addresses it had: a failed lookup must not empty the set. It is a no-op
// for the all-443 plan.
func (a *Active) SetDestinations(ctx context.Context, v4, v6 []netip.Addr) error {
	if a.mode != ModeTransparent {
		return errors.New("sysproxy: SetDestinations needs a transparent session")
	}
	if a.plan.AllDestinations {
		return nil
	}
	if len(v4) > 0 {
		a.plan.IPv4 = v4
	}
	if len(v6) > 0 {
		a.plan.IPv6 = v6
	}
	r, err := a.env.runner()
	if err != nil {
		return err
	}
	switch a.fw {
	case FirewallNFT:
		script := a.plan.UpdateScript()
		if script == "" {
			return nil
		}
		_, err = r.Run(ctx, Command{Name: "nft", Args: []string{"-f", "-"}, Stdin: script})
		return err
	case FirewallIptables:
		return a.refillIptables(ctx, r)
	}
	return fmt.Errorf("sysproxy: unknown redirect tool %q", a.fw)
}

func nftList() Command {
	return Command{Name: "nft", Args: []string{"list", "table", "inet", NFTTable}}
}

// restoreNFT deletes the table only when its rules carry the owner tag. A table
// without the tag belongs to someone else and is left alone.
func restoreNFT(ctx context.Context, r Runner, table string) ([]string, error) {
	if table != NFTTable {
		return nil, fmt.Errorf("sysproxy: refusing to touch table %q", table)
	}
	out, err := r.Run(ctx, nftList())
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			return []string{"nft table already absent"}, nil
		}
		return nil, err
	}
	if !strings.Contains(out, fmt.Sprintf("comment %q", OwnerTag)) {
		return nil, fmt.Errorf("table inet %s has no %q tag; not deleted", table, OwnerTag)
	}
	if _, err := r.Run(ctx, Command{Name: "nft", Args: []string{"delete", "table", "inet", table}}); err != nil {
		return nil, err
	}
	if _, err := r.Run(ctx, nftList()); err == nil {
		return nil, fmt.Errorf("table inet %s still present after delete", table)
	}
	return []string{"removed nft table inet " + table}, nil
}

// nftTableExists reports whether the acd-ctx table is present, read-only.
func nftTableExists(ctx context.Context, r Runner) (bool, error) {
	_, err := r.Run(ctx, nftList())
	if err == nil {
		return true, nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return false, err
}

// startNFT applies the ruleset. nft applies a script atomically, so a failed
// apply changed nothing and the state file is simply dropped.
func startNFT(ctx context.Context, e Env, r Runner, dir string, p NFTPlan) (*Active, error) {
	ruleset, err := p.Ruleset()
	if err != nil {
		return nil, err
	}
	exists, err := nftTableExists(ctx, r)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("sysproxy: table inet %s already exists; run acd-ctx attach --restore first", NFTTable)
	}
	st := newTransparentState(e, FirewallNFT)
	st.NFTTable = NFTTable
	a, err := beginTransparent(e, dir, st, p, FirewallNFT)
	if err != nil {
		return nil, err
	}
	if _, err := r.Run(ctx, Command{Name: "nft", Args: []string{"-f", "-"}, Stdin: ruleset}); err != nil {
		cause := fmt.Errorf("apply nft ruleset: %w", err)
		if ctx.Err() != nil {
			// A canceled apply may have landed: restore (a no-op when absent).
			return nil, a.rollback(cause)
		}
		return nil, a.unapplied(cause)
	}
	out, err := r.Run(ctx, nftList())
	if err != nil {
		return nil, a.rollback(fmt.Errorf("verify nft table: %w", err))
	}
	if !strings.Contains(out, fmt.Sprintf("comment %q", OwnerTag)) {
		return nil, a.rollback(fmt.Errorf("table inet %s is not ours: it has no %q tag", NFTTable, OwnerTag))
	}
	return a, nil
}
