package mitm

import (
	"errors"
	"os"
	"slices"
)

// ErrNoSystemBundle is returned when no file in SystemBundlePaths exists.
var ErrNoSystemBundle = errors.New("mitm: no system CA bundle file found")

// SystemBundlePaths lists system root bundles, tried in order when a bundle
// includes system roots. On platforms without such a file, Bundle returns
// ErrNoSystemBundle and the caller should export the CA alone.
var SystemBundlePaths = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// Bundle returns the CA certificate as PEM, followed by the previous CA
// certificate while it is inside its grace period, then optionally the system
// roots. Use it for SSL_CERT_FILE, NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE and
// CURL_CA_BUNDLE, scoped to the client process. Including the system roots keeps
// the client trusting other hosts, because these variables replace its store.
func (ca *CA) Bundle(withSystemRoots bool) ([]byte, error) {
	out := slices.Clone(ca.certPEM)
	out = append(out, ca.prevPEM...)
	if !withSystemRoots {
		return out, nil
	}
	for _, p := range SystemBundlePaths {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return append(out, data...), nil
		}
	}
	return nil, ErrNoSystemBundle
}

// WriteBundle writes Bundle output to path with mode 0600.
func (ca *CA) WriteBundle(path string, withSystemRoots bool) error {
	data, err := ca.Bundle(withSystemRoots)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, keyMode)
}
