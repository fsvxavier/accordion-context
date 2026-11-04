// Package proxy serves the Anthropic Messages endpoint as a context-compaction
// proxy (spec §19.2 mode 1, §18.2, §16). It is the only place where the HTTP
// traffic, wire/anthropic, session and store meet. Every failure on the
// compaction side is fail-open: the client request is then forwarded unchanged.
package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
	"github.com/fsvxavier/accordion-context/internal/wire/anthropic"
)

const (
	pathMessages   = "/v1/messages"
	pathHealth     = "/__acd-ctx/health"
	pathStatus     = "/__acd-ctx/status"
	headerProject  = "X-Acd-Ctx-Project-Dir"
	headerConv     = "X-Acd-Ctx-Conversation"
	defaultProject = "default"

	defaultContextLimit = 200_000
	defaultMaxLoop      = 4
	maxBodyBytes        = 32 << 20
	maxToolOutput       = 64 << 10
	// recentKeep mirrors session.recentKeep: the messages a compress call never touches.
	recentKeep = 10
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
	ContextLimit int          // default 200000 (no config layer yet)
	MaxLoop      int          // server-side tool rounds per client request, default 4
	Markers      []string     // project root markers, default go.mod, package.json, ...
	Logger       *slog.Logger // default: discard
	Transport    http.RoundTripper
	// UpstreamAllow lists the origins (scheme://host[:port]) reachable through the
	// /ctx/<upstream>/ prefix. Default: the origin of Upstream only.
	UpstreamAllow []string
}

// Server is the http.Handler of the proxy.
type Server struct {
	cfg     Config
	client  *http.Client
	started time.Time
	allow   map[string]bool // origins allowed for /ctx/ targets

	defaultRequests atomic.Int64 // requests that fell back to the default project (§18.2)

	mu       sync.Mutex
	sessions map[string]*session.Session
	ensured  map[string]bool   // project ids already written to the projects table
	roots    map[string]string // project id -> root_path last seen
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
	allow, err := allowSet(cfg.UpstreamAllow, cfg.Upstream)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: cfg.Transport,
		// Redirects are returned to the client as-is, never followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Server{
		cfg: cfg, client: client, started: time.Now(), allow: allow,
		sessions: map[string]*session.Session{},
		ensured:  map[string]bool{},
		roots:    map[string]string{},
	}, nil
}

// ServeHTTP routes internal endpoints, the compaction path and pass-through.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case pathHealth:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	case pathStatus:
		s.status(w)
		return
	}
	rt, status, msg := s.route(r.URL.Path)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	if r.Method == http.MethodPost && (rt.path == pathMessages || rt.viaCtx && strings.HasSuffix(rt.path, pathMessages)) {
		s.messages(w, r, rt)
		return
	}
	s.passthrough(w, r, rt, nil)
}

func (s *Server) status(w http.ResponseWriter) {
	s.mu.Lock()
	loaded, projects := len(s.sessions), len(s.ensured)
	s.mu.Unlock()
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
	})
}

// messages runs the compaction pipeline for POST /v1/messages.
func (s *Server) messages(w http.ResponseWriter, r *http.Request, rt routed) {
	ctx := context.WithoutCancel(r.Context())
	body, err := readLimited(r.Body)
	if err != nil {
		http.Error(w, "request body unreadable or too large", http.StatusRequestEntityTooLarge)
		return
	}

	var meta struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if json.Unmarshal(body, &meta) != nil {
		s.passthrough(w, r, rt, body)
		return
	}
	req, msgs, err := anthropic.Decode(body)
	if err != nil {
		s.failOpen(w, r, rt, body, "decode", err)
		return
	}
	for i := range msgs {
		msgs[i].Tokens = kernel.EstimateTokens(msgs[i].Text)
	}

	proj := s.resolveProject(r, rt.project)
	base := sessionID(proj.ID, r.Header.Get(headerConv), msgs)
	// The walk never returns a session whose ledger differs from the transcript:
	// an edited or colliding transcript gets its own fork (see fork.go).
	sid, sess, fk, err := s.resolveLedger(ctx, proj, base, msgs, meta.Model)
	if err != nil {
		s.failOpen(w, r, rt, body, "session", err)
		return
	}
	if _, err := sess.Ingest(ctx, msgs); err != nil {
		s.failOpen(w, r, rt, body, "ingest", err)
		return
	}
	if fk != nil {
		if err := s.inheritBlocks(ctx, sid, fk); err != nil {
			s.cfg.Logger.Warn("fork did not inherit blocks", "session", sid, "err", err)
		}
	}
	snap, err := s.cfg.Store.Snapshot(ctx, sid)
	if err != nil {
		s.failOpen(w, r, rt, body, "snapshot", err)
		return
	}
	// The client resends the whole transcript, so the size that matters is the
	// rendered window, not the raw transcript.
	visible := 0
	for _, m := range kernel.Render(snap) {
		visible += m.Tokens
	}
	plan, err := sess.Plan(ctx, visible)
	if err != nil {
		s.failOpen(w, r, rt, body, "plan", err)
		return
	}
	window, err := anthropic.Encode(req, plan.Visible)
	if err != nil {
		s.failOpen(w, r, rt, body, "encode", err)
		return
	}
	out, err := anthropic.InjectTools(window)
	if err != nil {
		s.failOpen(w, r, rt, body, "inject", err)
		return
	}
	if plan.Decision.Nudge {
		spans := kernel.CompressibleSpans(snap, recentKeep)
		if nudged, err := appendToLastUser(out, nudgeText(plan.Decision.Voice, spans)); err == nil {
			out = nudged
		} else {
			s.cfg.Logger.Warn("nudge not injected", "err", err)
		}
	}

	t := &turn{
		ctx: ctx, sess: sess, sid: sid, up: rt.up, path: rt.path, stream: meta.Stream, body: out,
		estTokens: visible, proj: proj.ID,
	}
	s.cfg.Logger.Debug("compaction turn", "project", proj.ID, "session", sid,
		"rule", plan.Decision.Row, "nudge", plan.Decision.Nudge, "tokens", visible)
	s.runTurns(w, r, t)
}

// turn is the per-client-request state of the server-side tool loop.
type turn struct {
	ctx       context.Context
	sess      *session.Session
	sid       string
	proj      string
	up        *url.URL // upstream base of this request
	path      string   // upstream path of this request
	stream    bool
	begun     bool   // the client stream has started (headers and bytes sent)
	body      []byte // upstream request body of the current round
	estTokens int
	compress  int // successful compress calls so far in this request
}

// runTurns sends rounds upstream until the model stops asking for compress or
// decompress. Internal tool_use blocks never reach the client.
func (s *Server) runTurns(w http.ResponseWriter, r *http.Request, t *turn) {
	var fw *anthropic.Forwarder
	if t.stream {
		fw = anthropic.NewForwarder(flushWriter{w})
	}
	for round := 0; ; round++ {
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
		if t.stream {
			done = s.streamRound(w, resp, t, round, fw)
		} else {
			done = s.jsonRound(w, resp, t, round)
		}
		if done {
			return
		}
	}
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

// jsonRound handles one non-streaming upstream response. It returns true when
// the response was written to the client.
func (s *Server) jsonRound(w http.ResponseWriter, resp *http.Response, t *turn, round int) bool {
	data, err := readLimited(resp.Body)
	resp.Body.Close()
	if err != nil {
		http.Error(w, "upstream body unreadable", http.StatusBadGateway)
		return true
	}
	if u, ok, err := anthropic.ParseUsage(data); err == nil && ok {
		s.recordCache(t, u)
	}

	var msg struct {
		StopReason string            `json:"stop_reason"`
		Content    []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(data, &msg) != nil {
		writeRaw(w, resp, data)
		return true
	}
	var calls []toolCall
	clientTools := 0
	for _, raw := range msg.Content {
		var b toolUseWire
		if json.Unmarshal(raw, &b) != nil || b.Type != "tool_use" {
			continue
		}
		if isInternal(b.Name) {
			calls = append(calls, toolCall{id: b.ID, name: b.Name, input: b.Input})
		} else {
			clientTools++
		}
	}
	if len(calls) == 0 {
		writeRaw(w, resp, data)
		return true
	}

	results := make([]json.RawMessage, 0, len(calls))
	for _, c := range calls {
		results = append(results, s.runTool(t, c))
	}
	if clientTools > 0 || round+1 >= s.cfg.MaxLoop {
		// The round cannot continue: drop the internal blocks from the answer.
		s.cfg.Logger.Warn("internal tool_use not continued", "session", t.sid,
			"client_tools", clientTools, "round", round)
		out, err := stripInternal(data)
		if err != nil {
			writeRaw(w, resp, data)
			return true
		}
		writeRaw(w, resp, out)
		return true
	}

	next, err := appendTurn(t.body, msg.Content, results)
	if err != nil {
		s.cfg.Logger.Error("append tool round failed", "session", t.sid, "err", err)
		writeRaw(w, resp, data)
		return true
	}
	t.body = next
	return false
}

// streamRound forwards one upstream SSE response as it arrives. Internal blocks
// are withheld and their calls run after the round. When the round continues
// server-side, the client keeps receiving the same message, and only the
// message end waits for the last round.
func (s *Server) streamRound(w http.ResponseWriter, resp *http.Response, t *turn, round int, fw *anthropic.Forwarder) bool {
	defer resp.Body.Close()
	if !t.begun {
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(http.StatusOK)
		t.begun = true
	}
	fw.BeginRound()
	sse := anthropic.NewSSEReader(resp.Body)
	for {
		ev, err := sse.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.cfg.Logger.Error("upstream stream broken", "session", t.sid, "err", err)
			s.abort(w, t, "upstream stream broken", http.StatusBadGateway)
			return true
		}
		if err := fw.Feed(ev); err != nil {
			s.cfg.Logger.Warn("client stream write failed", "session", t.sid, "err", err)
			return true
		}
	}

	res := fw.EndRound()
	s.recordCache(t, res.Usage)
	if len(res.Internal) == 0 {
		if err := fw.Finish(false); err != nil {
			s.cfg.Logger.Warn("client stream write failed", "session", t.sid, "err", err)
		}
		return true
	}

	results := make([]json.RawMessage, 0, len(res.Internal))
	for _, u := range res.Internal {
		results = append(results, s.runTool(t, toolCall{id: u.ID, name: u.Name, input: u.Input}))
	}
	if res.ClientTools > 0 || round+1 >= s.cfg.MaxLoop {
		// The round cannot continue. The internal blocks were already withheld from the client.
		s.cfg.Logger.Warn("internal tool_use not continued", "session", t.sid,
			"client_tools", res.ClientTools, "round", round)
		if err := fw.Finish(res.ClientTools == 0); err != nil {
			s.cfg.Logger.Warn("client stream write failed", "session", t.sid, "err", err)
		}
		return true
	}

	next, err := appendTurn(t.body, res.Content, results)
	if err != nil {
		s.cfg.Logger.Error("append tool round failed", "session", t.sid, "err", err)
		_ = fw.Finish(true)
		return true
	}
	t.body = next
	return false
}

type toolCall struct {
	id    string
	name  string
	input []byte
}

// toolUseWire is the wire form of a tool_use block (also used for replay).
type toolUseWire struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

type toolResultWire struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// runTool executes one internal call and returns its tool_result block.
func (s *Server) runTool(t *turn, c toolCall) json.RawMessage {
	var text string
	var err error
	switch c.name {
	case anthropic.ToolCompress:
		text, err = s.compress(t, c.input)
	case anthropic.ToolDecompress:
		text, err = s.decompress(t, c.input)
	}
	res := toolResultWire{Type: "tool_result", ToolUseID: c.id, Content: text}
	if err != nil {
		res.Content = "error: " + err.Error()
		res.IsError = true
		s.cfg.Logger.Warn("internal tool failed", "tool", c.name, "session", t.sid, "err", err)
	}
	raw, _ := json.Marshal(res)
	return raw
}

func (s *Server) compress(t *turn, input []byte) (string, error) {
	args, err := anthropic.ParseCompressArgs(input)
	if err != nil {
		return "", err
	}
	ranges := make([]session.CompressRange, 0, len(args.Ranges))
	for _, r := range args.Ranges {
		ranges = append(ranges, session.CompressRange{
			StartRef: kernel.FormatRef(r.StartSeq),
			EndRef:   kernel.FormatRef(r.EndSeq),
			Summary:  r.Summary,
			Topic:    r.Topic,
		})
	}
	res, err := t.sess.HandleCompress(t.ctx, session.CompressArgs{Ranges: ranges})
	if err != nil {
		return "", err
	}
	t.compress++
	return fmt.Sprintf("Recorded %d block(s): %s. Tokens after fold: %d.",
		len(res.BlockIDs), strings.Join(res.BlockIDs, ", "), res.TokensAfter), nil
}

// decompress returns the original messages of a block. mode peek filters lines
// by query, context and file return the whole block (capped).
func (s *Server) decompress(t *turn, input []byte) (string, error) {
	var args struct {
		BlockID string `json:"blockId"`
		Mode    string `json:"mode"`
		Query   string `json:"query"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("decompress args: %w", err)
	}
	if args.BlockID == "" {
		return "", errors.New("decompress: blockId is required")
	}
	snap, err := s.cfg.Store.Snapshot(t.ctx, t.sid)
	if err != nil {
		return "", err
	}
	var blk *kernel.Block
	for i := range snap.Blocks {
		if snap.Blocks[i].ID == args.BlockID {
			blk = &snap.Blocks[i]
			break
		}
	}
	if blk == nil {
		return "", fmt.Errorf("decompress: unknown block %q", args.BlockID)
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
		if args.Mode == "peek" && args.Query != "" {
			q := strings.ToLower(args.Query)
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

func (s *Server) recordCache(t *turn, u anthropic.Usage) {
	err := s.cfg.Store.RecordCache(t.ctx, t.sid, store.CacheCall{
		InputTokens:   u.TotalInput(),
		CachedTokens:  u.CacheReadInputTokens,
		EstTokens:     t.estTokens,
		AfterCompress: t.compress > 0,
	})
	if err != nil {
		s.cfg.Logger.Warn("cache ledger write failed", "session", t.sid, "err", err)
	}
}

// failOpen forwards the untouched client body. Compaction problems must never
// break the agent's traffic.
func (s *Server) failOpen(w http.ResponseWriter, r *http.Request, rt routed, body []byte, stage string, err error) {
	s.cfg.Logger.Warn("compaction bypassed", "stage", stage, "err", err)
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
	u.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vv := range r.Header {
		if skipRequestHeader(k) {
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	return s.client.Do(req)
}

// resolveProject applies §18.2: URL prefix, then X-Acd-Ctx-Project-Dir, then default.
func (s *Server) resolveProject(r *http.Request, urlID string) projectRef {
	if dir := strings.TrimSpace(r.Header.Get(headerProject)); dir != "" {
		res, err := project.Resolve(dir, s.cfg.Markers)
		if err == nil {
			s.mu.Lock()
			s.roots[res.ID] = res.Root
			s.mu.Unlock()
			return projectRef{ID: res.ID, Root: res.Root, Storage: res.Storage}
		}
		s.cfg.Logger.Warn("project dir not usable", "err", err)
	}
	if urlID != "" {
		return projectRef{ID: urlID, Root: s.rootOf(urlID), Storage: "global"}
	}
	s.defaultRequests.Add(1)
	s.cfg.Logger.Warn("no project signal; using default project")
	return projectRef{ID: defaultProject, Root: s.rootOf(defaultProject), Storage: "global"}
}

type projectRef struct {
	ID      string
	Root    string
	Storage string
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
func (s *Server) session(ctx context.Context, p projectRef, sid, model string) (*session.Session, error) {
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
		if err := s.cfg.Store.CreateSession(ctx, store.Session{
			ID: sid, ProjectID: p.ID, Model: model, ContextLimit: s.cfg.ContextLimit,
			Mode: "proxy", Integration: "url",
		}); err != nil {
			return nil, err
		}
	}
	sess := session.New(s.cfg.Store, sid)
	s.sessions[sid] = sess
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

func isInternal(name string) bool {
	return anthropic.IsInternalTool(name)
}

// stripInternal removes internal tool_use blocks from a JSON message and turns
// stop_reason tool_use into end_turn when nothing else is pending.
func stripInternal(data []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	var content []json.RawMessage
	if err := json.Unmarshal(top["content"], &content); err != nil {
		return nil, err
	}
	kept := make([]json.RawMessage, 0, len(content))
	pending := false
	for _, raw := range content {
		var b toolUseWire
		if json.Unmarshal(raw, &b) == nil && b.Type == "tool_use" {
			if isInternal(b.Name) {
				continue
			}
			pending = true
		}
		kept = append(kept, raw)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	top["content"] = out
	if !pending {
		var stop string
		if json.Unmarshal(top["stop_reason"], &stop) == nil && stop == "tool_use" {
			top["stop_reason"] = json.RawMessage(`"end_turn"`)
		}
	}
	return json.Marshal(top)
}

// appendTurn adds the assistant tool round and the tool_result user message.
func appendTurn(body []byte, assistant []json.RawMessage, results []json.RawMessage) ([]byte, error) {
	return editMessages(body, func(msgs []json.RawMessage) ([]json.RawMessage, error) {
		a, err := json.Marshal(map[string]any{"role": "assistant", "content": assistant})
		if err != nil {
			return nil, err
		}
		u, err := json.Marshal(map[string]any{"role": "user", "content": results})
		if err != nil {
			return nil, err
		}
		return append(msgs, a, u), nil
	})
}

// appendToLastUser adds a text block to the last user message (upstream only).
func appendToLastUser(body []byte, text string) ([]byte, error) {
	return editMessages(body, func(msgs []json.RawMessage) ([]json.RawMessage, error) {
		if len(msgs) == 0 {
			return nil, errors.New("no messages")
		}
		var last struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(msgs[len(msgs)-1], &last); err != nil {
			return nil, err
		}
		if last.Role != "user" {
			return nil, errors.New("last message is not user")
		}
		block, err := json.Marshal(map[string]string{"type": "text", "text": text})
		if err != nil {
			return nil, err
		}
		last.Content = append(last.Content, block)
		out, err := json.Marshal(last)
		if err != nil {
			return nil, err
		}
		msgs[len(msgs)-1] = out
		return msgs, nil
	})
}

func editMessages(body []byte, fn func([]json.RawMessage) ([]json.RawMessage, error)) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(top["messages"], &msgs); err != nil {
		return nil, err
	}
	msgs, err := fn(msgs)
	if err != nil {
		return nil, err
	}
	arr, err := json.Marshal(msgs)
	if err != nil {
		return nil, err
	}
	top["messages"] = arr
	return json.Marshal(top)
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
