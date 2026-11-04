package mitm

import (
	"errors"
	"net"
	"strings"
)

// CAEnvVars lists the environment variables that tell common clients which CA
// bundle to trust.
var CAEnvVars = []string{
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
	"NODE_EXTRA_CA_CERTS",
	"REQUESTS_CA_BUNDLE",
	"CURL_CA_BUNDLE",
}

// ClientSupport returns the CA env vars from CAEnvVars that are set to a
// non-blank value in env, in CAEnvVars order. It never returns nil.
func ClientSupport(env map[string]string) []string {
	out := []string{}
	for _, k := range CAEnvVars {
		if strings.TrimSpace(env[k]) != "" {
			out = append(out, k)
		}
	}
	return out
}

// ClassifyHandshakeError reports whether err is a client-side TLS alert that
// means the client refused the minted certificate. It returns a short reason
// such as "unknown_ca" and true for those alerts. Other errors return false.
func ClassifyHandshakeError(err error) (string, bool) {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "remote error" || op.Err == nil {
		return "", false
	}
	msg := op.Err.Error()
	switch {
	case strings.Contains(msg, "unknown certificate authority"):
		return "unknown_ca", true
	case strings.Contains(msg, "bad certificate"):
		return "bad_certificate", true
	case strings.Contains(msg, "certificate unknown"):
		return "certificate_unknown", true
	}
	return "", false
}
