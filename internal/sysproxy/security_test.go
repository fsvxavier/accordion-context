package sysproxy

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// allowedRulesetLine lists every line shape Ruleset may produce. Anything else
// (a stray newline, ';', '}', quote or domain text) fails the injection test.
var allowedRulesetLine = []*regexp.Regexp{
	regexp.MustCompile(`^table inet acd_ctx \{$`),
	regexp.MustCompile(`^\tset mitm_ipv4 \{$`),
	regexp.MustCompile(`^\t\ttype ipv4_addr$`),
	regexp.MustCompile(`^\t\telements = \{ (\d{1,3}(\.\d{1,3}){3})(, \d{1,3}(\.\d{1,3}){3})* \}$`),
	regexp.MustCompile(`^\t\}$`),
	regexp.MustCompile(`^$`),
	regexp.MustCompile(`^\tchain output \{$`),
	regexp.MustCompile(`^\t\ttype nat hook output priority -100; policy accept;$`),
	regexp.MustCompile(`^\t\tmeta skuid \d+ accept comment "acd-ctx"$`),
	regexp.MustCompile(`^\t\tip daddr 127\.0\.0\.0/8 accept comment "acd-ctx"$`),
	regexp.MustCompile(`^\t\tmeta skuid \d+ meta nfproto ipv4 meta l4proto tcp tcp dport 443 redirect to :(\d+|<port>) comment "acd-ctx"$`),
	regexp.MustCompile(`^\t\tmeta skuid \d+ meta l4proto tcp tcp dport 443 ip daddr @mitm_ipv4 redirect to :(\d+|<port>) comment "acd-ctx"$`),
	regexp.MustCompile(`^\}$`),
}

func lineAllowed(l string) bool {
	for _, re := range allowedRulesetLine {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// TestRulesetInjection: hostile host names go through the resolver only, and
// the rendered ruleset and update script contain nothing but allowed lines.
func TestRulesetInjection(t *testing.T) {
	hostile := []string{
		"evil.com\n\tmeta skuid 0 redirect to :1",
		"a;flush ruleset", "x}\ndelete table inet filter", `q"; drop`, "a\rb", "",
	}
	e := Env{Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
		// A hostile resolver answer is still only a list of addresses.
		return []netip.Addr{netip.MustParseAddr("::ffff:9.9.9.9"), netip.MustParseAddr("127.0.0.1"),
			netip.MustParseAddr("0.0.0.0"), netip.MustParseAddr("224.0.0.1"), netip.MustParseAddr("2001:db8::1")}, nil
	}}
	addrs, _ := ResolveIPv4(context.Background(), e, hostile)
	p := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, IPv4: addrs}
	for _, all := range []bool{false, true} {
		p.AllDestinations = all
		text, err := p.Ruleset()
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if !lineAllowed(l) {
				t.Fatalf("unexpected ruleset line %q in\n%s", l, text)
			}
		}
		if strings.Contains(text, "evil") || strings.Contains(text, "flush") || strings.Contains(text, "delete") {
			t.Fatalf("host text leaked into ruleset:\n%s", text)
		}
	}
	p.AllDestinations = false
	for _, l := range strings.Split(strings.TrimSpace(p.sortedIPv4Join()+"\n"+p.UpdateScript()), "\n") {
		if l != "" && !strings.HasPrefix(l, "flush set inet acd_ctx mitm_ipv4") &&
			!strings.HasPrefix(l, "add element inet acd_ctx mitm_ipv4 { 9.9.9.9 }") && l != "9.9.9.9" {
			t.Fatalf("unexpected update line %q", l)
		}
	}
	if got := p.sortedIPv4(); len(got) != 1 || got[0] != "9.9.9.9" {
		t.Fatalf("loopback/unspecified/multicast/IPv6 must be dropped, got %v", got)
	}
}

func (p NFTPlan) sortedIPv4Join() string { return strings.Join(p.sortedIPv4(), "\n") }

// TestRulesetNeverRedirectsProxyOrLoopback evaluates the rendered output chain
// in order for packets and checks that no packet of the proxy's uid, none to
// loopback, and no IPv6 or non-443 packet ever reaches a redirect verdict.
func TestRulesetNeverRedirectsProxyOrLoopback(t *testing.T) {
	type pkt struct {
		uid   int
		proto int // 4 or 6
		dst   string
		port  int
	}
	for _, all := range []bool{false, true} {
		p := NFTPlan{TargetUID: 1000, ProxyUID: 1234, RedirectPort: 8443, AllDestinations: all,
			IPv4: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
		text, err := p.Ruleset()
		if err != nil {
			t.Fatal(err)
		}
		var rules []string
		for _, l := range strings.Split(text, "\n") {
			if t := strings.TrimSpace(l); strings.HasPrefix(t, "meta skuid") || strings.HasPrefix(t, "ip daddr 127") {
				rules = append(rules, t)
			}
		}
		set := map[string]bool{"93.184.216.34": true}
		redirects := func(k pkt) bool {
			for _, r := range rules {
				switch {
				case r == `meta skuid 1234 accept comment "acd-ctx"`:
					if k.uid == 1234 {
						return false
					}
				case strings.HasPrefix(r, "ip daddr 127.0.0.0/8 accept"):
					if k.proto == 4 && strings.HasPrefix(k.dst, "127.") {
						return false
					}
				case strings.Contains(r, "redirect to"):
					if k.uid != 1000 || k.port != 443 {
						continue
					}
					if strings.Contains(r, "meta nfproto ipv4") && k.proto != 4 {
						continue
					}
					if strings.Contains(r, "ip daddr @mitm_ipv4") && !(k.proto == 4 && set[k.dst]) {
						continue
					}
					return true
				}
			}
			return false
		}
		for _, k := range []pkt{
			{1234, 4, "93.184.216.34", 443}, {1234, 4, "8.8.8.8", 443}, {1234, 4, "127.0.0.1", 443},
			{1000, 4, "127.0.0.1", 443}, {1000, 4, "127.1.2.3", 443}, {1000, 6, "2001:db8::1", 443},
			{1000, 4, "93.184.216.34", 80}, {0, 4, "93.184.216.34", 443}, {1001, 4, "93.184.216.34", 443},
		} {
			if redirects(k) {
				t.Fatalf("all=%v: packet %+v would be redirected\n%s", all, k, text)
			}
		}
		if !redirects(pkt{1000, 4, "93.184.216.34", 443}) {
			t.Fatalf("all=%v: the target's own traffic is not redirected\n%s", all, text)
		}
	}
	// The proxy's uid can never be the target.
	if err := (NFTPlan{TargetUID: 5, ProxyUID: 5, AllDestinations: true}).Validate(); err == nil {
		t.Fatal("target == proxy uid accepted")
	}
}

func FuzzParseSNI(f *testing.F) {
	_, raw := clientHelloF(f, "seed.example.com")
	f.Add(raw[5:])
	f.Add([]byte{0x01, 0, 0, 0})
	f.Add([]byte{0x01, 0xff, 0xff, 0xff, 3, 3})
	f.Fuzz(func(t *testing.T, rec []byte) {
		sni, ok := ParseSNI(rec)
		if !ok && sni != "" {
			t.Fatalf("name %q returned with ok=false", sni)
		}
		if len(sni) > len(rec) {
			t.Fatalf("name longer than the record")
		}
	})
}

func FuzzReadHello(f *testing.F) {
	_, raw := clientHelloF(f, "seed.example.com")
	f.Add(raw)
	f.Add([]byte{0x16, 3, 1, 0xff, 0xff})
	f.Add([]byte{0x16, 3, 1, 0, 0})
	f.Add([]byte("GET / HTTP/1.1\r\n\r\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		sni, got, _ := ReadHello(bytes.NewReader(in))
		if len(got) > 5+maxRecord || len(got) > len(in) {
			t.Fatalf("read %d bytes of %d (max record %d)", len(got), len(in), maxRecord)
		}
		if !bytes.HasPrefix(in, got) {
			t.Fatal("returned bytes are not a prefix of the input: replay would corrupt the stream")
		}
		_ = sni
	})
}

func clientHelloF(f *testing.F, name string) (string, []byte) {
	f.Helper()
	return helloBytes(name)
}

// TestStateRejectsUntrustedFiles: a state file another user could have written
// is never replayed.
func TestStateRejectsUntrustedFiles(t *testing.T) {
	dir := t.TempDir()
	st := &State{Mode: ModeSystemProxy, Backend: "gnome", PID: 31337, Created: time.Now()}
	p, err := createState(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readState(p); err != nil {
		t.Fatalf("own 0600 file refused: %v", err)
	}
	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := readState(p); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("world-writable state accepted: %v", err)
	}
	_ = os.Chmod(p, 0o600)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(p, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := readState(link); err == nil {
		t.Fatal("symlinked state accepted")
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := readState(p); err == nil {
		t.Fatal("state in a group/world-writable dir accepted")
	}
	if _, err := createState(dir, &State{Mode: ModeTransparent, PID: 5}); err == nil {
		t.Fatal("state created in a group/world-writable dir")
	}
}

func TestPeerRowMustBeEstablished(t *testing.T) {
	root := fakeProc(t, 46544, 8080, 0)
	data, _ := os.ReadFile(filepath.Join(root, "net", "tcp"))
	// TIME_WAIT (06) rows carry uid 0 and must not authenticate anyone.
	data = bytes.Replace(data, []byte(" 01 "), []byte(" 06 "), 1)
	_ = os.WriteFile(filepath.Join(root, "net", "tcp"), data, 0o644)
	if _, err := PeerUID(root, netip.MustParseAddrPort("127.0.0.1:46544"), netip.MustParseAddrPort("127.0.0.1:8080")); err == nil {
		t.Fatal("TIME_WAIT row attributed")
	}
}

func TestEndpointRejectsRemoteAndEscapes(t *testing.T) {
	for _, ep := range []Endpoint{
		{Host: "8.8.8.8", Port: 1}, {Host: "evil.example", Port: 1}, {Host: `1\27.0.0.1`, Port: 1},
		{Host: "127.0.0.1", Port: 1, Bypass: []string{`a\b`}}, {Host: "127.0.0.1", Port: 1, Bypass: []string{"-x"}},
	} {
		if ep.Validate() == nil {
			t.Fatalf("endpoint %+v accepted", ep)
		}
	}
	for _, h := range []string{"127.0.0.1", "localhost", "::1"} {
		if err := (Endpoint{Host: h, Port: 1}).Validate(); err != nil {
			t.Fatalf("%s: %v", h, err)
		}
	}
}
