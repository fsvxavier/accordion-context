package mitm

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Go's verifier must reject a leaf for a host outside the CA's name
// constraints, even when the CA key signed it (a stolen-key scenario).
func TestNameConstraintsRejectOutOfScopeLeafChain(t *testing.T) {
	ca := newCAForTest(t, "example.com")
	roots := caPool(ca)
	now := time.Now()
	verify := func(host string) error {
		c, err := ca.mint(host, now) // bypasses CA.Leaf's own check on purpose
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Leaf.Verify(x509.VerifyOptions{
			DNSName: host, Roots: roots, CurrentTime: now,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		return err
	}
	for _, h := range []string{"example.com", "api.example.com"} {
		if err := verify(h); err != nil {
			t.Fatalf("in-scope %s failed: %v", h, err)
		}
	}
	for _, h := range []string{"evil.com", "example.com.evil.com", "notexample.com"} {
		err := verify(h)
		var ie x509.CertificateInvalidError
		if !errors.As(err, &ie) || ie.Reason != x509.CANotAuthorizedForThisName {
			t.Fatalf("out-of-scope %s: err = %v, want CANotAuthorizedForThisName", h, err)
		}
		if _, lerr := ca.Leaf(h); lerr == nil {
			t.Fatalf("CA.Leaf minted out-of-scope %s", h)
		}
	}
}

func TestCAKeySymlinkIsNotFollowed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if _, err := LoadOrCreateCA(dir, CAOptions{}); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	kp := filepath.Join(dir, caKeyName)
	_ = os.Remove(kp)
	if err := os.Symlink(victim, kp); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := LoadOrCreateCA(dir, CAOptions{}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(victim); fi.Mode().Perm() != 0o666 {
		t.Fatalf("symlink target chmodded to %o", fi.Mode().Perm())
	}
	if fi, _ := os.Lstat(kp); !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key not replaced by a 0600 regular file: %v", fi.Mode())
	}
}

func TestDNSNameStrict(t *testing.T) {
	for _, bad := range []string{"", "a..b", ".example.com", "ex ample.com", "exämple.com", "a/b", "127.0.0.1", "ex\x00.com", "*.x.com"} {
		if _, err := dnsName(bad); err == nil {
			t.Errorf("dnsName(%q) accepted", bad)
		}
	}
	if got, err := dnsName("API.Example.com."); err != nil || got != "api.example.com" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDefaultDialRefusesLocalTargets(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ca := newCAForTest(t)
	s, err := New(Options{CA: ca, Handler: nilHandler{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{ln.Addr().String(), "169.254.169.254:80", "[::1]:80", "[::ffff:127.0.0.1]:80", "0.0.0.0:80"} {
		conn, err := s.dial(context.Background(), "tcp", addr)
		if err == nil {
			conn.Close()
			t.Fatalf("dial %s succeeded", addr)
		}
	}
}

func TestIsLoopbackVariants(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:1": true, "[::1]:1": true, "[::ffff:127.0.0.1]:1": true,
		"10.0.0.1:1": false, "": false, "@": false, "[::ffff:10.0.0.1]:1": false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

type nilHandler struct{}

func (nilHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}
