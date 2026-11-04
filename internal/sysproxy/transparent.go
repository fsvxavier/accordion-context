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

// nftSet is the destination set, filled from the mitm domains.
const nftSet = "mitm_ipv4"

// ErrUnsupported means the degree is not available on this platform.
var ErrUnsupported = errors.New("not supported on this platform")

// NFTPlan is the redirect of one user's TCP 443 traffic to the local listener.
// Traffic of ProxyUID is accepted before any redirect, so the proxy's own
// upstream connections never loop back.
type NFTPlan struct {
	TargetUID       int
	ProxyUID        int
	RedirectPort    int // 0 renders a placeholder in previews
	AllDestinations bool
	IPv4            []netip.Addr
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

// sortedIPv4 returns the unique IPv4 addresses in p, sorted.
func (p NFTPlan) sortedIPv4() []string {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, a := range p.IPv4 {
		a = a.Unmap()
		if a.Is4() && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
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
// `nft -f -`, which is atomic.
func (p NFTPlan) Ruleset() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", NFTTable)
	if !p.AllDestinations {
		fmt.Fprintf(&b, "\tset %s {\n\t\ttype ipv4_addr\n", nftSet)
		if ips := p.sortedIPv4(); len(ips) > 0 {
			fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(ips, ", "))
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("\n\tchain output {\n")
	b.WriteString("\t\ttype nat hook output priority -100; policy accept;\n")
	fmt.Fprintf(&b, "\t\tmeta skuid %d accept comment %q\n", p.ProxyUID, OwnerTag)
	// The listener is IPv4 only, so IPv6 must never match the redirect. The set
	// variant is IPv4 by its ip daddr match; the all-443 variant needs nfproto.
	if p.AllDestinations {
		fmt.Fprintf(&b, "\t\tmeta skuid %d meta nfproto ipv4 meta l4proto tcp tcp dport 443 redirect to :%s comment %q\n",
			p.TargetUID, p.redirectTarget(), OwnerTag)
	} else {
		fmt.Fprintf(&b, "\t\tmeta skuid %d meta l4proto tcp tcp dport 443 ip daddr @%s redirect to :%s comment %q\n",
			p.TargetUID, nftSet, p.redirectTarget(), OwnerTag)
	}
	b.WriteString("\t}\n}\n")
	return b.String(), nil
}

// UpdateScript renders the nft script that replaces the destination set with
// the given addresses. It returns "" when there is no set.
func (p NFTPlan) UpdateScript() string {
	if p.AllDestinations {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "flush set inet %s %s\n", NFTTable, nftSet)
	if ips := p.sortedIPv4(); len(ips) > 0 {
		fmt.Fprintf(&b, "add element inet %s %s { %s }\n", NFTTable, nftSet, strings.Join(ips, ", "))
	}
	return b.String()
}

// CanApplyTransparent reports whether the process may change nftables rules:
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

// ResolveIPv4 resolves hosts to unique IPv4 addresses. A host that fails is
// reported in the error, and the addresses of the others are still returned.
func ResolveIPv4(ctx context.Context, e Env, hosts []string) ([]netip.Addr, error) {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	var errs []error
	for _, h := range hosts {
		addrs, err := e.resolve(ctx, h)
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve %s: %w", h, err))
			continue
		}
		for _, a := range addrs {
			a = a.Unmap()
			if a.Is4() && !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return out, errors.Join(errs...)
}

func (e Env) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if e.Resolve != nil {
		return e.Resolve(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
}

// StartTransparent installs the redirect. It refuses when the table already
// exists, so a second run cannot overwrite the first one's rules.
func StartTransparent(ctx context.Context, e Env, dir string, p NFTPlan) (*Active, error) {
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
	ruleset, err := p.Ruleset()
	if err != nil {
		return nil, err
	}
	if p.RedirectPort == 0 {
		return nil, errors.New("sysproxy: redirect port must be set before applying")
	}
	if _, err := r.Run(ctx, nftList()); err == nil {
		return nil, fmt.Errorf("sysproxy: table inet %s already exists; run acd-ctx attach --restore first", NFTTable)
	} else {
		var ee *ExitError
		if !errors.As(err, &ee) {
			return nil, err
		}
	}
	pid := os.Getpid()
	st := &State{
		Mode: ModeTransparent, NFTTable: NFTTable, PID: pid,
		StartTime: startTime(e.procRoot(), pid), Created: e.now(),
	}
	path, err := createState(dir, st)
	if err != nil {
		return nil, err
	}
	a := &Active{env: e, mode: ModeTransparent, state: st, path: path, plan: p}
	if _, err := r.Run(ctx, Command{Name: "nft", Args: []string{"-f", "-"}, Stdin: ruleset}); err != nil {
		return nil, a.rollback(fmt.Errorf("apply nft ruleset: %w", err))
	}
	if _, err := r.Run(ctx, nftList()); err != nil {
		return nil, a.rollback(fmt.Errorf("verify nft table: %w", err))
	}
	return a, nil
}

// SetIPv4 replaces the destination set. It is a no-op for the all-443 plan.
func (a *Active) SetIPv4(ctx context.Context, addrs []netip.Addr) error {
	if a.mode != ModeTransparent {
		return errors.New("sysproxy: SetIPv4 needs a transparent session")
	}
	r, err := a.env.runner()
	if err != nil {
		return err
	}
	p := a.plan
	p.IPv4 = addrs
	script := p.UpdateScript()
	if script == "" {
		return nil
	}
	_, err = r.Run(ctx, Command{Name: "nft", Args: []string{"-f", "-"}, Stdin: script})
	return err
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
