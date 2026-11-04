package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// iptChain is the nat chain acd-ctx owns. It is reached from OUTPUT by one jump
// rule for the target uid, and the restore deletes only that jump and this chain.
const iptChain = "ACD_CTX_OUT"

// iptLockWait is the seconds iptables waits for the xtables lock, so a
// concurrent iptables call from another tool does not fail the run.
const iptLockWait = "5"

// maxJumpDeletes bounds the loop that removes duplicate jump rules.
const maxJumpDeletes = 8

// iptTag matches the owner comment in an `iptables -S` listing.
var iptTag = regexp.MustCompile(`--comment "?` + regexp.QuoteMeta(OwnerTag) + `"?( |$)`)

func natModule(v6 bool) string {
	if v6 {
		return "ip6table_nat"
	}
	return "iptable_nat"
}

func iptBin(v6 bool) string {
	if v6 {
		return "ip6tables"
	}
	return "iptables"
}

// iptCmd is one command in the nat table: iptables -w 5 -t nat <op> <args>.
func iptCmd(v6 bool, op string, args ...string) Command {
	a := append([]string{"-w", iptLockWait, "-t", "nat", op}, args...)
	return Command{Name: iptBin(v6), Args: a}
}

// iptJumpRule is the OUTPUT rule that sends the target uid into the chain.
func iptJumpRule(uid int) []string {
	return []string{"-m", "owner", "--uid-owner", strconv.Itoa(uid),
		"-m", "comment", "--comment", OwnerTag, "-j", iptChain}
}

// iptRules returns the rules of the chain of one family, as argument lists after
// "-A <chain>". The order matters: the uid exclusion and the loopback return
// come before any redirect.
func (p NFTPlan) iptRules(v6 bool) [][]string {
	tag := []string{"-m", "comment", "--comment", OwnerTag}
	loop := "127.0.0.0/8"
	if v6 {
		loop = "::1/128"
	}
	to := []string{"-p", "tcp", "--dport", "443"}
	target := []string{"-j", "REDIRECT", "--to-ports", p.redirectTarget()}
	rules := [][]string{
		joinArgs([]string{"-m", "owner", "--uid-owner", strconv.Itoa(p.ProxyUID)}, tag, []string{"-j", "RETURN"}),
		joinArgs([]string{"-d", loop}, tag, []string{"-j", "RETURN"}),
	}
	if p.AllDestinations {
		return append(rules, joinArgs(to, tag, target))
	}
	addrs := p.IPv4
	bits := "/32"
	if v6 {
		addrs, bits = p.IPv6, "/128"
	}
	for _, ip := range sortedAddrs(addrs, v6) {
		rules = append(rules, joinArgs([]string{"-d", ip + bits}, to, tag, target))
	}
	return rules
}

func joinArgs(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// iptScript creates the chain of one family, fills it and jumps to it. The jump
// comes last, so a failure before it leaves no traffic redirected.
func (p NFTPlan) iptScript(v6 bool) []Command {
	out := []Command{iptCmd(v6, "-N", iptChain)}
	for _, r := range p.iptRules(v6) {
		out = append(out, iptCmd(v6, "-A", append([]string{iptChain}, r...)...))
	}
	return append(out, iptCmd(v6, "-I", append([]string{"OUTPUT", "1"}, iptJumpRule(p.TargetUID)...)...))
}

// iptRefill empties the chain and fills it again with the current addresses.
func (p NFTPlan) iptRefill(v6 bool) []Command {
	out := []Command{iptCmd(v6, "-F", iptChain)}
	for _, r := range p.iptRules(v6) {
		out = append(out, iptCmd(v6, "-A", append([]string{iptChain}, r...)...))
	}
	return out
}

// families lists the families a plan installs: IPv4, and IPv6 when it redirects it.
func (p NFTPlan) families() []bool {
	if p.RedirectIPv6 {
		return []bool{false, true}
	}
	return []bool{false}
}

// isExit reports whether err is a command that ran and exited non-zero.
func isExit(err error) bool {
	var ee *ExitError
	return errors.As(err, &ee)
}

// iptOwned reports whether an `iptables -S <chain>` listing is a chain acd-ctx
// created: the chain line is there and every rule carries the owner comment.
// Anything else means someone else changed the chain, and it is not deleted.
func iptOwned(listing string) bool {
	sawChain := false
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case line == "-N "+iptChain:
			sawChain = true
		case strings.HasPrefix(line, "-A "+iptChain+" "):
			if !iptTag.MatchString(line) {
				return false
			}
		default:
			return false
		}
	}
	return sawChain
}

// startIptables installs the redirect with iptables (and ip6tables when the plan
// redirects IPv6). A chain that already exists is refused before any change.
func startIptables(ctx context.Context, e Env, r Runner, dir string, p NFTPlan) (*Active, error) {
	for _, v6 := range p.families() {
		// The nat table must exist for REDIRECT. ip6tables without it (module
		// ip6table_nat) fails here, before any family is half installed.
		if _, err := r.Run(ctx, iptCmd(v6, "-S", "OUTPUT")); err != nil {
			if isExit(err) {
				return nil, fmt.Errorf("sysproxy: %s has no usable nat table (%v); load the %s module (nothing was changed)", iptBin(v6), err, natModule(v6))
			}
			return nil, err
		}
		_, err := r.Run(ctx, iptCmd(v6, "-S", iptChain))
		if err == nil {
			return nil, fmt.Errorf("sysproxy: %s chain %s already exists; run acd-ctx attach --restore first", iptBin(v6), iptChain)
		}
		if !isExit(err) {
			return nil, err
		}
	}
	st := newTransparentState(e, FirewallIptables)
	st.Chain, st.UID, st.IPv6 = iptChain, p.TargetUID, p.RedirectIPv6
	a, err := beginTransparent(e, dir, st, p, FirewallIptables)
	if err != nil {
		return nil, err
	}
	first := true
	for _, v6 := range p.families() {
		for _, c := range p.iptScript(v6) {
			if _, err := r.Run(ctx, c); err != nil {
				cause := fmt.Errorf("apply %s: %w", c, err)
				if first && ctx.Err() == nil {
					// The chain was not created: nothing of ours is in place.
					return nil, a.unapplied(cause)
				}
				return nil, a.rollback(cause)
			}
			first = false
		}
	}
	for _, v6 := range p.families() {
		out, err := r.Run(ctx, iptCmd(v6, "-S", iptChain))
		if err != nil {
			return nil, a.rollback(fmt.Errorf("verify %s chain: %w", iptBin(v6), err))
		}
		if !iptOwned(out) {
			return nil, a.rollback(fmt.Errorf("%s chain %s is not ours: it has rules without the %q tag", iptBin(v6), iptChain, OwnerTag))
		}
		if _, err := r.Run(ctx, iptCmd(v6, "-C", append([]string{"OUTPUT"}, iptJumpRule(p.TargetUID)...)...)); err != nil {
			return nil, a.rollback(fmt.Errorf("verify %s jump: %w", iptBin(v6), err))
		}
	}
	return a, nil
}

// refillIptables rewrites the chains of the families in use with the plan's
// addresses. Traffic to a chain being refilled is not redirected for a moment,
// which is the same as traffic to an address outside the set.
func (a *Active) refillIptables(ctx context.Context, r Runner) error {
	for _, v6 := range a.plan.families() {
		for _, c := range a.plan.iptRefill(v6) {
			if _, err := r.Run(ctx, c); err != nil {
				return fmt.Errorf("refresh %s: %w", c, err)
			}
		}
	}
	return nil
}

// restoreIptables removes the redirect of each family in the state.
func restoreIptables(ctx context.Context, r Runner, st *State) ([]string, error) {
	if st.Chain != iptChain {
		return nil, fmt.Errorf("sysproxy: refusing to touch chain %q", st.Chain)
	}
	fams := []bool{false}
	if st.IPv6 {
		fams = append(fams, true)
	}
	var notes []string
	for _, v6 := range fams {
		n, err := restoreIptFamily(ctx, r, v6, st.UID)
		notes = append(notes, n...)
		if err != nil {
			return notes, err
		}
	}
	return notes, nil
}

// restoreIptFamily deletes the OUTPUT jump, then the chain. The chain is checked
// for the owner tag before anything is deleted, the jump is checked with -C
// before and after each -D, and the chain is verified gone at the end.
func restoreIptFamily(ctx context.Context, r Runner, v6 bool, uid int) ([]string, error) {
	bin := iptBin(v6)
	out, err := r.Run(ctx, iptCmd(v6, "-S", iptChain))
	if err != nil {
		if isExit(err) {
			return []string{bin + " chain " + iptChain + " already absent"}, nil
		}
		return nil, err
	}
	if !iptOwned(out) {
		return nil, fmt.Errorf("%s chain %s has rules without the %q tag; not deleted", bin, iptChain, OwnerTag)
	}
	jump := append([]string{"OUTPUT"}, iptJumpRule(uid)...)
	removed := 0
	for {
		_, err := r.Run(ctx, iptCmd(v6, "-C", jump...))
		if err != nil {
			if isExit(err) {
				break
			}
			return nil, err
		}
		if removed == maxJumpDeletes {
			return nil, fmt.Errorf("%s: the OUTPUT jump is still present after %d deletes", bin, removed)
		}
		if _, err := r.Run(ctx, iptCmd(v6, "-D", jump...)); err != nil {
			return nil, err
		}
		removed++
	}
	if _, err := r.Run(ctx, iptCmd(v6, "-F", iptChain)); err != nil {
		return nil, err
	}
	if _, err := r.Run(ctx, iptCmd(v6, "-X", iptChain)); err != nil {
		return nil, err
	}
	if _, err := r.Run(ctx, iptCmd(v6, "-S", iptChain)); err == nil {
		return nil, fmt.Errorf("%s chain %s still present after delete", bin, iptChain)
	}
	return []string{fmt.Sprintf("removed %s OUTPUT jump and chain %s", bin, iptChain)}, nil
}

// iptChainExists reports whether a family's chain is present, read-only.
func iptChainExists(ctx context.Context, r Runner, v6 bool) (bool, error) {
	_, err := r.Run(ctx, iptCmd(v6, "-S", iptChain))
	if err == nil {
		return true, nil
	}
	if isExit(err) {
		return false, nil
	}
	return false, err
}
