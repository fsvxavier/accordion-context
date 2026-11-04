package mitm

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	proxyUser          = "acd-ctx"
	proxyRealm         = "acd-ctx"
	defaultPort        = 443
	defaultDialTimeout = 10 * time.Second
	handshakeTimeout   = 15 * time.Second
	readHeaderTimeout  = 30 * time.Second
	idleTimeout        = 90 * time.Second
)

// Options configures a forward proxy Server.
type Options struct {
	// CA mints the leaf certificates. Required.
	CA *CA
	// Domains are the hosts whose TLS is terminated. "host" matches exactly;
	// "*.domain" matches subdomains. Each must be permitted by CA's constraints.
	Domains []string
	// AllowPorts are the CONNECT target ports accepted. Default: 443.
	AllowPorts []int
	// Handler receives the decrypted requests for Domains. Required. Each
	// request has URL.Scheme "https" and URL.Host set to the real target.
	Handler http.Handler
	// Token is the Proxy-Authorization secret. Empty means a random 256-bit
	// token, to be placed in HTTPS_PROXY as http://acd-ctx:<token>@127.0.0.1:port.
	Token string
	// Dial reaches real hosts, for both blind tunnels and the upstream transport.
	// Default: net.Dialer with a 10s timeout.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// UpstreamTLS verifies real hosts. Nil means the system roots.
	UpstreamTLS *tls.Config
	// Logger receives diagnostics. It never receives tokens, headers or keys.
	Logger *slog.Logger
}

// Rejection is the per-host record of a client refusing the minted certificate.
type Rejection struct {
	Host   string
	Count  int
	Last   time.Time
	Reason string // e.g. "unknown_ca", "bad_certificate"
	Err    string // last handshake error text
}

// Server is a loopback-only forward proxy (http.Handler) for CONNECT requests.
// Domains in the allowlist are decrypted and handed to Options.Handler. All
// other targets are blind-tunnelled.
type Server struct {
	ca        *CA
	allow     []allowEntry
	ports     map[int]bool
	handler   http.Handler
	token     string
	dial      func(ctx context.Context, network, addr string) (net.Conn, error)
	transport *http.Transport
	log       *slog.Logger

	mu  sync.Mutex
	rej map[string]*Rejection
}

type allowEntry struct {
	name     string
	wildcard bool
}

// New builds a Server. It fails when a domain is not permitted by the CA's name
// constraints, so a configured domain can never get a certificate the CA
// cannot issue.
func New(o Options) (*Server, error) {
	if o.CA == nil {
		return nil, errors.New("mitm: Options.CA is required")
	}
	if o.Handler == nil {
		return nil, errors.New("mitm: Options.Handler is required")
	}
	allow, err := parseAllow(o.Domains)
	if err != nil {
		return nil, err
	}
	for _, a := range allow {
		if !o.CA.permits(a.name) {
			return nil, fmt.Errorf("mitm: CA name constraints do not permit %q", a.name)
		}
	}
	ports := o.AllowPorts
	if len(ports) == 0 {
		ports = []int{defaultPort}
	}
	portSet := make(map[int]bool, len(ports))
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("mitm: invalid port %d", p)
		}
		portSet[p] = true
	}
	token := o.Token
	if token == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("mitm: token: %w", err)
		}
		token = base64.RawURLEncoding.EncodeToString(b)
	}
	dial := o.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: defaultDialTimeout}
		dial = d.DialContext
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{
		ca:      o.CA,
		allow:   allow,
		ports:   portSet,
		handler: o.Handler,
		token:   token,
		dial:    dial,
		log:     logger,
		rej:     make(map[string]*Rejection),
	}
	// Proxy is deliberately nil: the upstream transport must reach the real
	// host directly, never through HTTPS_PROXY, which would loop back here.
	s.transport = &http.Transport{
		DialContext:         dial,
		Proxy:               nil,
		TLSClientConfig:     o.UpstreamTLS,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: defaultDialTimeout,
		IdleConnTimeout:     idleTimeout,
	}
	return s, nil
}

// Token returns the Proxy-Authorization secret for this instance.
func (s *Server) Token() string { return s.token }

// Rejections returns the per-host count of client-side certificate refusals,
// sorted by host.
func (s *Server) Rejections() []Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Rejection, 0, len(s.rej))
	for _, r := range s.rej {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b Rejection) int { return strings.Compare(a.Host, b.Host) })
	return out
}

// upstreamKey carries the upstream transport in a decrypted request's context.
type upstreamKey struct{}

// UpstreamTransport returns the transport a decrypted-request handler must use
// to reach the real host. It never uses an HTTPS_PROXY environment setting, so
// the request cannot loop back into this server.
func UpstreamTransport(ctx context.Context) (*http.Transport, bool) {
	tr, ok := ctx.Value(upstreamKey{}).(*http.Transport)
	return tr, ok
}

// ServeHTTP handles CONNECT only. Clients must be on loopback and present the
// Token as Proxy-Authorization Basic (password or username).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodConnect {
		w.Header().Set("Allow", http.MethodConnect)
		http.Error(w, "only CONNECT is accepted", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r.Header.Get("Proxy-Authorization")) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="`+proxyRealm+`"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	authority := r.Host
	if authority == "" {
		authority = r.URL.Host
	}
	host, port, err := parseAuthority(authority)
	if err != nil || !s.ports[port] {
		http.Error(w, "target not allowed", http.StatusForbidden)
		return
	}
	if s.inspects(host) {
		s.intercept(w, host, port)
		return
	}
	s.tunnel(w, r, net.JoinHostPort(host, strconv.Itoa(port)))
}

func (s *Server) intercept(w http.ResponseWriter, host string, port int) {
	conn, buf, err := establish(w)
	if err != nil {
		s.log.Debug("mitm: hijack failed", "host", host, "err", err)
		return
	}
	s.serveDecrypted(bufferedConn{Conn: conn, r: buf.Reader}, host, net.JoinHostPort(host, strconv.Itoa(port)))
}

// serveDecrypted runs the TLS handshake with a minted leaf for host, then serves
// the decrypted HTTP/1.1 or h2 traffic through the injected handler.
func (s *Server) serveDecrypted(conn net.Conn, host, target string) {
	defer conn.Close()
	tc := tls.Server(conn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hi.ServerName != "" && canonicalHost(hi.ServerName) != host {
				return nil, fmt.Errorf("mitm: SNI does not match CONNECT target %q", host)
			}
			return s.ca.Leaf(host)
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	err := tc.HandshakeContext(ctx)
	cancel()
	if err != nil {
		s.noteHandshake(host, err)
		return
	}

	done := make(chan struct{})
	var once sync.Once
	srv := &http.Server{
		Handler:           s.decrypted(host, target),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          log.New(io.Discard, "", 0),
		ConnState: func(_ net.Conn, st http.ConnState) {
			if st == http.StateClosed || st == http.StateHijacked {
				once.Do(func() { close(done) })
			}
		},
	}
	// http.Server.Serve negotiates h2 for a *tls.Conn with ALPN "h2" and
	// otherwise serves HTTP/1.1. The listener yields the one conn and blocks
	// until that conn closes, so Serve does not return early.
	_ = srv.Serve(&oneConn{conn: tc, done: done})
}

func (s *Server) decrypted(host, target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameHost(r.Host, host) {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		r.URL.Scheme = "https"
		r.URL.Host = target
		ctx := context.WithValue(r.Context(), upstreamKey{}, s.transport)
		s.handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tunnel blind-copies bytes between the client and the real target.
func (s *Server) tunnel(w http.ResponseWriter, r *http.Request, target string) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDialTimeout)
	up, err := s.dial(ctx, "tcp", target)
	cancel()
	if err != nil {
		s.log.Debug("mitm: tunnel dial failed", "target", target, "err", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	client, buf, err := establish(w)
	if err != nil {
		_ = up.Close()
		return
	}
	src := bufferedConn{Conn: client, r: buf.Reader}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, src); done <- struct{}{} }()
	go func() { _, _ = io.Copy(src, up); done <- struct{}{} }()
	<-done
	_ = client.Close()
	_ = up.Close()
	<-done
}

func (s *Server) noteHandshake(host string, err error) {
	reason, ok := ClassifyHandshakeError(err)
	if !ok {
		s.log.Debug("mitm: handshake failed", "host", host)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rj := s.rej[host]
	if rj == nil {
		rj = &Rejection{Host: host}
		s.rej[host] = rj
	}
	rj.Count++
	rj.Last = time.Now()
	rj.Reason = reason
	rj.Err = err.Error()
	s.log.Warn("mitm: client rejected the proxy CA", "host", host, "reason", reason)
}

func (s *Server) authorized(header string) bool {
	const prefix = "Basic "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return false
	}
	user, pass, _ := strings.Cut(string(raw), ":")
	return tokenEqual(pass, s.token) || tokenEqual(user, s.token)
}

func (s *Server) inspects(host string) bool {
	for _, a := range s.allow {
		if a.wildcard {
			if strings.HasSuffix(host, "."+a.name) {
				return true
			}
		} else if host == a.name {
			return true
		}
	}
	return false
}

func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func parseAllow(domains []string) ([]allowEntry, error) {
	var out []allowEntry
	for _, d := range domains {
		t := strings.TrimSpace(d)
		if t == "" {
			continue
		}
		wild := strings.HasPrefix(t, wildcardPrefix)
		name, err := dnsName(strings.TrimPrefix(t, wildcardPrefix))
		if err != nil {
			return nil, err
		}
		out = append(out, allowEntry{name: name, wildcard: wild})
	}
	return out, nil
}

// parseAuthority splits a CONNECT authority into a canonical host and port.
func parseAuthority(authority string) (string, int, error) {
	h, p, err := net.SplitHostPort(authority)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("mitm: invalid port in %q", authority)
	}
	host := canonicalHost(h)
	if host == "" {
		return "", 0, fmt.Errorf("mitm: empty host in %q", authority)
	}
	return host, port, nil
}

// sameHost reports whether an HTTP authority (host or host:port) names host.
func sameHost(authority, host string) bool {
	h, _, err := net.SplitHostPort(authority)
	if err != nil {
		h = authority
	}
	return canonicalHost(h) == host
}

func isLoopback(remote string) bool {
	h, _, err := net.SplitHostPort(remote)
	if err != nil {
		h = remote
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// establish hijacks the connection and sends the 200 that opens the tunnel.
func establish(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return nil, nil, err
	}
	if _, err := buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := buf.Flush(); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, buf, nil
}

// bufferedConn reads through r so bytes the client sent before the tunnel
// opened are not lost. Writes go straight to the connection.
type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// oneConn is a net.Listener that yields a single connection, then blocks until
// that connection has closed.
type oneConn struct {
	conn net.Conn
	done <-chan struct{}
	used atomic.Bool
}

func (l *oneConn) Accept() (net.Conn, error) {
	if l.used.CompareAndSwap(false, true) {
		return l.conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConn) Close() error   { return nil }
func (l *oneConn) Addr() net.Addr { return l.conn.LocalAddr() }
