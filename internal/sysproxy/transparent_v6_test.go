package sysproxy

import (
	"strings"
	"testing"
)

// wantRulesetDual is the nft ruleset with both families, destination sets mode.
const wantRulesetDual = `create table inet acd_ctx
flush table inet acd_ctx
table inet acd_ctx {
	set mitm_ipv4 {
		type ipv4_addr
		elements = { 1.1.1.1, 93.184.216.34 }
	}
	set mitm_ipv6 {
		type ipv6_addr
		elements = { 2001:db8::1, 2001:db8::2 }
	}

	chain output {
		type nat hook output priority -100; policy accept;
		meta skuid 0 accept comment "acd-ctx"
		ip daddr 127.0.0.0/8 accept comment "acd-ctx"
		ip6 daddr ::1 accept comment "acd-ctx"
		meta skuid 1000 meta l4proto tcp tcp dport 443 ip daddr @mitm_ipv4 redirect to :8443 comment "acd-ctx"
		meta skuid 1000 meta l4proto tcp tcp dport 443 ip6 daddr @mitm_ipv6 redirect to :8443 comment "acd-ctx"
	}
}
`

// wantRulesetDualAll is the all-443 form with both families.
const wantRulesetDualAll = `create table inet acd_ctx
flush table inet acd_ctx
table inet acd_ctx {

	chain output {
		type nat hook output priority -100; policy accept;
		meta skuid 0 accept comment "acd-ctx"
		ip daddr 127.0.0.0/8 accept comment "acd-ctx"
		ip6 daddr ::1 accept comment "acd-ctx"
		meta skuid 1000 meta nfproto ipv4 meta l4proto tcp tcp dport 443 redirect to :8443 comment "acd-ctx"
		meta skuid 1000 meta nfproto ipv6 meta l4proto tcp tcp dport 443 redirect to :8443 comment "acd-ctx"
	}
}
`

func TestRulesetDualStackGolden(t *testing.T) {
	p := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, RedirectIPv6: true,
		IPv4: ips(t, "93.184.216.34", "1.1.1.1"), IPv6: ips(t, "2001:db8::2", "2001:db8::1", "::1")}
	got, err := p.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if got != wantRulesetDual {
		t.Fatalf("dual-stack ruleset mismatch\n--- got\n%s--- want\n%s", got, wantRulesetDual)
	}
	all := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, AllDestinations: true, RedirectIPv6: true}
	got, err = all.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if got != wantRulesetDualAll {
		t.Fatalf("dual-stack all-443 ruleset mismatch\n--- got\n%s--- want\n%s", got, wantRulesetDualAll)
	}
}

// Without IPv6 the ruleset is the IPv4 one: no ip6 rule, no ipv6 set.
func TestRulesetWithoutIPv6HasNoIPv6Lines(t *testing.T) {
	p := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1"), IPv6: ips(t, "2001:db8::1")}
	text, err := p.Ruleset()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "ip6") || strings.Contains(text, "ipv6") {
		t.Fatalf("IPv6 line without RedirectIPv6:\n%s", text)
	}
	if strings.Contains(p.UpdateScript(), "ipv6") {
		t.Fatal("IPv6 update without RedirectIPv6")
	}
}

func TestUpdateScriptDualStack(t *testing.T) {
	p := NFTPlan{TargetUID: 1000, RedirectPort: 8443, RedirectIPv6: true, IPv4: ips(t, "1.1.1.1"), IPv6: ips(t, "2001:db8::1")}
	want := "flush set inet acd_ctx mitm_ipv4\nadd element inet acd_ctx mitm_ipv4 { 1.1.1.1 }\n" +
		"flush set inet acd_ctx mitm_ipv6\nadd element inet acd_ctx mitm_ipv6 { 2001:db8::1 }\n"
	if got := p.UpdateScript(); got != want {
		t.Fatalf("update = %q", got)
	}
}

// SetDestinations keeps the previous addresses of a family that resolved to
// nothing: a failed lookup must not empty the set.
func TestSetDestinationsKeepsPreviousOnEmpty(t *testing.T) {
	f := &fakeNFT{}
	e := linuxEnv(f, 0)
	plan := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, RedirectIPv6: true,
		IPv4: ips(t, "1.1.1.1"), IPv6: ips(t, "2001:db8::1")}
	a, err := StartTransparent(t.Context(), e, t.TempDir(), plan, FirewallNFT)
	if err != nil {
		t.Fatal(err)
	}
	f.stdins = nil
	if err := a.SetDestinations(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.stdins[len(f.stdins)-1]; !strings.Contains(got, "add element inet acd_ctx mitm_ipv4 { 1.1.1.1 }") ||
		!strings.Contains(got, "add element inet acd_ctx mitm_ipv6 { 2001:db8::1 }") {
		t.Fatalf("empty resolution must keep the previous sets:\n%s", got)
	}
	if err := a.SetDestinations(t.Context(), ips(t, "2.2.2.2"), nil); err != nil {
		t.Fatal(err)
	}
	if got := f.stdins[len(f.stdins)-1]; !strings.Contains(got, "{ 2.2.2.2 }") || !strings.Contains(got, "{ 2001:db8::1 }") {
		t.Fatalf("only the resolved family changes:\n%s", got)
	}
	_, _ = a.Stop(RestoreContext2(t))
}
