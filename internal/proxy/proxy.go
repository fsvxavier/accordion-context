// Package proxy serves the compacted API endpoints as a context-compaction proxy
// (spec §19.1, §19.2 mode 1, §18.2, §16). The HTTP traffic, the wire codecs,
// session and store meet here. Each dialect is an adapter behind the protocol
// interface (protocol.go), and one server-side loop serves all of them. Every
// failure on the compaction side is fail-open: the client request is then
// forwarded unchanged.
package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/obs"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
	"github.com/fsvxavier/accordion-context/internal/wire/anthropic"
)

const (
	pathHealth     = "/__acd-ctx/health"
	pathStatus     = "/__acd-ctx/status"
	pathInternal   = "/__acd-ctx/"
	headerProject  = "X-Acd-Ctx-Project-Dir"
	headerConv     = "X-Acd-Ctx-Conversation"
	defaultProject = "default"

	defaultContextLimit = 200_000
	defaultMaxLoop      = 4
	maxBodyBytes        = 32 << 20
	maxToolOutput       = 64 << 10
	// maxNudgeSpans caps how many compressible ranges the nudge lists.
	maxNudgeSpans = 8
)

var defaultMarkers = []string{"go.mod", "package.json", "pyproject.toml", "composer.json", "Cargo.toml"}

var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Config wires the proxy. Upstream and Store are required.
type Config struct {
	Upstream     *url.URL
	Store        *store.Store
	Version      string
	ContextLimit int          // window used when the model has no entry, default 200000
	MaxLoop      int          // server-side tool rounds per client request, default 4
	Markers      []string     // project root markers, default go.mod, package.json, ...
	Logger       *slog.Logger // default: discard
	Transport    http.RoundTripper
	// GlobalPath is the global configuration file (spec §18.1). Empty means the
	// user config path. Project files are read per request-project root.
	GlobalPath string
	// ConfigFlags are explicit configuration keys (the highest layer of §18.5).
	ConfigFlags map[string]string
	// ContextWindows maps model name prefixes to context windows, added to the
	// built-in table (builtinWindows).
	ContextWindows map[string]int
	// LookupEnv reads ACD_CTX_* variables. Nil means os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Now is the clock used for idle times and config reloads. Nil means time.Now.
	Now func() time.Time
	// ListenHost is the host part of the listen address. It is accepted in the
	// Host header in addition to the loopback names. Empty when unknown.
	ListenHost string
	// UpstreamAllow lists the origins (scheme://host[:port]) reachable through the
	// /ctx/<upstream>/ prefix. Default: the origin of Upstream only.
	UpstreamAllow []string
	// AllowedHosts, when non-empty, is the exact set of Host names the guard
	// accepts (replacing the loopback defaults and ListenHost). Set by serve for
	// non-loopback listening; see CheckListen.
	AllowedHosts []string
	// WSIdleTimeout closes a WebSocket connection that has had no client frame for
	// this long. Zero means defaultWSIdleTimeout.
	WSIdleTimeout time.Duration
	// WSMaxFrameBytes caps one WebSocket message in either direction. Zero means
	// maxBodyBytes.
	WSMaxFrameBytes int64
}

// Server is the http.Handler of the proxy.
type Server struct {
	cfg     Config
	client  *http.Client
	started time.Time
	allow   map[string]bool // origins allowed for /ctx/ targets
	hosts   map[string]bool // Host header names accepted (DNS-rebinding guard)
	// inherit gives a new fork its parent's blocks. A field so tests can fail it.
	inherit func(ctx context.Context, child string, f *fork) error

	defaultRequests atomic.Int64   // requests that fell back to the default project (§18.2)
	windows         map[string]int // model prefix -> context window, lower-case keys

	mu       sync.Mutex
	sessions map[string]*session.Session
	ensured  map[string]bool         // project ids already written to the projects table
	roots    map[string]string       // project id -> root_path last seen
	tunes    map[string]*tune        // per session: calibration, cache gate state (tune.go)
	cfgs     map[string]cachedConfig // project root ("" = global only) -> configuration
	epoch    int64                   // store epoch the caches above were built at (epoch.go)

	telemetry *telemetry // metrics and recall-hint tracking (obs.go)

	wsMu       sync.Mutex
	wsSessions map[*wsSession]struct{} // live WebSocket sessions (ws.go)
}

// New validates cfg and builds the handler.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("proxy: store is required")
	}
	if cfg.Upstream == nil || (cfg.Upstream.Scheme != "http" && cfg.Upstream.Scheme != "https") || cfg.Upstream.Host == "" {
		return nil, errors.New("proxy: upstream must be an absolute http(s) URL")
	}
	if cfg.ContextLimit <= 0 {
		cfg.ContextLimit = defaultContextLimit
	}
	if cfg.MaxLoop <= 0 {
		cfg.MaxLoop = defaultMaxLoop
	}
	if cfg.Markers == nil {
		cfg.Markers = defaultMarkers
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.WSIdleTimeout <= 0 {
		cfg.WSIdleTimeout = defaultWSIdleTimeout
	}
	if cfg.WSMaxFrameBytes <= 0 {
		cfg.WSMaxFrameBytes = maxBodyBytes
	}
	allow, err := allowSet(cfg.UpstreamAllow, cfg.Upstream)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: cfg.Transport,
		// Redirects are returned to the client as-is, never followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	hosts, err := guardHosts(cfg.ListenHost, cfg.AllowedHosts)
	if err != nil {
		return nil, err
	}
	srv := &Server{
		cfg: cfg, client: client, started: time.Now(), allow: allow, hosts: hosts,
		sessions:  map[string]*session.Session{},
		ensured:   map[string]bool{},
		roots:     map[string]string{},
		tunes:     map[string]*tune{},
		cfgs:      map[string]cachedConfig{},
		windows:   mergedWindows(cfg.ContextWindows),
		telemetry: newTelemetry(),
	}
	srv.inherit = srv.inheritBlocks
	return srv, nil
}

// hostAllowed reports whether a Host header names this loopback proxy. Any other
// name is refused, which stops DNS rebinding from reaching the proxy from a page.
func (s *Server) hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	} else {
		host = strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
	}
	return s.hosts[strings.ToLower(host)]
}

// ServeHTTP checks the request against the loopback guard, then routes internal
// endpoints, the compaction endpoints and pass-through.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodOptions {
		// CORS preflight: the proxy grants no cross-origin access, so answer here.
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, pathInternal) && len(r.Header.Values("Origin")) > 0 {
		// Browser pages may not reach the control endpoints.
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case pathHealth:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	case pathStatus:
		s.status(w)
		return
	case pathMetrics:
		s.metricsEndpoint(w, r)
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, pathSessions); ok {
		s.sessionEndpoint(w, r, rest)
		return
	}
	rt, status, msg := s.route(r.URL.Path)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	if _, ok := protocolFor(rt).(responsesProtocol); ok && isWebSocketUpgrade(r) {
		// WebSocket mode of /v1/responses (ws.go). Other upgrades pass through as before.
		s.serveResponsesWS(w, r, rt)
		return
	}
	if r.Method == http.MethodPost {
		if p := protocolFor(rt); p != nil {
			s.compactTurn(w, r, rt, p)
			return
		}
	}
	s.passthrough(w, r, rt, nil)
}

func (s *Server) status(w http.ResponseWriter) {
	s.mu.Lock()
	loaded, projects := len(s.sessions), len(s.ensured)
	s.mu.Unlock()
	names := make([]string, 0, len(protocols))
	for _, p := range protocols {
		names = append(names, p.Name())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":                   "ok",
		"version":                  s.cfg.Version,
		"uptime_seconds":           int(time.Since(s.started).Seconds()),
		"upstream_host":            s.cfg.Upstream.Host,
		"context_limit":            s.cfg.ContextLimit,
		"max_loop":                 s.cfg.MaxLoop,
		"sessions_loaded":          loaded,
		"projects_seen":            projects,
		"default_project_requests": s.defaultRequests.Load(),
		"protocols":                names,
	})
}

// compactTurn runs the compaction pipeline for one request of a dialect p.
func (s *Server) compactTurn(w http.ResponseWriter, r *http.Request, rt routed, p protocol) {
	started := time.Now()
	defer func() { s.telemetry.m.RequestSeconds.WithLabelValues(p.Name()).Observe(time.Since(started).Seconds()) }()
	ctx := context.WithoutCancel(r.Context())
	body, err := readLimited(r.Body)
	if err != nil {
		http.Error(w, "request body unreadable or too large", http.StatusRequestEntityTooLarge)
		return
	}

	model, stream, err := p.Meta(rt.path, r.URL.RawQuery, body)
	if err != nil {
		s.passthrough(w, r, rt, body)
		return
	}
	req, msgs, err := p.Decode(body)
	if err != nil {
		s.failOpen(w, r, rt, body, "decode", err)
		return
	}
	// The compaction prelude is shared with the WebSocket path (ws.go), which
	// passes frames already decoded by its codec.
	t, stage, err := s.prepareTurn(ctx, r, rt, p, model, req, msgs)
	if err != nil {
		s.failOpen(w, r, rt, body, stage, err)
		return
	}
	t.stream = stream
	s.runTurns(w, r, t)
	s.countRequest(p.Name(), obs.OutcomeCompact)
}

// prepareTurn runs the compaction pipeline for one decoded request: session and
// ledger, ingest, plan, encode, tool injection and the nudge. On failure it
// returns the stage name, and the caller forwards the request unchanged.
func (s *Server) prepareTurn(ctx context.Context, r *http.Request, rt routed, p protocol, model string, req request, msgs []kernel.Message) (*turn, string, error) {
	s.syncEpoch(ctx)
	for i := range msgs {
		msgs[i].Tokens = kernel.EstimateTokens(msgs[i].Text)
	}

	proj := s.resolveProject(r, rt.project)
	cfg := s.configFor(proj.Root)
	base := sessionID(proj.ID, r.Header.Get(headerConv), msgs)
	// The walk never returns a session whose ledger differs from the transcript:
	// an edited or colliding transcript gets its own fork (see fork.go).
	sid, sess, fk, err := s.resolveLedger(ctx, proj, base, msgs, model, s.contextWindow(model, cfg))
	if err != nil {
		return nil, "session", err
	}
	// An existing session follows the configured window (spec §8.1, §18.8).
	if err := s.applyContextLimit(ctx, sid, cfg); err != nil {
		s.cfg.Logger.Warn("context limit not applied; session keeps its stored window", "session", sid, "err", err)
	}
	// The session takes the settings of the project's configuration (spec §18.5).
	sess.SetOptions(sessionOptions(cfg))
	if _, err := sess.Ingest(ctx, msgs); err != nil {
		return nil, "ingest", err
	}
	if fk != nil {
		// Retried on every turn of the fork until it succeeds (see inheritBlocks).
		if err := s.inherit(ctx, sid, fk); err != nil {
			s.cfg.Logger.Warn("fork did not inherit blocks; retry next turn", "session", sid, "err", err)
		}
	}
	snap, err := s.cfg.Store.Snapshot(ctx, sid)
	if err != nil {
		return nil, "snapshot", err
	}
	// The client resends the whole transcript, so the size that matters is the
	// rendered window, after CCR and crush (spec §14), not the raw transcript.
	// Its calibrated size is the TokenCount of the trigger (spec §15).
	rendered := reduceWindow(kernel.Render(snap), cfg.CCRThresholdTokens, cfg.CrushEnabled)
	tokenCount := s.calibrate(sid, sumTokens(rendered))
	s.telemetry.m.ContextTokens.WithLabelValues(p.Name()).Observe(float64(tokenCount))
	// The turn event carries what replay needs to repeat this Plan (spec §17.2 item 4).
	opts := sess.Settings()
	if err := s.cfg.Store.AppendEvent(ctx, sid, "turn", obs.TurnEvent{
		Tokens: tokenCount, Messages: len(snap.Messages), ContextLimit: snap.ContextLimit,
		Settings: obs.SettingsOf(opts.Kernel, opts.RecentKeep, opts.MinCompressRange, opts.MinSummaryChars),
	}); err != nil {
		return nil, "turn", err
	}
	plan, err := sess.Plan(ctx, tokenCount)
	if err != nil {
		return nil, "plan", err
	}
	s.telemetry.turnHints(sid, plan.Hints)
	s.observeTurn(ctx, sid, sess, snap, plan, tokenCount, cfg)
	window := reduceWindow(plan.Visible, cfg.CCRThresholdTokens, cfg.CrushEnabled)
	encoded, err := req.encode(window)
	if err != nil {
		return nil, "encode", err
	}
	out, err := p.InjectTools(encoded)
	if err != nil {
		return nil, "inject", err
	}
	held := s.takeHeld(sid)
	if plan.Decision.Nudge {
		all := nudgeSpans(snap, sess.Settings())
		spans := dropHeld(all, held)
		if len(all) > 0 && len(spans) == 0 {
			// Everything on offer was just expanded by decompress: undo the nudge count.
			if err := sess.DeferNudge(ctx, plan); err != nil {
				s.cfg.Logger.Warn("anti-flap: trigger undo failed", "session", sid, "err", err)
			}
			plan.Decision.Nudge = false
		}
	}
	if plan.Decision.Nudge {
		spans := dropHeld(nudgeSpans(snap, sess.Settings()), held)
		send := s.gateNudge(ctx, sid, gateIn{
			sess: sess, plan: plan, spans: spans, visible: window,
			tokens: tokenCount, limit: snap.ContextLimit, cfg: cfg,
		})
		if send {
			if nudged, err := p.AppendNudge(out, nudgeText(plan.Decision.Voice, spans)); err == nil {
				out = nudged
			} else {
				s.cfg.Logger.Warn("nudge not injected", "protocol", p.Name(), "err", err)
			}
		}
	}

	t := &turn{
		ctx: ctx, sess: sess, sid: sid, proto: p, up: rt.up, path: rt.path, body: out,
		estTokens: sumTokens(window), proj: proj.ID,
	}
	s.cfg.Logger.Debug("compaction turn", "protocol", p.Name(), "project", proj.ID, "session", sid,
		"rule", plan.Decision.Row, "nudge", plan.Decision.Nudge, "tokens", tokenCount, "est_tokens", t.estTokens)
	return t, "", nil

}

// turn is the per-client-request state of the server-side tool loop.
type turn struct {
	ctx       context.Context
	sess      *session.Session
	sid       string
	proj      string
	proto     protocol
	up        *url.URL // upstream base of this request
	path      string   // upstream path of this request
	stream    bool
	begun     bool   // the client stream has started (headers and bytes sent)
	body      []byte // upstream request body of the current round
	estTokens int    // local estimate of the body sent this round, raw (before calibration)
	compress  int    // successful compress calls so far in this request
	foldsSeen int    // compress calls already reflected in a ledger row
}

// grow adds what a finished round appends to the body: the call arguments and
// the results. The estimate tracks the body that the provider counts.
func (t *turn) grow(calls []toolCall, results []toolResult) {
	for _, c := range calls {
		t.estTokens += kernel.EstimateTokens(string(c.input))
	}
	for _, r := range results {
		t.estTokens += kernel.EstimateTokens(r.text)
	}
}

// runTurns sends rounds upstream until the model stops asking for compress or
// decompress. Internal calls never reach the client.
func (s *Server) runTurns(w http.ResponseWriter, r *http.Request, t *turn) {
	var fw *anthropic.Forwarder
	if t.stream && t.proto.Live() {
		fw = anthropic.NewForwarder(flushWriter{w})
	}
	for round := 0; ; round++ {
		s.observeCall(t.sid)
		resp, err := s.upstreamCall(r, t.up, t.path, t.body)
		if err != nil {
			s.cfg.Logger.Error("upstream call failed", "session", t.sid, "err", err)
			s.abort(w, t, "upstream unavailable", http.StatusBadGateway)
			return
		}
		if resp.StatusCode != http.StatusOK {
			s.upstreamError(w, resp, t)
			return
		}
		var done bool
		if fw != nil {
			done = s.streamRound(w, resp, t, round, fw)
		} else {
			done = s.bufferedRound(w, resp, t, round)
		}
		if done {
			return
		}
	}
}

// bufferedRound reads one whole upstream answer and decides whether the loop goes
// on. Nothing reaches the client before the final round, so internal calls never
// show. It returns true when the answer was written to the client.
func (s *Server) bufferedRound(w http.ResponseWriter, resp *http.Response, t *turn, round int) bool {
	data, err := readLimited(resp.Body)
	resp.Body.Close()
	if err != nil {
		s.abort(w, t, "upstream body unreadable", http.StatusBadGateway)
		return true
	}
	rr := t.proto.Read(data, t.stream)
	if rr.usage != nil {
		s.recordCache(t, *rr.usage)
	}
	if t.stream && !rr.complete {
		// The upstream closed the stream before its terminal event: the round is cut
		// short and must not be finished as if it were complete.
		s.cfg.Logger.Error("upstream stream truncated", "session", t.sid, "round", round)
		s.abort(w, t, "upstream stream truncated", http.StatusBadGateway)
		return true
	}
	if len(rr.calls) == 0 {
		s.send(w, resp, t, rr.final)
		return true
	}

	results := make([]toolResult, 0, len(rr.calls))
	for _, c := range rr.calls {
		results = append(results, s.runTool(t, c))
	}
	if rr.clientCalls > 0 || round+1 >= s.cfg.MaxLoop {
		// The round cannot continue: the internal calls are removed from the answer.
		s.cfg.Logger.Warn("internal tool_use not continued", "protocol", t.proto.Name(), "session", t.sid,
			"client_tools", rr.clientCalls, "round", round)
		s.send(w, resp, t, rr.final)
		return true
	}
	next, err := t.proto.Continue(t.body, rr, results)
	if err != nil {
		s.cfg.Logger.Error("append tool round failed", "session", t.sid, "err", err)
		s.send(w, resp, t, rr.final)
		return true
	}
	t.grow(rr.calls, results)
	t.body = next
	return false
}

// send writes the final answer of a round to the client. A stream starts with
// its first write, so headers of a stream that is still being compacted are not sent.
func (s *Server) send(w http.ResponseWriter, resp *http.Response, t *turn, body []byte) {
	if !t.stream {
		writeRaw(w, resp, body)
		return
	}
	if !t.begun {
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(http.StatusOK)
		t.begun = true
	}
	_, _ = flushWriter{w}.Write(body)
}

// abort ends a request that failed before anything reached the client, or, once
// the stream has started, with an SSE error event.
func (s *Server) abort(w http.ResponseWriter, t *turn, msg string, status int) {
	if !t.begun {
		http.Error(w, msg, status)
		return
	}
	data, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": msg}})
	writeSSEError(w, data)
}

// upstreamError relays a non-200 answer. Once the stream has started, the body
// goes out as an SSE error event instead.
func (s *Server) upstreamError(w http.ResponseWriter, resp *http.Response, t *turn) {
	defer resp.Body.Close()
	if !t.begun {
		relay(w, resp)
		return
	}
	body, err := readLimited(resp.Body)
	var buf bytes.Buffer
	if err == nil && json.Compact(&buf, body) == nil && json.Valid(buf.Bytes()) {
		writeSSEError(w, buf.Bytes())
		return
	}
	s.abort(w, t, "upstream error", http.StatusBadGateway)
}

func writeSSEError(w http.ResponseWriter, data []byte) {
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
}

// runTool executes one internal call and returns its result for the dialect.
func (s *Server) runTool(t *turn, c toolCall) toolResult {
	res := toolResult{id: c.id, name: c.name}
	text, err := s.execTool(t, c)
	if err != nil {
		res.text = "error: " + err.Error()
		res.isErr = true
		s.cfg.Logger.Warn("internal tool failed", "tool", c.name, "session", t.sid, "err", err)
		return res
	}
	res.text = text
	return res
}

func (s *Server) execTool(t *turn, c toolCall) (string, error) {
	args, err := t.proto.ParseTool(c.name, c.input)
	if err != nil {
		return "", err
	}
	switch c.name {
	case toolCompress:
		return s.compress(t, args)
	case toolDecompress:
		return s.decompress(t, args)
	}
	return "", fmt.Errorf("unknown tool %q", c.name)
}

func (s *Server) compress(t *turn, args toolArgs) (string, error) {
	cargs := session.CompressArgs{
		Ranges: args.ranges, Folds: args.folds, CloseIntents: args.closeIntents,
	}
	res, err := t.sess.HandleCompress(t.ctx, cargs)
	s.observeCompress(t.ctx, t.sid, cargs, res, err)
	if err != nil {
		return "", err
	}
	t.compress++
	return fmt.Sprintf("Recorded %d block(s): %s. Tokens after fold: %d.",
		len(res.BlockIDs), strings.Join(res.BlockIDs, ", "), res.TokensAfter), nil
}

// decompress expands a block (mode context, the default): its sources come back and
// the block is deactivated, which the next window shows (spec §13.1). A message
// ref is recalled. mode peek returns the original lines that match a query, and
// file returns the whole block text (capped).
func (s *Server) decompress(t *turn, args toolArgs) (string, error) {
	if args.block == "" {
		return "", errors.New("decompress: blockId is required")
	}
	snap, err := s.cfg.Store.Snapshot(t.ctx, t.sid)
	if err != nil {
		return "", err
	}
	var blk *kernel.Block
	for i := range snap.Blocks {
		if snap.Blocks[i].ID == args.block {
			blk = &snap.Blocks[i]
			break
		}
	}
	if blk == nil {
		if msgRef.MatchString(args.block) {
			// A message reference from a CCR placeholder (ccr.go): the stored original.
			text, err := t.sess.Recall(t.ctx, args.block)
			if err != nil {
				return "", err
			}
			return refExcerpt(args.block, text, args), nil
		}
		return "", fmt.Errorf("decompress: unknown block %q", args.block)
	}
	if args.mode == "" || args.mode == "context" {
		res, err := t.sess.Decompress(t.ctx, args.block)
		if err != nil {
			return "", err
		}
		s.holdSpan(t.sid, blk.StartSeq, blk.EndSeq)
		s.telemetry.markActed(t.sid, args.block)
		return fmt.Sprintf("Expanded %s into %s; reactivated: %s. Their messages are visible again.",
			args.block, res.Target, strings.Join(res.Reactivated, ", ")), nil
	}

	var b strings.Builder
	for _, m := range snap.Messages {
		if m.Seq < blk.StartSeq || m.Seq > blk.EndSeq {
			continue
		}
		text, err := t.sess.Recall(t.ctx, m.Ref)
		if err != nil {
			return "", err
		}
		if args.mode == "peek" && args.query != "" {
			q := strings.ToLower(args.query)
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(strings.ToLower(line), q) {
					fmt.Fprintf(&b, "[%s] %s\n", m.Ref, line)
				}
			}
		} else {
			fmt.Fprintf(&b, "[%s] %s\n\n", m.Ref, text)
		}
		if b.Len() > maxToolOutput {
			break
		}
	}
	out := strings.ToValidUTF8(b.String(), "")
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput] + "\n[truncated]"
	}
	if out == "" {
		out = "(no matching excerpts)"
	}
	return out, nil
}

func (s *Server) recordCache(t *turn, u usage) {
	folded := t.compress > t.foldsSeen
	t.foldsSeen = t.compress
	err := s.cfg.Store.RecordCache(t.ctx, t.sid, store.CacheCall{
		InputTokens:   u.input,
		CachedTokens:  u.cached,
		EstTokens:     t.estTokens,
		AfterCompress: folded,
	})
	if err != nil {
		s.cfg.Logger.Warn("cache ledger write failed", "session", t.sid, "err", err)
	}
	// The sample pairs the raw estimate with the reported input (spec §15).
	s.observeUsage(t.sid, t.estTokens, u.input, folded)
	s.observeCache(t.sid, u)
}

// failOpen forwards the untouched client body. Compaction problems must never
// break the agent's traffic.
func (s *Server) failOpen(w http.ResponseWriter, r *http.Request, rt routed, body []byte, stage string, err error) {
	s.cfg.Logger.Warn("compaction bypassed", "stage", stage, "err", err)
	if p := protocolFor(rt); p != nil {
		s.countRequest(p.Name(), obs.OutcomeFailOpen)
	}
	s.passthrough(w, r, rt, body)
}

// passthrough forwards a request unchanged and streams the answer back.
func (s *Server) passthrough(w http.ResponseWriter, r *http.Request, rt routed, body []byte) {
	if body == nil {
		b, err := readLimited(r.Body)
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
	}
	resp, err := s.upstreamCall(r, rt.up, rt.path, body)
	if err != nil {
		s.cfg.Logger.Error("upstream call failed", "path", rt.path, "err", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(flushWriter{w}, resp.Body)
}

func (s *Server) upstreamCall(r *http.Request, up *url.URL, path string, body []byte) (*http.Response, error) {
	u := *up
	u.Path = strings.TrimRight(up.Path, "/") + path
	u.RawPath = ""
	if esc := escapedTail(r.URL, path); esc != "" {
		// Keep %2F and friends as the client sent them.
		u.RawPath = strings.TrimRight(up.EscapedPath(), "/") + esc
	}
	u.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("build upstream request failed")
	}
	for k, vv := range r.Header {
		if skipRequestHeader(k) {
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	started := time.Now()
	resp, err := s.client.Do(req)
	s.observeUpstream(time.Since(started))
	if err != nil {
		// url.Error embeds the full URL, query string included (API keys travel
		// there for some providers). Keep it out of logs and error strings.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = fmt.Errorf("%s upstream %s: %w", ue.Op, up.Host, ue.Err)
		}
		return nil, err
	}
	return resp, nil
}

// resolveProject applies §18.2: URL prefix, then X-Acd-Ctx-Project-Dir, then default.
func (s *Server) resolveProject(r *http.Request, urlID string) projectRef {
	if dir := strings.TrimSpace(r.Header.Get(headerProject)); dir != "" {
		if res, ok := s.projectFromDir(dir); ok {
			s.mu.Lock()
			s.roots[res.ID] = res.Root
			s.mu.Unlock()
			return projectRef{ID: res.ID, Root: res.Root, Storage: res.Storage}
		}
	}
	if urlID != "" {
		return projectRef{ID: urlID, Root: s.rootOf(urlID), Storage: "global"}
	}
	s.defaultRequests.Add(1)
	s.cfg.Logger.Warn("no project signal; using default project")
	return projectRef{ID: defaultProject, Root: s.rootOf(defaultProject), Storage: "global"}
}

// projectFromDir resolves a client-supplied directory without writing anything.
// The directory must be absolute and exist; otherwise the header is ignored.
func (s *Server) projectFromDir(dir string) (project.Resolved, bool) {
	if !filepath.IsAbs(dir) {
		s.cfg.Logger.Warn("project dir ignored: not absolute")
		return project.Resolved{}, false
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		s.cfg.Logger.Warn("project dir ignored: not an existing directory")
		return project.Resolved{}, false
	}
	res, err := project.ResolveWith(dir, s.cfg.Markers, project.Options{ReadOnly: true, IDByRoot: s.idByRoot})
	if err != nil {
		s.cfg.Logger.Warn("project dir not usable", "err", err)
		return project.Resolved{}, false
	}
	return res, true
}

type projectRef struct {
	ID      string
	Root    string
	Storage string
}

// idByRoot returns the project id registered at root. A project moved with
// `project move` keeps its id, so its history follows it (spec §18.2).
func (s *Server) idByRoot(root string) (string, bool) {
	id, err := s.cfg.Store.ProjectIDByRoot(context.Background(), root)
	return id, err == nil
}

// rootOf returns the root_path last seen for id. Projects known only by id get a
// synthetic root, because projects.root_path is UNIQUE.
func (s *Server) rootOf(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if root, ok := s.roots[id]; ok {
		return root
	}
	return "urn:acd-ctx:project:" + id
}

// session returns the cached session, creating the project and session rows on
// first use.
func (s *Server) session(ctx context.Context, p projectRef, sid, model string, limit int) (*session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sid]; ok {
		return sess, nil
	}
	if !s.ensured[p.ID] {
		if err := s.cfg.Store.EnsureProject(ctx, p.ID, p.Root, p.Storage, time.Now().Unix()); err != nil {
			return nil, err
		}
		s.ensured[p.ID] = true
	}
	if _, err := s.cfg.Store.Snapshot(ctx, sid); err != nil {
		// Only a missing session is created. Any other failure must not
		// overwrite or shadow a session that may exist.
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err := s.cfg.Store.CreateSession(ctx, store.Session{
			ID: sid, ProjectID: p.ID, Model: model, ContextLimit: limit,
			Mode: "proxy", Integration: "url",
		}); err != nil {
			return nil, err
		}
	}
	sess := session.New(s.cfg.Store, sid)
	s.sessions[sid] = sess
	s.seedCalibLocked(ctx, sid)
	return sess, nil
}

// sessionID hashes the project with the conversation header, or with the first
// user message when the client sends no header.
func sessionID(projectID, conv string, msgs []kernel.Message) string {
	h := sha256.New()
	h.Write([]byte(projectID))
	h.Write([]byte{0})
	if conv != "" {
		h.Write([]byte("conv\x00"))
		h.Write([]byte(conv))
	} else {
		h.Write([]byte("first\x00"))
		for _, m := range msgs {
			if m.Role == kernel.RoleUser && m.Kind == kernel.KindText {
				h.Write([]byte(m.Text))
				break
			}
		}
	}
	return "s_" + hex.EncodeToString(h.Sum(nil))[:32]
}

// nudgeText asks the model to compress and lists the ranges that are open now.
func nudgeText(v kernel.Voice, spans []kernel.Span) string {
	var b strings.Builder
	b.WriteString("<context_nudge>Nota de eficiencia, nao e alerta de limite. Faixas ja concluidas " +
		"podem ser resumidas com a ferramenta compress; recuse se estiver no meio de algo critico. " +
		"Cada mensagem comeca com sua referencia, por exemplo [m00042].")
	if len(spans) > 0 {
		b.WriteString(" Faixas comprimiveis agora (inicio..fim, tokens aproximados):")
		for i, sp := range spans {
			if i == maxNudgeSpans {
				fmt.Fprintf(&b, " e mais %d", len(spans)-i)
				break
			}
			fmt.Fprintf(&b, " %s..%s (%d tok);", sp.Start, sp.End, sp.Tokens)
		}
	}
	b.WriteString(" Voz: " + string(v) + "</context_nudge>")
	return b.String()
}

func readLimited(rd io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(rd, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBodyBytes {
		return nil, errors.New("body too large")
	}
	return b, nil
}

var hopByHop = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true,
	"te": true, "trailer": true, "transfer-encoding": true, "upgrade": true,
}

func skipRequestHeader(k string) bool {
	lk := strings.ToLower(k)
	if hopByHop[lk] || strings.HasPrefix(lk, "x-acd-ctx-") {
		return true
	}
	switch lk {
	case "host", "content-length", "accept-encoding":
		// Accept-Encoding is dropped so the transport decompresses responses itself.
		return true
	}
	return false
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		lk := strings.ToLower(k)
		if hopByHop[lk] || lk == "content-length" || lk == "content-encoding" {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// writeRaw sends a body with the upstream headers and status.
func writeRaw(w http.ResponseWriter, resp *http.Response, body []byte) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// relay copies an upstream response unchanged (used for non-200 answers).
func relay(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(flushWriter{w}, resp.Body)
}

type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// escapedTail returns the part of u's escaped path that decodes to path, when
// path is a suffix of the decoded path (routing only strips prefixes). It is ""
// when the request had no escapes or the suffix cannot be located.
func escapedTail(u *url.URL, path string) string {
	if u.RawPath == "" || !strings.HasSuffix(u.Path, path) {
		return ""
	}
	esc := u.EscapedPath()
	skip := len(u.Path) - len(path) // decoded bytes to drop
	i := 0
	for ; skip > 0 && i < len(esc); skip-- {
		if esc[i] == '%' {
			i += 3
		} else {
			i++
		}
	}
	if skip != 0 || i > len(esc) {
		return ""
	}
	return esc[i:]
}
