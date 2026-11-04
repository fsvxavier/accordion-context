package mitm

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

func TestClassifyHandshakeError(t *testing.T) {
	remote := func(msg string) error {
		return &net.OpError{Op: "remote error", Err: errors.New(msg)}
	}
	cases := []struct {
		err    error
		reason string
		ok     bool
	}{
		{remote("tls: unknown certificate authority"), "unknown_ca", true},
		{remote("tls: bad certificate"), "bad_certificate", true},
		{remote("tls: certificate unknown"), "certificate_unknown", true},
		{remote("tls: protocol version not supported"), "", false},
		{&net.OpError{Op: "read", Err: errors.New("EOF")}, "", false},
		{errors.New("plain"), "", false},
		{nil, "", false},
	}
	for _, c := range cases {
		reason, ok := ClassifyHandshakeError(c.err)
		if reason != c.reason || ok != c.ok {
			t.Errorf("ClassifyHandshakeError(%v) = %q,%v want %q,%v", c.err, reason, ok, c.reason, c.ok)
		}
	}
}

func TestClientSupport(t *testing.T) {
	got := ClientSupport(map[string]string{
		"NODE_EXTRA_CA_CERTS": "/x/ca.pem",
		"SSL_CERT_FILE":       "/x/bundle.pem",
		"CURL_CA_BUNDLE":      "   ",
		"HTTPS_PROXY":         "http://x",
	})
	want := []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClientSupport = %v, want %v", got, want)
	}
	if empty := ClientSupport(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("ClientSupport(nil) = %#v", empty)
	}
}
