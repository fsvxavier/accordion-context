package mitm

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newUpstream starts a TLS server for "localhost" / 127.0.0.1 that stands in for
// the real API host. It returns the server and a pool trusting its certificate.
func newUpstream(t *testing.T, h http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return srv, pool
}

func portOf(t *testing.T, s *httptest.Server) int {
	t.Helper()
	_, p, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return n
}

// startProxy runs a Server on loopback and returns it with the HTTPS_PROXY-style URL.
func startProxy(t *testing.T, ca *CA, domains []string, ports []int, h http.Handler, upstream *x509.CertPool) (*Server, *url.URL, *bytes.Buffer) {
	t.Helper()
	logBuf := &bytes.Buffer{}
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return logBuf.Write(p)
	}), &slog.HandlerOptions{Level: slog.LevelDebug}))
	s, err := New(Options{
		CA: ca, Domains: domains, AllowPorts: ports, Handler: h, AllowLocalTargets: true,
		UpstreamTLS: &tls.Config{RootCAs: upstream},
		Logger:      logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	u := &url.URL{Scheme: "http", Host: ts.Listener.Addr().String(), User: url.UserPassword(proxyUser, s.Token())}
	return s, u, logBuf
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func clientVia(t *testing.T, proxy *url.URL, roots *x509.CertPool, h2 bool) *http.Client {
	t.Helper()
	tr := &http.Transport{
		Proxy:             http.ProxyURL(proxy),
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2: h2,
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func newCAForTest(t *testing.T, domains ...string) *CA {
	t.Helper()
	ca, err := LoadOrCreateCA(filepath.Join(t.TempDir(), "ca"), CAOptions{Domains: domains})
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func caPool(ca *CA) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Certificate())
	return p
}

func TestMITMDecryptsHTTP1(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	upstream, roots := newUpstream(t, http.NotFoundHandler())
	port := portOf(t, upstream)
	var seenHost, seenScheme, seenURLHost string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost, seenScheme, seenURLHost = r.Host, r.URL.Scheme, r.URL.Host
		_, _ = io.WriteString(w, "decrypted:"+r.URL.Path)
	})
	_, proxy, _ := startProxy(t, ca, []string{"localhost"}, []int{port}, h, roots)

	client := clientVia(t, proxy, caPool(ca), false)
	resp, err := client.Get("https://localhost:" + strconv.Itoa(port) + "/v1/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "decrypted:/v1/messages" {
		t.Fatalf("body = %q", body)
	}
	if resp.ProtoMajor != 1 {
		t.Fatalf("proto = %s, want HTTP/1.x", resp.Proto)
	}
	if resp.TLS == nil || resp.TLS.PeerCertificates[0].Issuer.CommonName != ca.Certificate().Subject.CommonName {
		t.Fatal("client did not receive a leaf minted by the CA")
	}
	if seenScheme != "https" || seenURLHost != "localhost:"+strconv.Itoa(port) || !strings.HasPrefix(seenHost, "localhost") {
		t.Fatalf("handler saw scheme=%q urlHost=%q host=%q", seenScheme, seenURLHost, seenHost)
	}
}

func TestMITMHTTP2ViaALPN(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	upstream, roots := newUpstream(t, http.NotFoundHandler())
	port := portOf(t, upstream)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	})
	_, proxy, _ := startProxy(t, ca, []string{"localhost"}, []int{port}, h, roots)

	client := clientVia(t, proxy, caPool(ca), true)
	resp, err := client.Get("https://localhost:" + strconv.Itoa(port) + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.ProtoMajor != 2 || string(body) != "HTTP/2.0" {
		t.Fatalf("proto = %s body = %q, want h2", resp.Proto, body)
	}
}

func TestUpstreamTransportReachesRealHostWithoutLoop(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	var upstreamHits atomic.Int32
	upstream, roots := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		_, _ = io.WriteString(w, "upstream-ok")
	}))
	port := portOf(t, upstream)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, ok := UpstreamTransport(r.Context())
		if !ok {
			http.Error(w, "no transport", 500)
			return
		}
		if tr.Proxy != nil {
			http.Error(w, "upstream transport uses a proxy", 500)
			return
		}
		resp, err := (&http.Client{Transport: tr}).Get(r.URL.String() + "x")
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		_, _ = w.Write(b)
	})
	_, proxy, _ := startProxy(t, ca, []string{"localhost"}, []int{port}, h, roots)

	resp, err := clientVia(t, proxy, caPool(ca), false).Get("https://localhost:" + strconv.Itoa(port) + "/up/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "upstream-ok" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	if upstreamHits.Load() != 1 {
		t.Fatalf("upstream hits = %d, want 1", upstreamHits.Load())
	}
}

func TestBlindTunnelForNonAllowlistedHost(t *testing.T) {
	ca := newCAForTest(t, "example.invalid")
	var handled atomic.Int32
	upstream, roots := newUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	port := portOf(t, upstream)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handled.Add(1) })
	_, proxy, _ := startProxy(t, ca, []string{"example.invalid"}, []int{port}, h, roots)

	// 127.0.0.1 is not in the allowlist, so the client's own TLS runs end to end.
	resp, err := clientVia(t, proxy, roots, false).Get("https://127.0.0.1:" + strconv.Itoa(port) + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "direct" {
		t.Fatalf("body = %q", body)
	}
	if got := resp.TLS.PeerCertificates[0]; !got.Equal(upstream.Certificate()) {
		t.Fatal("tunnelled connection did not present the real server certificate")
	}
	if handled.Load() != 0 {
		t.Fatal("handler saw traffic from a tunnelled host")
	}
}

func TestProxyAuthRequired(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	upstream, roots := newUpstream(t, http.NotFoundHandler())
	port := portOf(t, upstream)
	s, proxyURL, _ := startProxy(t, ca, []string{"localhost"}, []int{port}, http.NotFoundHandler(), roots)
	target := "localhost:" + strconv.Itoa(port)

	cases := map[string]string{
		"none":      "",
		"wrong":     "Basic " + base64.StdEncoding.EncodeToString([]byte("acd-ctx:nope")),
		"not-basic": "Bearer " + s.Token(),
		"garbage":   "Basic !!!",
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			if code := rawConnect(t, proxyURL.Host, target, auth); code != http.StatusProxyAuthRequired {
				t.Fatalf("status = %d, want 407", code)
			}
		})
	}
	good := "Basic " + base64.StdEncoding.EncodeToString([]byte("acd-ctx:"+s.Token()))
	if code := rawConnect(t, proxyURL.Host, target, good); code != 200 {
		t.Fatalf("valid token status = %d, want 200", code)
	}
}

func TestProxyDeniesDisallowedPort(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	upstream, roots := newUpstream(t, http.NotFoundHandler())
	port := portOf(t, upstream)
	s, proxyURL, _ := startProxy(t, ca, []string{"localhost"}, []int{port}, http.NotFoundHandler(), roots)
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("acd-ctx:"+s.Token()))
	for _, target := range []string{"localhost:22", "localhost:" + strconv.Itoa(port+1), "localhost:0", "localhost"} {
		if code := rawConnect(t, proxyURL.Host, target, auth); code != http.StatusForbidden {
			t.Fatalf("CONNECT %s status = %d, want 403", target, code)
		}
	}
}

func TestProxyRejectsNonConnectAndNonLoopback(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	s, err := New(Options{CA: ca, Domains: []string{"localhost"}, Handler: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("acd-ctx:"+s.Token()))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodConnect, "localhost:443", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Proxy-Authorization", auth)
	s.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatal("loopback client refused")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodConnect, "localhost:443", nil)
	req.RemoteAddr = "192.0.2.10:5555"
	req.Header.Set("Proxy-Authorization", auth)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Proxy-Authorization", auth)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestDecryptedHostMismatchIsMisdirected(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	upstream, roots := newUpstream(t, http.NotFoundHandler())
	port := portOf(t, upstream)
	var called atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Store(true) })
	_, proxy, _ := startProxy(t, ca, []string{"localhost"}, []int{port}, h, roots)

	req, _ := http.NewRequest(http.MethodGet, "https://localhost:"+strconv.Itoa(port)+"/", nil)
	req.Host = "evil.example"
	resp, err := clientVia(t, proxy, caPool(ca), false).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest || called.Load() {
		t.Fatalf("status = %d handler called = %v", resp.StatusCode, called.Load())
	}
}

func TestCertRejectionIsRecorded(t *testing.T) {
	ca := newCAForTest(t, "localhost")
	upstream, roots := newUpstream(t, http.NotFoundHandler())
	port := portOf(t, upstream)
	s, proxy, logBuf := startProxy(t, ca, []string{"localhost"}, []int{port}, http.NotFoundHandler(), roots)

	// The client trusts the system roots only, not this CA, so it must refuse the leaf.
	client := clientVia(t, proxy, nil, false)
	if _, err := client.Get("https://localhost:" + strconv.Itoa(port) + "/"); err == nil {
		t.Fatal("request with untrusted CA succeeded")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rs := s.Rejections()
		if len(rs) == 1 {
			if rs[0].Host != "localhost" || rs[0].Count != 1 || rs[0].Reason == "" || rs[0].Err == "" {
				t.Fatalf("rejection = %+v", rs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no rejection recorded; rejections = %+v", rs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Contains(logBuf.String(), "PRIVATE KEY") {
		t.Fatal("private key material leaked into logs")
	}
}

func TestNewRejectsDomainOutsideCAConstraints(t *testing.T) {
	ca := newCAForTest(t, "anthropic.com")
	if _, err := New(Options{CA: ca, Domains: []string{"openai.com"}, Handler: http.NotFoundHandler()}); err == nil {
		t.Fatal("domain outside CA constraints accepted")
	}
	if _, err := New(Options{CA: ca, Domains: []string{"*.anthropic.com"}, Handler: http.NotFoundHandler()}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{CA: ca, Handler: nil}); err == nil {
		t.Fatal("nil handler accepted")
	}
	if _, err := New(Options{Handler: http.NotFoundHandler()}); err == nil {
		t.Fatal("nil CA accepted")
	}
}

// rawConnect sends a CONNECT and returns the status code of the response.
func rawConnect(t *testing.T, proxyAddr, target, auth string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if auth != "" {
		req += "Proxy-Authorization: " + auth + "\r\n"
	}
	req += "\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
