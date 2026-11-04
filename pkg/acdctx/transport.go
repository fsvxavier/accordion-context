package acdctx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/fsvxavier/accordion-context/internal/proxy"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// Transport is an http.RoundTripper that compacts model requests in flight. Requests
// to the endpoints of Anthropic Messages, OpenAI Chat Completions and Responses, and
// Gemini generateContent, on an origin listed in Options.Upstreams, go through the
// proxy's compaction loop. Everything else goes to the base transport unchanged.
type Transport struct {
	base http.RoundTripper
	p    *Proxy
}

// NewTransport wraps base (nil means http.DefaultTransport) with compaction. Use it
// as the Transport of an SDK's HTTP client. Close releases the database.
func NewTransport(base http.RoundTripper, opts Options) (*Transport, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	p, err := newProxy(base, opts)
	if err != nil {
		return nil, err
	}
	return &Transport{base: base, p: p}, nil
}

// RoundTrip implements http.RoundTripper. The request is not modified.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.p.compactable(req) {
		return t.base.RoundTrip(req)
	}
	return t.p.roundTrip(req)
}

// Close releases the database of the transport.
func (t *Transport) Close() error { return t.p.Close() }

// Proxy is the proxy handler, for embedding in an existing HTTP server. Mount it at
// the root or behind http.StripPrefix. It serves the same paths as the proxy binary.
type Proxy struct {
	srv     *proxy.Server
	st      *store.Store
	origins map[string]bool
	cwd     string

	mu     sync.Mutex
	closed bool
}

// Handler builds the proxy handler with opts. Close releases the database.
func Handler(opts Options) (*Proxy, error) {
	return newProxy(nil, opts)
}

func newProxy(base http.RoundTripper, opts Options) (*Proxy, error) {
	opts = opts.withDefaults()
	if opts.DBPath == "" {
		return nil, errors.New("acdctx: Options.DBPath is required")
	}
	origins := map[string]bool{}
	var first *url.URL
	for _, raw := range opts.Upstreams {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("acdctx: upstream %q must be an absolute http(s) origin", raw)
		}
		origins[originKey(u)] = true
		if first == nil {
			first = u
		}
	}
	st, err := openStore(opts)
	if err != nil {
		return nil, err
	}
	cfg := proxy.Config{
		Upstream:      first,
		Store:         st,
		Version:       "acdctx",
		ContextLimit:  opts.ContextLimit,
		MaxLoop:       opts.MaxLoop,
		Logger:        opts.Logger,
		Transport:     base,
		GlobalPath:    opts.ConfigPath,
		UpstreamAllow: opts.Upstreams,
		ListenHost:    opts.ListenHost,
	}
	srv, err := proxy.New(cfg)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("acdctx: proxy: %w", err)
	}
	return &Proxy{srv: srv, st: st, origins: origins, cwd: opts.Cwd}, nil
}

// ServeHTTP implements http.Handler. A request without X-Acd-Ctx-Project-Dir gets the
// project of Options.Cwd.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.cwd != "" && r.Header.Get("X-Acd-Ctx-Project-Dir") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("X-Acd-Ctx-Project-Dir", p.cwd)
	}
	p.srv.ServeHTTP(w, r)
}

// Close releases the database. Later requests fail with a store error.
func (p *Proxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.st.Close()
}

// compactable reports whether req goes through the proxy.
func (p *Proxy) compactable(req *http.Request) bool {
	if req.Method != http.MethodPost || req.URL == nil {
		return false
	}
	if !p.origins[originKey(req.URL)] {
		return false
	}
	return proxy.IsCompactedPath(req.URL.Path)
}

// roundTrip runs req through the proxy handler. The handler writes into a pipe, so
// streamed answers reach the caller as they are produced. The request context
// cancels the handler, and closing the response body does too.
func (p *Proxy) roundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	inner := req.Clone(ctx)
	inner.URL = &url.URL{
		Scheme:   "http",
		Host:     "127.0.0.1",
		Path:     "/ctx/" + originKey(req.URL) + req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}
	inner.Host = "127.0.0.1"
	inner.RequestURI = ""
	if p.cwd != "" && inner.Header.Get("X-Acd-Ctx-Project-Dir") == "" {
		inner.Header.Set("X-Acd-Ctx-Project-Dir", p.cwd)
	}

	pr, pw := io.Pipe()
	rw := &pipeWriter{header: http.Header{}, pw: pw, ready: make(chan struct{})}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				pw.CloseWithError(fmt.Errorf("acdctx: proxy panic: %v", rec))
				return
			}
			rw.WriteHeader(http.StatusOK) // a handler that wrote nothing answers 200
			pw.Close()
		}()
		p.srv.ServeHTTP(rw, inner)
	}()

	select {
	case <-rw.ready:
	case <-ctx.Done():
		cancel()
		pr.CloseWithError(ctx.Err())
		return nil, ctx.Err()
	}
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", rw.status, http.StatusText(rw.status)),
		StatusCode: rw.status,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     rw.header,
		Body:       &cancelBody{ReadCloser: pr, cancel: cancel},
		Request:    req,
	}, nil
}

// pipeWriter is the http.ResponseWriter of the in-process handler call.
type pipeWriter struct {
	header http.Header
	pw     *io.PipeWriter
	ready  chan struct{}
	once   sync.Once
	status int
}

func (w *pipeWriter) Header() http.Header { return w.header }

// WriteHeader fixes the status. Only the first call counts, as in net/http.
func (w *pipeWriter) WriteHeader(code int) {
	w.once.Do(func() {
		w.status = code
		close(w.ready)
	})
}

func (w *pipeWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

// Flush is a no-op: pipe writes are delivered as soon as the reader takes them.
func (w *pipeWriter) Flush() {}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

// originKey is the allowlist key of a URL: lower-case scheme://host[:port].
func originKey(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
