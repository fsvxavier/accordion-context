// Package plugin serves the HTTP loopback plugin protocol (spec §19.4) and the
// stdio MCP companion (spec §19.3, mode 5). Both sit on internal/session and
// internal/store. Writes go through session and store only; search and session
// discovery use a read-only handle on the same database file.
package plugin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

const (
	pathPrefix   = "/__acd-ctx/plugin/"
	pathManifest = pathPrefix + "manifest"
	pathRuntime  = pathPrefix + "runtime-info"
	pathTurn     = pathPrefix + "turn"
	pathTool     = pathPrefix + "tool"
	pathStatus   = pathPrefix + "status"
	pathCompact  = pathPrefix + "compact"

	protocolName    = "acd-ctx-plugin"
	protocolVersion = 1

	// maxBodyBytes bounds every request body. A turn carries the full client
	// transcript, so the limit matches the proxy's.
	maxBodyBytes        = 32 << 20
	maxIDLen            = 256
	maxModelLen         = 256
	defaultContextLimit = 200_000
	minTokenLen         = 32
	defaultModel        = "unknown"
)

const doctrine = "Summarize old ranges with compress before the window fills. " +
	"Recover detail with decompress, retrieve or search_context. " +
	"Never write compression markers in your own text."

// Config wires the plugin handler. Store and DBPath name the same database.
type Config struct {
	Store   *store.Store     // required; the caller owns it and closes it
	Project project.Resolved // required; every session of this instance belongs to it
	// Token is the bearer token for every /__acd-ctx/plugin/ endpoint. Required,
	// at least 32 characters. Generate it with NewToken.
	Token      string
	ListenHost string // host of the listen address, accepted in the Host header
	// AllowedHosts, when non-empty, is the exact Host name set (already normalized
	// by the caller, see proxy.NormalizeHosts); it replaces the defaults.
	AllowedHosts []string
	Version      string
	// ContextLimit is used when runtime-info names no window. Default 200000.
	ContextLimit int
	// ExportDir receives the files of decompress mode file. Default: the
	// project's exports directory (see exportsDir).
	ExportDir string
	Logger    *slog.Logger // default: discard
}

// runtimeInfo is what runtime-info reports. It applies when a session is created.
type runtimeInfo struct {
	model  string
	window int
}

// Handler is the http.Handler of the plugin protocol. It is safe for concurrent use.
type Handler struct {
	cfg   Config
	hosts map[string]bool // Host header names accepted (DNS-rebinding guard)
	tools tools

	mu       sync.Mutex
	sessions map[string]*session.Session // by store session id
	epoch    int64                       // store epoch the sessions map was built at (epoch.go)
}

// New validates cfg, ensures the project row and builds the handler.
func New(cfg Config) (*Handler, error) {
	if cfg.Store == nil {
		return nil, errors.New("plugin: store is required")
	}
	if cfg.Project.ID == "" {
		return nil, errors.New("plugin: project is required")
	}
	if len(cfg.Token) < minTokenLen {
		return nil, fmt.Errorf("plugin: token must be at least %d characters", minTokenLen)
	}
	if cfg.ContextLimit <= 0 {
		cfg.ContextLimit = defaultContextLimit
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.ExportDir == "" {
		if dir, err := exportsDir(cfg.Project); err == nil {
			cfg.ExportDir = dir
		}
	}
	if err := cfg.Store.EnsureProject(context.Background(), cfg.Project.ID, cfg.Project.Root,
		cfg.Project.Storage, nowUnix()); err != nil {
		return nil, fmt.Errorf("plugin: project row: %w", err)
	}
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if h := strings.ToLower(cfg.ListenHost); h != "" {
		hosts[h] = true
	}
	if len(cfg.AllowedHosts) > 0 {
		hosts = map[string]bool{}
		for _, h := range cfg.AllowedHosts {
			hosts[strings.ToLower(h)] = true
		}
	}
	return &Handler{
		cfg: cfg, hosts: hosts,
		tools:    tools{st: cfg.Store, exports: cfg.ExportDir},
		sessions: map[string]*session.Session{},
	}, nil
}

// ServeHTTP enforces the loopback guards, authenticates and routes the endpoints.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.hostAllowed(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodOptions || len(r.Header.Values("Origin")) > 0 {
		// Browser pages get no cross-origin access to the control endpoints.
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		http.NotFound(w, r)
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="acd-ctx"`)
		writeErr(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}
	h.syncEpoch(r.Context())
	switch r.URL.Path {
	case pathManifest:
		h.onlyMethod(w, r, http.MethodGet, h.handleManifest)
	case pathRuntime:
		h.onlyMethod(w, r, http.MethodPost, h.handleRuntimeInfo)
	case pathTurn:
		h.onlyMethod(w, r, http.MethodPost, h.handleTurn)
	case pathTool:
		h.onlyMethod(w, r, http.MethodPost, h.handleTool)
	case pathStatus:
		h.onlyMethod(w, r, http.MethodGet, h.handleStatus)
	case pathCompact:
		h.onlyMethod(w, r, http.MethodPost, h.handleCompact)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) onlyMethod(w http.ResponseWriter, r *http.Request, method string, next http.HandlerFunc) {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	next(w, r)
}

// hostAllowed reports whether a Host header names this loopback instance.
func (h *Handler) hostAllowed(hostport string) bool {
	host := hostport
	if hh, _, err := net.SplitHostPort(hostport); err == nil {
		host = hh
	} else {
		host = strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
	}
	return h.hosts[strings.ToLower(host)]
}

// authorized compares the bearer token in constant time.
func (h *Handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got[len(prefix):]), []byte(h.cfg.Token)) == 1
}

func (h *Handler) handleManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol":        protocolName,
		"protocolVersion": protocolVersion,
		"version":         h.cfg.Version,
		"tools":           toolSpecs,
		"doctrine":        doctrine,
	})
}

type runtimeReq struct {
	ConversationID  string `json:"conversationId"`
	Model           string `json:"model"`
	ContextWindow   int    `json:"contextWindow"`
	MaxOutputTokens int    `json:"maxOutputTokens"`
	Cwd             string `json:"cwd"`
}

// handleRuntimeInfo records what the client knows about the model. Window and
// model apply when the session is created; later values are recorded as events.
func (h *Handler) handleRuntimeInfo(w http.ResponseWriter, r *http.Request) {
	var req runtimeReq
	if !decodeBody(w, r, &req) {
		return
	}
	if !validID(req.ConversationID) {
		writeErr(w, http.StatusBadRequest, "conversationId is required (max 256 characters)")
		return
	}
	if len(req.Model) > maxModelLen || req.ContextWindow < 0 || req.MaxOutputTokens < 0 {
		writeErr(w, http.StatusBadRequest, "invalid runtime-info values")
		return
	}
	ctx := r.Context()
	_, sid, created, err := h.get(ctx, req.ConversationID, &runtimeInfo{model: req.Model, window: req.ContextWindow})
	if err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	if err := h.cfg.Store.AppendEvent(ctx, sid, "runtime_info", map[string]any{
		"model": req.Model, "contextWindow": req.ContextWindow,
		"maxOutputTokens": req.MaxOutputTokens, "cwd": req.Cwd,
	}); err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": sid, "created": created})
}

type wireMessage struct {
	Ref        string `json:"ref,omitempty"` // output only
	Role       string `json:"role"`
	Kind       string `json:"kind"`
	ToolName   string `json:"toolName,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Text       string `json:"text"`
	Tokens     int    `json:"tokens"`
}

type turnReq struct {
	ConversationID string        `json:"conversationId"`
	TokenCount     int           `json:"tokenCount"`
	Messages       []wireMessage `json:"messages"`
}

type turnResp struct {
	Ingested int           `json:"ingested"`
	LowWater int           `json:"lowWater"`
	Decision decisionJSON  `json:"decision"`
	Hints    []hintJSON    `json:"hints"`
	Visible  []wireMessage `json:"visible"`
}

type decisionJSON struct {
	Nudge  bool   `json:"nudge"`
	Tier   int    `json:"tier"`
	Voice  string `json:"voice"`
	Row    string `json:"row"`
	Reason string `json:"reason"`
}

type hintJSON struct {
	Kind    string `json:"kind"`
	BlockID string `json:"blockId,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Path    string `json:"path,omitempty"`
	Text    string `json:"text"`
}

// handleTurn ingests the client transcript and returns the turn plan (spec §19.4).
func (h *Handler) handleTurn(w http.ResponseWriter, r *http.Request) {
	var req turnReq
	if !decodeBody(w, r, &req) {
		return
	}
	if !validID(req.ConversationID) {
		writeErr(w, http.StatusBadRequest, "conversationId is required (max 256 characters)")
		return
	}
	msgs := make([]kernel.Message, 0, len(req.Messages))
	total := 0
	for i, wm := range req.Messages {
		m, err := fromWire(wm)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("message %d: %v", i+1, err))
			return
		}
		total += m.Tokens
		msgs = append(msgs, m)
	}
	ctx := r.Context()
	sess, _, _, err := h.get(ctx, req.ConversationID, &runtimeInfo{})
	if err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	added, err := sess.Ingest(ctx, msgs)
	if err != nil {
		// Ingest refuses a transcript that diverges from the stored ledger.
		if strings.Contains(err.Error(), "diverges") {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	tokenCount := req.TokenCount
	if tokenCount <= 0 {
		tokenCount = total
	}
	plan, err := sess.Plan(ctx, tokenCount)
	if err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	resp := turnResp{
		Ingested: added, LowWater: plan.LowWater,
		Decision: decisionJSON{
			Nudge: plan.Decision.Nudge, Tier: plan.Decision.Tier, Voice: string(plan.Decision.Voice),
			Row: plan.Decision.Row, Reason: plan.Decision.Reason,
		},
		Hints:   make([]hintJSON, 0, len(plan.Hints)),
		Visible: make([]wireMessage, 0, len(plan.Visible)),
	}
	for _, hn := range plan.Hints {
		resp.Hints = append(resp.Hints, hintJSON{
			Kind: string(hn.Kind), BlockID: hn.BlockID, Ref: hn.Ref, Path: hn.Path, Text: hn.Text,
		})
	}
	for _, m := range plan.Visible {
		resp.Visible = append(resp.Visible, toWire(m))
	}
	writeJSON(w, http.StatusOK, resp)
}

type compactReq struct {
	ConversationID string `json:"conversationId"`
}

// handleCompact is POST compact: a user-requested compaction (spec §19.4). It
// offers the compressible spans of the conversation and arms a nudge for the
// next turn. An unknown conversation is 404.
func (h *Handler) handleCompact(w http.ResponseWriter, r *http.Request) {
	var req compactReq
	if !decodeBody(w, r, &req) {
		return
	}
	if !validID(req.ConversationID) {
		writeErr(w, http.StatusBadRequest, "conversationId is required (max 256 characters)")
		return
	}
	ctx := r.Context()
	sess, sid, _, err := h.get(ctx, req.ConversationID, nil)
	if err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	res, err := sess.Compact(ctx, session.CompactOptions{})
	if err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessionId":    sid,
		"nudgePending": res.NudgePending,
		"spans":        session.SpanPayload(res.Spans),
	})
}

type toolReq struct {
	ConversationID string          `json:"conversationId"`
	Name           string          `json:"name"`
	Arguments      json.RawMessage `json:"arguments"`
}

// handleTool runs one tool call against the conversation's session.
func (h *Handler) handleTool(w http.ResponseWriter, r *http.Request) {
	var req toolReq
	if !decodeBody(w, r, &req) {
		return
	}
	if !validID(req.ConversationID) {
		writeErr(w, http.StatusBadRequest, "conversationId is required (max 256 characters)")
		return
	}
	ctx := r.Context()
	sess, sid, _, err := h.get(ctx, req.ConversationID, nil)
	if err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	res, err := h.tools.call(ctx, sess, sid, req.Name, req.Arguments)
	if err != nil {
		writeToolErr(w, h.cfg.Logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res})
}

// handleStatus is GET /status?conversationId=...; the same data as ctx_status.
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	conv := r.URL.Query().Get("conversationId")
	if !validID(conv) {
		writeErr(w, http.StatusBadRequest, "conversationId is required (max 256 characters)")
		return
	}
	ctx := r.Context()
	sess, sid, _, err := h.get(ctx, conv, nil)
	if err != nil {
		writeStoreErr(w, h.cfg.Logger, err)
		return
	}
	res, err := h.tools.call(ctx, sess, sid, "ctx_status", nil)
	if err != nil {
		writeToolErr(w, h.cfg.Logger, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// get returns the session of conv. With create set, a missing session is created
// with the given runtime info. Without it, a missing session is errUnknownConv.
func (h *Handler) get(ctx context.Context, conv string, create *runtimeInfo) (*session.Session, string, bool, error) {
	sid := sessionID(h.cfg.Project.ID, conv)
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[sid]; ok {
		return s, sid, false, nil
	}
	created := false
	_, err := h.cfg.Store.Snapshot(ctx, sid)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		if create == nil {
			return nil, sid, false, errUnknownConv
		}
		model := create.model
		if model == "" {
			model = defaultModel
		}
		limit := create.window
		if limit <= 0 {
			limit = h.cfg.ContextLimit
		}
		if err := h.cfg.Store.CreateSession(ctx, store.Session{
			ID: sid, ProjectID: h.cfg.Project.ID, Model: model, ContextLimit: limit,
			Mode: "plugin", Integration: "plugin",
		}); err != nil {
			return nil, sid, false, err
		}
		created = true
	default:
		return nil, sid, false, err
	}
	s := session.New(h.cfg.Store, sid)
	h.sessions[sid] = s
	return s, sid, created, nil
}

// sessionID derives the store id of a plugin conversation within a project.
func sessionID(projectID, conv string) string {
	h := sha256.New()
	h.Write([]byte(projectID))
	h.Write([]byte{0})
	h.Write([]byte(conv))
	return "plg-" + hex.EncodeToString(h.Sum(nil))[:32]
}

func fromWire(wm wireMessage) (kernel.Message, error) {
	m := kernel.Message{
		Role: kernel.Role(wm.Role), Kind: kernel.Kind(wm.Kind),
		ToolName: wm.ToolName, ToolCallID: wm.ToolCallID, Text: wm.Text, Tokens: wm.Tokens,
	}
	switch m.Role {
	case kernel.RoleSystem, kernel.RoleUser, kernel.RoleAssistant, kernel.RoleTool:
	default:
		return m, fmt.Errorf("unknown role %q", wm.Role)
	}
	switch m.Kind {
	case kernel.KindText, kernel.KindToolCall, kernel.KindToolResult, kernel.KindReasoning, kernel.KindOpaque:
	default:
		return m, fmt.Errorf("unknown kind %q", wm.Kind)
	}
	if m.Tokens < 0 {
		return m, errors.New("negative token count")
	}
	if m.Tokens == 0 {
		m.Tokens = kernel.EstimateTokens(m.Text)
	}
	return m, nil
}

func toWire(m kernel.Message) wireMessage {
	return wireMessage{
		Ref: m.Ref, Role: string(m.Role), Kind: string(m.Kind),
		ToolName: m.ToolName, ToolCallID: m.ToolCallID, Text: m.Text, Tokens: m.Tokens,
	}
}

func validID(s string) bool {
	return s != "" && len(s) <= maxIDLen && strings.TrimSpace(s) == s
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeStoreErr maps failures of the turn or session plumbing. The client is
// expected to continue without compression when this happens (fail-open, §19.7).
func writeStoreErr(w http.ResponseWriter, log *slog.Logger, err error) {
	if errors.Is(err, errUnknownConv) {
		writeErr(w, http.StatusNotFound, "unknown conversation")
		return
	}
	log.Warn("plugin: store failure", "err", err)
	writeErr(w, http.StatusInternalServerError, "store failure; continue without compression")
}

// writeToolErr maps tool-call failures. Session refusals (invalid refs, bad
// summaries) come back as 422 with the reason.
func writeToolErr(w http.ResponseWriter, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, errUnknownConv):
		writeErr(w, http.StatusNotFound, "unknown conversation")
	case errors.Is(err, errUnknownTool):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errBadArgs):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		log.Info("plugin: tool refused", "err", err)
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	}
}

// NewToken returns a random 256-bit bearer token, hex encoded.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// WriteTokenFile writes token to path readable only by the owner (0600).
func WriteTokenFile(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(token); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
