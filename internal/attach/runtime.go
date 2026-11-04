package attach

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsvxavier/accordion-context/internal/mitm"
	"github.com/fsvxavier/accordion-context/internal/proxy"
	"github.com/fsvxavier/accordion-context/internal/store"
)

const (
	probeHeader   = "X-Acd-Ctx-Attach-Probe"
	probePath     = "/v1/messages"
	proxyUser     = "acd-ctx"
	bundleName    = "ca-bundle.pem"
	headerProject = "X-Acd-Ctx-Project-Dir"
	upstreamTLSTO = 10 * time.Second
	idleTimeout   = 90 * time.Second
)

// kv is one environment assignment.
type kv struct{ k, v string }

// probe checks one path the client would use. It returns nil when the probe
// reached the listener.
type probe struct {
	name string
	run  func(context.Context) error
}

// plan is one degree ready to run: the environment it sets and the probes that
// must pass first. all selects whether every probe is needed or just one.
type plan struct {
	degree   string
	env      []kv
	probes   []probe
	all      bool
	usesMITM bool
	// flags are argv tokens for the client, placed before its own arguments
	// unless flagsAfter is set.
	flags      []string
	flagsAfter bool
}

// runProbes runs p's probes. It returns "verified" and true on success, or the
// joined failure reasons.
func runProbes(ctx context.Context, p plan) (string, bool) {
	if len(p.probes) == 0 {
		return "no probe available", false
	}
	var fails []string
	passed := 0
	for _, pr := range p.probes {
		if err := pr.run(ctx); err != nil {
			fails = append(fails, pr.name+": "+err.Error())
			continue
		}
		passed++
	}
	if passed > 0 && (!p.all || len(fails) == 0) {
		return "verified", true
	}
	return strings.Join(fails, "; "), false
}

// gate counts requests that reach the proxy and answers probes itself.
type gate struct {
	nonce  string
	next   http.Handler
	hits   atomic.Int64
	probes atomic.Int64
}

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h := r.Header.Get(probeHeader); h != "" && subtle.ConstantTimeCompare([]byte(h), []byte(g.nonce)) == 1 {
		g.probes.Add(1)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	g.hits.Add(1)
	g.next.ServeHTTP(w, r)
}

// upstreamRT sends the proxy's upstream requests. Decrypted requests use the
// transport from mitm (it never reads HTTPS_PROXY); everything else uses a
// transport with no proxy, so the listener can never be reached again.
type upstreamRT struct{ direct *http.Transport }

func (u upstreamRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if tr, ok := mitm.UpstreamTransport(req.Context()); ok {
		return tr.RoundTrip(req)
	}
	return u.direct.RoundTrip(req)
}

// runtime is the in-process listener: one loopback port serving plain
// requests (base URL degree) and CONNECT (MITM degree).
type runtime struct {
	addr string
	gate *gate
	mitm atomic.Pointer[mitm.Server]
	srv  *http.Server
	ln   net.Listener
	// system marks a listener reached through a system proxy or a redirect.
	// Such clients cannot carry the per-run token, so the listener checks the
	// peer instead (see sysproxy.PeerListener) and adds the token itself.
	system bool
}

func startRuntime(o Options, st *store.Store, logger *slog.Logger, sc *scope) (*runtime, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("attach: listen: %w", err)
	}
	return startRuntimeOn(o, st, logger, sc, ln, false)
}

// startRuntimeOn serves the runtime on ln. It closes ln when it fails.
func startRuntimeOn(o Options, st *store.Store, logger *slog.Logger, sc *scope, ln net.Listener, system bool) (*runtime, error) {
	rt, err := newRuntime(o, st, logger, sc, ln, system)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	rt.srv = &http.Server{Handler: rt, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = rt.srv.Serve(ln) }()
	return rt, nil
}

func newRuntime(o Options, st *store.Store, logger *slog.Logger, sc *scope, ln net.Listener, system bool) (*runtime, error) {
	nonce, err := randomToken()
	if err != nil {
		return nil, err
	}
	dial := o.Dial
	direct := &http.Transport{
		Proxy:               nil,
		DialContext:         dial,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: upstreamTLSTO,
		IdleConnTimeout:     idleTimeout,
	}
	if dial == nil {
		direct.DialContext = (&net.Dialer{Timeout: upstreamTLSTO}).DialContext
	}

	allow := []string{}
	for _, h := range sc.caHosts {
		allow = append(allow, "https://"+h)
	}
	h, err := proxy.New(proxy.Config{
		Upstream:      sc.upstream,
		Store:         st,
		Version:       o.Version,
		Logger:        logger,
		Markers:       o.Markers,
		UpstreamAllow: allow,
		ListenHost:    "127.0.0.1",
		Transport:     upstreamRT{direct: direct},
	})
	if err != nil {
		return nil, fmt.Errorf("attach: configure proxy: %w", err)
	}
	return &runtime{
		addr:   ln.Addr().String(),
		gate:   &gate{nonce: nonce, next: h},
		ln:     ln,
		system: system,
	}, nil
}

// ServeHTTP routes CONNECT to the MITM server and everything else to the gate.
func (rt *runtime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rt.system {
		rt.serveSystem(w, r)
		return
	}
	if r.Method == http.MethodConnect {
		m := rt.mitm.Load()
		if m == nil {
			http.Error(w, "CONNECT is not enabled for this run", http.StatusMethodNotAllowed)
			return
		}
		m.ServeHTTP(w, r)
		return
	}
	rt.gate.ServeHTTP(w, r)
}

// serveSystem accepts only CONNECT, the way a system proxy sends HTTPS. Every
// request is admitted by the peer check on the listener, so the token is added
// here, replacing whatever the client sent.
func (rt *runtime) serveSystem(w http.ResponseWriter, r *http.Request) {
	m := rt.mitm.Load()
	if r.Method != http.MethodConnect || m == nil {
		w.Header().Set("Allow", http.MethodConnect)
		http.Error(w, "only CONNECT is accepted", http.StatusMethodNotAllowed)
		return
	}
	r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyUser+":"+m.Token())))
	m.ServeHTTP(w, r)
}

func (rt *runtime) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.srv.Shutdown(ctx); err != nil {
		_ = rt.srv.Close()
	}
}

// decrypted rewrites a request the MITM server decrypted into the proxy's
// /ctx/https://<host><path> form and hands it to the gate.
func (rt *runtime) decrypted(cwd string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Port(); p != "" && p != "443" {
			http.Error(w, "port not allowed", http.StatusForbidden)
			return
		}
		path := r.URL.Path
		if !strings.HasPrefix(path, "/") {
			// "" or "*" (OPTIONS *) would otherwise glue onto the host name.
			http.Error(w, "unsupported request target", http.StatusBadRequest)
			return
		}
		esc := ""
		if r.URL.RawPath != "" {
			esc = "/ctx/https://" + r.URL.Hostname() + r.URL.EscapedPath() // keeps %2F
		}
		r.URL.Path = "/ctx/https://" + r.URL.Hostname() + path
		r.URL.RawPath = esc
		r.URL.Scheme = ""
		r.URL.Host = ""
		r.Host = rt.addr
		if r.Header.Get(headerProject) == "" {
			r.Header.Set(headerProject, cwd)
		}
		rt.gate.ServeHTTP(w, r)
	})
}

// mitmState is the prepared MITM degree: the CA, the bundle location and the
// environment that makes a client use it.
type mitmState struct {
	env            []kv
	caPEM          []byte
	token          string
	notes          []string
	hasSystemRoots bool
}

// setupMITM loads or creates the CA, writes the bundle into the run's temp dir
// and installs the MITM server on the listener.
func (rt *runtime) setupMITM(sc *scope) (*mitmState, error) {
	o := sc.o
	if o.CADir == "" {
		return nil, errors.New("no CA directory configured")
	}
	ca, err := mitm.LoadOrCreateCA(o.CADir, mitm.CAOptions{Domains: sc.caHosts})
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}
	logger := slog.New(slog.DiscardHandler)
	srv, err := mitm.New(mitm.Options{
		CA:      ca,
		Domains: sc.runHosts,
		Handler: rt.decrypted(sc.dir),
		Dial:    o.Dial,
		Logger:  logger,
	})
	if err != nil {
		return nil, fmt.Errorf("configure MITM: %w", err)
	}
	rt.mitm.Store(srv)

	bundle := filepath.Join(sc.tmp, bundleName)
	m := &mitmState{token: srv.Token(), hasSystemRoots: true}
	if err := ca.WriteBundle(bundle, true); err != nil {
		if !errors.Is(err, mitm.ErrNoSystemBundle) {
			return nil, fmt.Errorf("write CA bundle: %w", err)
		}
		m.hasSystemRoots = false
		if err := ca.WriteBundle(bundle, false); err != nil {
			return nil, fmt.Errorf("write CA bundle: %w", err)
		}
	}
	m.caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate().Raw})

	proxyURL := (&url.URL{Scheme: "http", User: url.UserPassword(proxyUser, m.token), Host: rt.addr}).String()
	m.env = []kv{{"HTTPS_PROXY", proxyURL}, {"https_proxy", proxyURL}}
	if m.hasSystemRoots {
		// These replace the client's trust store, so the bundle must include
		// the system roots or every blind-tunnelled host would fail.
		for _, k := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS"} {
			m.env = append(m.env, kv{k, bundle})
		}
	} else {
		// NODE_EXTRA_CA_CERTS adds to the store and is therefore safe alone.
		m.env = append(m.env, kv{"NODE_EXTRA_CA_CERTS", bundle})
		m.notes = append(m.notes, "no system CA bundle found: only NODE_EXTRA_CA_CERTS trusts the acd-ctx CA; other variables were not set")
	}
	return m, nil
}

// probeBase sends a probe along the base URL the client would use.
func (rt *runtime) probeBase(ctx context.Context, base string, timeout time.Duration) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+probePath, nil)
	if err != nil {
		return err
	}
	req.Header.Set(probeHeader, rt.gate.nonce)
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	return rt.expectProbe(&http.Client{Timeout: timeout, Transport: tr}, req)
}

// probeMITM sends a probe through HTTPS_PROXY, trusting only the CA, the way a
// client configured by the MITM degree would.
func (rt *runtime) probeMITM(ctx context.Context, m *mitmState, host string, timeout time.Duration) error {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(m.caPEM) {
		return errors.New("CA certificate does not parse")
	}
	pu := &url.URL{Scheme: "http", User: url.UserPassword(proxyUser, m.token), Host: rt.addr}
	tr := &http.Transport{
		Proxy:               http.ProxyURL(pu),
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: timeout,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+probePath, nil)
	if err != nil {
		return err
	}
	req.Header.Set(probeHeader, rt.gate.nonce)
	return rt.expectProbe(&http.Client{Timeout: timeout, Transport: tr}, req)
}

// expectProbe performs req and requires the listener's probe acknowledgement.
func (rt *runtime) expectProbe(c *http.Client, req *http.Request) error {
	before := rt.gate.probes.Load()
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if rt.gate.probes.Load() <= before {
		return errors.New("probe did not reach the listener")
	}
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("attach: random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
