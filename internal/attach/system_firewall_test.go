package attach

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// onlyTools reports the given commands as installed.
func onlyTools(names ...string) func(string) bool {
	return func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
}

// The dry run shows the iptables rules of both families when nft is absent.
func TestTransparentDryRunShowsIptablesBothFamilies(t *testing.T) {
	env := &sysproxy.Env{
		GOOS:     "linux",
		Runner:   failRunner{t},
		Geteuid:  func() int { return 0 },
		LookPath: onlyTools("iptables", "ip6tables"),
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		},
	}
	o := sysOptions(t, &SystemOptions{Yes: true, Apply: false, UID: 1000, Env: env})
	o.Transparent = true
	o.MITMDomains = []string{"api.example.com"}
	out := o.Stdout.(*bytes.Buffer)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, want := range []string{
		"iptables -w 5 -t nat -N ACD_CTX_OUT",
		"ip6tables -w 5 -t nat -N ACD_CTX_OUT",
		"-d 203.0.113.7/32 -p tcp --dport 443",
		"-I OUTPUT 1 -m owner --uid-owner 1000",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry run misses %q:\n%s", want, out.String())
		}
	}
}

// Privileged, with neither nft nor iptables: refused before any listener or
// command.
func TestTransparentApplyWithoutFirewallToolRefuses(t *testing.T) {
	env := &sysproxy.Env{
		GOOS:       "linux",
		Runner:     failRunner{t},
		Geteuid:    func() int { return 0 },
		ProcStatus: func() string { return "" },
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		},
	}
	o := sysOptions(t, &SystemOptions{Yes: true, Apply: true, UID: 1000, Env: env})
	o.Transparent = true
	o.MITMDomains = []string{"api.example.com"}
	_, err := Run(context.Background(), o)
	if !errors.Is(err, sysproxy.ErrNoFirewall) {
		t.Fatalf("err = %v, want ErrNoFirewall", err)
	}
}

// recordRunner answers the existence check as "absent" and fails the test on
// any change, so a refusal that happens after a rule would be caught.
type recordRunner struct {
	t    *testing.T
	cmds []string
}

func (r *recordRunner) Run(_ context.Context, c sysproxy.Command) (string, error) {
	r.cmds = append(r.cmds, c.String())
	if c.Name == "nft" && len(c.Args) == 4 && c.Args[0] == "list" {
		return "", &sysproxy.ExitError{Command: c, Code: 1, Output: "No such file or directory"}
	}
	r.t.Fatalf("unexpected command: %s", c)
	return "", nil
}

// Without a watchdog spawner the transparent change is refused: nothing would
// restore the redirect after a crash. The refusal comes before any rule.
func TestTransparentApplyRefusesWithoutWatchdog(t *testing.T) {
	rr := &recordRunner{t: t}
	env := &sysproxy.Env{
		GOOS:       "linux",
		Runner:     rr,
		Geteuid:    func() int { return 0 },
		ProcStatus: func() string { return "" },
		LookPath:   onlyTools("nft"),
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		},
	}
	o := sysOptions(t, &SystemOptions{Yes: true, Apply: true, UID: 1000, Env: env})
	o.Transparent = true
	o.MITMDomains = []string{"api.example.com"}
	_, err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "watchdog") {
		t.Fatalf("err = %v, want a watchdog refusal", err)
	}
	for _, c := range rr.cmds {
		if strings.Contains(c, "-f") {
			t.Fatalf("rules applied without a watchdog: %v", rr.cmds)
		}
	}
}

// The IPv4 and IPv6 redirect listeners share one port. Without [::1] on that
// port the IPv6 listener is nil, and the caller leaves IPv6 alone.
func TestListenRedirectSharesPort(t *testing.T) {
	ln4, ln6, err := listenRedirect(false)
	if err != nil {
		t.Fatal(err)
	}
	defer ln4.Close()
	if ln6 != nil {
		t.Fatal("IPv6 listener opened when not wanted")
	}
	ln4, ln6, err = listenRedirect(true)
	if err != nil {
		t.Fatal(err)
	}
	defer ln4.Close()
	if ln6 == nil {
		t.Skip("[::1] is not available on this machine")
	}
	defer ln6.Close()
	p4 := ln4.Addr().(*net.TCPAddr).Port
	p6 := ln6.Addr().(*net.TCPAddr).Port
	if p4 != p6 {
		t.Fatalf("ports differ: v4 %d, v6 %d", p4, p6)
	}
}
