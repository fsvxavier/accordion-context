// Package mitm terminates TLS for an allowlist of API hosts so that a closed
// client that honours HTTPS_PROXY and a CA bundle env var can be inspected by
// the proxy. Every other host is blind-tunnelled. See spec §19.3 and §19.7.
package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultCAValidity is the lifetime of a newly generated CA (~1 year).
	DefaultCAValidity = 365 * 24 * time.Hour

	defaultLeafCacheSize = 256
	leafValidity         = 24 * time.Hour

	dirMode    os.FileMode = 0o700
	keyMode    os.FileMode = 0o600
	certMode   os.FileMode = 0o644
	caCertName             = "ca.crt"
	caKeyName              = "ca.key"
)

// CAOptions configures LoadOrCreateCA.
type CAOptions struct {
	// Domains restricts the names the CA may issue for (X.509 DNS name
	// constraints). "host" permits host and its subdomains; "*.domain" is
	// accepted as shorthand for "domain". Empty means unconstrained.
	Domains []string
	// Validity of a newly generated CA. Zero means DefaultCAValidity.
	Validity time.Duration
	// LeafCacheSize bounds the per-host leaf cache. Zero means 256.
	LeafCacheSize int
}

// CA is a per-install certificate authority that mints leaf certificates.
// Its private key never leaves this process and is never logged.
type CA struct {
	cert    *x509.Certificate
	certPEM []byte
	key     *ecdsa.PrivateKey
	domains []string // sorted name constraints, empty when unconstrained
	leaves  *leafCache
}

// LoadOrCreateCA loads the CA stored in dir, or generates a new one. A stored
// CA is replaced when its key is missing or inconsistent, when it is within
// validity/12 of expiry, or when its name constraints differ from opts.Domains.
// The directory is kept at 0700 and the key file at 0600. Nothing is installed
// in the system trust store.
func LoadOrCreateCA(dir string, opts CAOptions) (*CA, error) {
	if dir == "" {
		return nil, errors.New("mitm: CA directory is empty")
	}
	domains, err := constraintList(opts.Domains)
	if err != nil {
		return nil, err
	}
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	now := time.Now()
	if ca, err := loadCA(dir, opts.LeafCacheSize); err == nil && ca.usable(domains, now) {
		return ca, nil
	}
	validity := opts.Validity
	if validity <= 0 {
		validity = DefaultCAValidity
	}
	ca, err := generateCA(domains, validity, now, opts.LeafCacheSize)
	if err != nil {
		return nil, err
	}
	if err := persistCA(dir, ca); err != nil {
		return nil, err
	}
	return ca, nil
}

// Certificate returns the CA certificate. Callers must not modify it.
func (ca *CA) Certificate() *x509.Certificate { return ca.cert }

// Leaf returns a certificate for host signed by the CA, minted once and cached
// per host. host must be a DNS name permitted by the CA's name constraints.
func (ca *CA) Leaf(host string) (*tls.Certificate, error) {
	h, err := dnsName(host)
	if err != nil {
		return nil, err
	}
	if !ca.permits(h) {
		return nil, fmt.Errorf("mitm: %q is outside the CA name constraints", h)
	}
	return ca.leaves.get(h, time.Now(), ca.mint)
}

// usable reports whether a loaded CA still matches the wanted constraints and
// is not close to expiry.
func (ca *CA) usable(domains []string, now time.Time) bool {
	if !slices.Equal(ca.cert.PermittedDNSDomains, domains) {
		return false
	}
	life := ca.cert.NotAfter.Sub(ca.cert.NotBefore)
	return now.Before(ca.cert.NotAfter.Add(-life / 12))
}

// permits reports whether the CA's name constraints allow host.
func (ca *CA) permits(host string) bool {
	if len(ca.domains) == 0 {
		return true
	}
	for _, d := range ca.domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func (ca *CA) mint(host string, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mitm: generate leaf key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	notAfter := now.Add(leafValidity)
	if notAfter.After(ca.cert.NotAfter) {
		notAfter = ca.cert.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("mitm: sign leaf for %q: %w", host, err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("mitm: parse leaf for %q: %w", host, err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

func generateCA(domains []string, validity time.Duration, now time.Time, cacheSize int) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mitm: generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: "Accordion Context local CA", Organization: []string{"acd-ctx"}},
		NotBefore:                   now.Add(-5 * time.Minute),
		NotAfter:                    now.Add(validity),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:                 []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		PermittedDNSDomainsCritical: len(domains) > 0,
		PermittedDNSDomains:         domains,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("mitm: create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("mitm: parse CA certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return newCA(cert, certPEM, key, cacheSize), nil
}

func newCA(cert *x509.Certificate, certPEM []byte, key *ecdsa.PrivateKey, cacheSize int) *CA {
	if cacheSize <= 0 {
		cacheSize = defaultLeafCacheSize
	}
	return &CA{
		cert:    cert,
		certPEM: certPEM,
		key:     key,
		domains: cert.PermittedDNSDomains,
		leaves:  newLeafCache(cacheSize),
	}
}

func loadCA(dir string, cacheSize int) (*CA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, caCertName))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("mitm: CA certificate file is not a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("mitm: parse CA certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("mitm: stored certificate is not a CA")
	}
	keyPath := filepath.Join(dir, caKeyName)
	info, err := os.Lstat(keyPath) // never chmod through a symlink
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("mitm: CA key is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(keyPath, keyMode); err != nil {
			return nil, fmt.Errorf("mitm: tighten CA key mode: %w", err)
		}
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil || kb.Type != "PRIVATE KEY" {
		return nil, errors.New("mitm: CA key file is not a PEM PKCS#8 key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, errors.New("mitm: parse CA key: invalid key data")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("mitm: CA key does not match CA certificate")
	}
	return newCA(cert, certPEM, key, cacheSize), nil
}

func persistCA(dir string, ca *CA) error {
	der, err := x509.MarshalPKCS8PrivateKey(ca.key)
	if err != nil {
		return fmt.Errorf("mitm: marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := writeFileAtomic(filepath.Join(dir, caKeyName), keyPEM, keyMode); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, caCertName), ca.certPEM, certMode)
}

// writeFileAtomic writes data to a temp file in the same directory, sets its
// mode and renames it over path, so a crash never leaves a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("mitm: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return fmt.Errorf("mitm: chmod %s: %w", filepath.Base(path), err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("mitm: write %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("mitm: sync %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("mitm: close %s: %w", filepath.Base(path), err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("mitm: replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("mitm: create CA dir: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("mitm: stat CA dir: %w", err)
	}
	if info.Mode().Perm() != dirMode {
		if err := os.Chmod(dir, dirMode); err != nil {
			return fmt.Errorf("mitm: chmod CA dir: %w", err)
		}
	}
	return nil
}

func randomSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("mitm: serial: %w", err)
	}
	return n.Add(n, big.NewInt(1)), nil
}

// wildcardPrefix marks a domain entry that also matches subdomains.
const wildcardPrefix = "*."

// canonicalHost lower-cases a host and strips a trailing dot. It does not
// validate; IP literals pass through.
func canonicalHost(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}

// dnsName canonicalises h and rejects empty values, IP literals and characters
// that cannot appear in a DNS name.
func dnsName(h string) (string, error) {
	c := canonicalHost(h)
	if c == "" || len(c) > 253 {
		return "", fmt.Errorf("mitm: invalid host %q", h)
	}
	for _, label := range strings.Split(c, ".") {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("mitm: invalid host %q", h)
		}
		for i := 0; i < len(label); i++ {
			b := label[i]
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
				return "", fmt.Errorf("mitm: invalid host %q (use punycode for IDN)", h)
			}
		}
	}
	if net.ParseIP(c) != nil {
		return "", fmt.Errorf("mitm: IP literal not allowed: %q", h)
	}
	return c, nil
}

// constraintList turns domain entries into a sorted, de-duplicated list of
// DNS name constraints.
func constraintList(entries []string) ([]string, error) {
	var out []string
	for _, e := range entries {
		t := strings.TrimSpace(e)
		if t == "" {
			continue
		}
		name, err := dnsName(strings.TrimPrefix(t, wildcardPrefix))
		if err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
