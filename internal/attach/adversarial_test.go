package attach

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestRuntime(next http.Handler) *runtime {
	return &runtime{addr: "127.0.0.1:1", gate: &gate{nonce: "secret-nonce", next: next}}
}

func TestDecryptedRewriteIsConfined(t *testing.T) {
	var gotPath, gotHost string
	calls := 0
	rt := newTestRuntime(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls++
		gotPath, gotHost = r.URL.Path, r.Host
	}))
	h := rt.decrypted("/work")

	serve := func(mutate func(*http.Request)) int {
		r := httptest.NewRequest(http.MethodPost, "https://api.acd-ctx.invalid:443/v1/messages", nil)
		mutate(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	if c := serve(func(*http.Request) {}); c != 200 || gotPath != "/ctx/https://api.acd-ctx.invalid/v1/messages" || gotHost != rt.addr {
		t.Fatalf("normal: code %d path %q host %q", c, gotPath, gotHost)
	}
	before := calls
	// OPTIONS * must not glue "*" onto the host name.
	if c := serve(func(r *http.Request) { r.URL.Path = "*" }); c != http.StatusBadRequest || calls != before {
		t.Fatalf("asterisk target: code %d, reached proxy %v", c, calls != before)
	}
	if c := serve(func(r *http.Request) { r.URL.Path = "" }); c != http.StatusBadRequest || calls != before {
		t.Fatalf("empty target: code %d", c)
	}
	if c := serve(func(r *http.Request) { r.URL.Host = "api.acd-ctx.invalid:8443" }); c != http.StatusForbidden || calls != before {
		t.Fatalf("odd port: code %d", c)
	}
}

func TestProbeHeaderWithoutNonceIsCountedAsHit(t *testing.T) {
	rt := newTestRuntime(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, v := range []string{"guess", "secret-nonc", "secret-nonce-x"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
		r.Header.Set(probeHeader, v)
		w := httptest.NewRecorder()
		rt.gate.ServeHTTP(w, r)
		if w.Code == http.StatusNoContent {
			t.Fatalf("forged probe header %q was acknowledged", v)
		}
	}
	if rt.gate.hits.Load() != 3 || rt.gate.probes.Load() != 0 {
		t.Fatalf("hits %d probes %d, want 3 and 0", rt.gate.hits.Load(), rt.gate.probes.Load())
	}
}

func TestConnectWithoutMITMIsRefused(t *testing.T) {
	rt := newTestRuntime(http.NotFoundHandler())
	r := httptest.NewRequest(http.MethodConnect, "http://evil.example:443", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("CONNECT code %d, want 405", w.Code)
	}
}
