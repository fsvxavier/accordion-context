package acdctx

import (
	"net/url"
	"testing"
)

func TestOriginKeyNormalizesDefaultPorts(t *testing.T) {
	for in, want := range map[string]string{
		"https://API.host:443/x": "https://api.host",
		"https://api.host/x":     "https://api.host",
		"http://api.host:80":     "http://api.host",
		"https://api.host:8443":  "https://api.host:8443",
		"http://api.host:443":    "http://api.host:443",
		"https://[::1]:443/":     "https://[::1]",
	} {
		u, _ := url.Parse(in)
		if got := originKey(u); got != want {
			t.Errorf("originKey(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestRawCtxPathKeepsEncodedSlash(t *testing.T) {
	u, _ := url.Parse("https://h/v1/a%2Fb")
	if got, want := rawCtxPath(u), "/ctx/https://h/v1/a%2Fb"; got != want {
		t.Fatalf("rawCtxPath=%q, want %q", got, want)
	}
}
