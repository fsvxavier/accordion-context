package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadOrCreateCAWritesProtectedFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	ca, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"*.anthropic.com", "api.openai.com"}})
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 700", di.Mode().Perm())
	}
	ki, err := os.Stat(filepath.Join(dir, caKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if ki.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 600", ki.Mode().Perm())
	}
	c := ca.Certificate()
	if !c.IsCA || c.MaxPathLen != 0 || !c.MaxPathLenZero {
		t.Fatalf("CA flags wrong: IsCA=%v MaxPathLen=%d", c.IsCA, c.MaxPathLen)
	}
	if !c.PermittedDNSDomainsCritical {
		t.Fatal("name constraints not critical")
	}
	if got := strings.Join(c.PermittedDNSDomains, ","); got != "anthropic.com,api.openai.com" {
		t.Fatalf("constraints = %q", got)
	}
	pub, ok := c.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Fatal("CA key is not ECDSA P-256")
	}
	if life := c.NotAfter.Sub(c.NotBefore); life < 364*24*time.Hour || life > 367*24*time.Hour {
		t.Fatalf("validity = %v, want ~1y", life)
	}
	// Reloading returns the same CA, not a new one.
	again, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"api.openai.com", "anthropic.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if again.Certificate().SerialNumber.Cmp(c.SerialNumber) != 0 {
		t.Fatal("CA was regenerated on reload")
	}
}

func TestLoadOrCreateCARegeneratesOnConstraintChangeAndBrokenKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	first, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"a.test"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"b.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Certificate().SerialNumber.Cmp(first.Certificate().SerialNumber) == 0 {
		t.Fatal("constraint change did not regenerate the CA")
	}
	if err := os.WriteFile(filepath.Join(dir, caKeyName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := LoadOrCreateCA(dir, CAOptions{Domains: []string{"b.test"}})
	if err != nil {
		t.Fatalf("broken key should trigger regeneration: %v", err)
	}
	if third.Certificate().SerialNumber.Cmp(second.Certificate().SerialNumber) == 0 {
		t.Fatal("broken key did not regenerate the CA")
	}
}

func TestLoadOrCreateCATightensKeyMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	if _, err := LoadOrCreateCA(dir, CAOptions{}); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, caKeyName)
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCA(dir, CAOptions{}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(keyPath)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o after load, want 600", fi.Mode().Perm())
	}
}

func TestCAUsableWindow(t *testing.T) {
	now := time.Now()
	ca, err := generateCA([]string{"a.test"}, 12*time.Hour, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ca.usable([]string{"a.test"}, now) {
		t.Fatal("fresh CA not usable")
	}
	if ca.usable([]string{"b.test"}, now) {
		t.Fatal("CA with different constraints reported usable")
	}
	// validity/12 of a 12h CA is one hour: inside that window it must renew.
	nearEnd := ca.cert.NotAfter.Add(-30 * time.Minute)
	if ca.usable([]string{"a.test"}, nearEnd) {
		t.Fatal("CA inside its renewal window reported usable")
	}
	if ca.usable([]string{"a.test"}, ca.cert.NotAfter.Add(time.Minute)) {
		t.Fatal("expired CA reported usable")
	}
}

func TestLoadOrCreateCARejectsEmptyDir(t *testing.T) {
	if _, err := LoadOrCreateCA("", CAOptions{}); err == nil {
		t.Fatal("empty dir accepted")
	}
	if _, err := LoadOrCreateCA(t.TempDir(), CAOptions{Domains: []string{"10.0.0.1"}}); err == nil {
		t.Fatal("IP constraint accepted")
	}
}

func TestLeafMintingAndConstraints(t *testing.T) {
	ca, err := LoadOrCreateCA(filepath.Join(t.TempDir(), "ca"), CAOptions{Domains: []string{"anthropic.com"}, LeafCacheSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := ca.Leaf("api.anthropic.com")
	if err != nil {
		t.Fatal(err)
	}
	if l1.Leaf.DNSNames[0] != "api.anthropic.com" {
		t.Fatalf("SAN = %v", l1.Leaf.DNSNames)
	}
	if err := l1.Leaf.CheckSignatureFrom(ca.Certificate()); err != nil {
		t.Fatalf("leaf not signed by CA: %v", err)
	}
	if l1.Leaf.IsCA {
		t.Fatal("leaf must not be a CA")
	}
	again, _ := ca.Leaf("API.anthropic.com.")
	if again != l1 {
		t.Fatal("cache miss for same host")
	}
	for _, bad := range []string{"evil.com", "1.2.3.4", "", "a b.anthropic.com"} {
		if _, err := ca.Leaf(bad); err == nil {
			t.Fatalf("Leaf(%q) accepted", bad)
		}
	}
}

func TestLeafCacheEvictsLRU(t *testing.T) {
	ca, err := LoadOrCreateCA(filepath.Join(t.TempDir(), "ca"), CAOptions{LeafCacheSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	a1, _ := ca.Leaf("a.test")
	_, _ = ca.Leaf("b.test")
	_, _ = ca.Leaf("a.test") // a is now most recent
	_, _ = ca.Leaf("c.test") // evicts b
	a2, _ := ca.Leaf("a.test")
	if a1 != a2 {
		t.Fatal("recently used leaf was evicted")
	}
	b1, _ := ca.Leaf("b.test")
	if b1 == nil || b1.Leaf == nil {
		t.Fatal("b not minted")
	}
	if ca.leaves.ll.Len() != 2 {
		t.Fatalf("cache len = %d, want 2", ca.leaves.ll.Len())
	}
}

func TestBundle(t *testing.T) {
	ca, err := LoadOrCreateCA(filepath.Join(t.TempDir(), "ca"), CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	onlyCA, err := ca.Bundle(false)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(onlyCA), "BEGIN CERTIFICATE"); n != 1 {
		t.Fatalf("CA-only bundle has %d certs", n)
	}

	sysPath := filepath.Join(t.TempDir(), "system.pem")
	sysCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate().Raw})
	if err := os.WriteFile(sysPath, sysCert, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := SystemBundlePaths
	t.Cleanup(func() { SystemBundlePaths = orig })

	SystemBundlePaths = []string{filepath.Join(t.TempDir(), "missing.pem")}
	if _, err := ca.Bundle(true); !errors.Is(err, ErrNoSystemBundle) {
		t.Fatalf("missing system bundle: err = %v", err)
	}

	SystemBundlePaths = []string{sysPath}
	full, err := ca.Bundle(true)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(full), "BEGIN CERTIFICATE"); n != 2 {
		t.Fatalf("full bundle has %d certs, want CA + system", n)
	}
	if !strings.HasPrefix(string(full), string(onlyCA)) {
		t.Fatal("CA must come first in the bundle")
	}

	out := filepath.Join(t.TempDir(), "bundle.pem")
	if err := ca.WriteBundle(out, true); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(out)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode = %o", fi.Mode().Perm())
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(full) {
		t.Fatal("bundle does not parse")
	}
}
