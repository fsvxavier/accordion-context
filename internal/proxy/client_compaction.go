package proxy

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fsvxavier/accordion-context/internal/obs"
)

// Client compaction is the coding client's own request that shrinks its
// transcript (see docs/compaction-commands.md). acd does not compact it: the
// request is forwarded unchanged, with no ingest, tools or nudge, so the client
// summarizes its real transcript. The ledger records the event when the session
// already exists, and the first request after the compaction is linked to it.

// headerKiloMode is set by Kilo Code on every request with the name of its agent;
// the summary call uses the agent "compaction" (packages/opencode/src/session/llm/
// request.ts:183,259, commit c140bcd; VERIFIED from source, not on the wire).
const headerKiloMode = "X-Kilocode-Mode"

// Bounds of the link map. A link is only useful for the first request after a
// compaction, so an hour is generous, and the cap keeps a long-running proxy from
// growing with every project it has seen.
const (
	compactionLinkTTL = time.Hour
	maxCompactionLink = 256
)

// compactionLinks remembers, per project, the session of the last client
// compaction, so that the first request in a new session after it can be linked to
// that session. It lives in memory only: after a restart the link is not written.
type compactionLinks struct {
	mu   sync.Mutex
	last map[string]compactionLink
	now  func() time.Time // nil means time.Now; tests set it
}

type compactionLink struct {
	sid string
	at  time.Time
}

func (c *compactionLinks) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// set records the session of a project's last compaction. Expired links are
// dropped first; when the map is still full, the oldest link is dropped.
func (c *compactionLinks) set(project, sid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = make(map[string]compactionLink)
	}
	now := c.clock()
	for p, l := range c.last {
		if now.Sub(l.at) >= compactionLinkTTL {
			delete(c.last, p)
		}
	}
	if _, ok := c.last[project]; !ok && len(c.last) >= maxCompactionLink {
		oldest, first := "", true
		for p, l := range c.last {
			if first || l.at.Before(c.last[oldest].at) {
				oldest, first = p, false
			}
		}
		delete(c.last, oldest)
	}
	c.last[project] = compactionLink{sid: sid, at: now}
}

// take returns and forgets the session of the last client compaction of a project,
// when it is not expired. Only the first request after a compaction consumes it.
func (c *compactionLinks) take(project string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.last[project]
	delete(c.last, project)
	if !ok || c.clock().Sub(l.at) >= compactionLinkTTL {
		return "", false
	}
	return l.sid, true
}

// compactionSignal names why a POST is the client's own compaction call, or returns
// "". The deterministic signals of the adapter come first. Then the text rules run,
// reading the configured mode of the project only when their probes are found.
func (s *Server) compactionSignal(r *http.Request, rt routed, p protocol, body []byte) string {
	// Kilo Code names the agent of every call in this header. It is sent with the
	// Kilo gateway as provider; with an own key the text rule below still applies.
	if strings.EqualFold(strings.TrimSpace(r.Header.Get(headerKiloMode)), "compaction") {
		return signalKiloHeader
	}
	adapter := p.ClientCompaction(rt, r.Header, body)
	if adapter != "" && adapter != signalGeminiUtility {
		return adapter
	}
	mode := ""
	sig := matchText(p.Name(), body, func() string {
		proj := s.resolveProject(r, rt.project)
		mode = s.configFor(proj.Root).CompactionTextRules
		return mode
	})
	// The Gemini snapshot text is trusted only together with the shape of the
	// compression call, unless the owner chose "all".
	if sig == signalGeminiSnapshot && mode != "all" && !geminiCompressionShape(rt.path, body) {
		sig = ""
	}
	if sig != "" {
		return sig
	}
	return adapter
}

// clientCompaction forwards the client's own compaction call unchanged. body is the
// raw request body, or nil when the caller has not read it yet. The outcome is
// counted as client_compaction, never as a fail-open.
func (s *Server) clientCompaction(w http.ResponseWriter, r *http.Request, rt routed, p protocol, body []byte, sig string) {
	if body == nil {
		b, err := readLimited(r.Body)
		if err != nil {
			http.Error(w, "request body unreadable or too large", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
	}
	s.noteClientCompaction(context.WithoutCancel(r.Context()), r, rt, p, body, sig)
	s.passthrough(w, r, rt, body)
}

// noteClientCompaction counts and logs one client compaction, and records its event
// when the session exists. The request itself is not touched here.
func (s *Server) noteClientCompaction(ctx context.Context, r *http.Request, rt routed, p protocol, body []byte, sig string) {
	s.countRequest(p.Name(), obs.OutcomeClientCompaction)
	s.telemetry.m.ClientCompactions.WithLabelValues(p.Name(), sig).Inc()
	s.cfg.Logger.Info("client compaction forwarded unchanged", "protocol", p.Name(), "signal", sig, "path", rt.path)
	s.recordClientCompaction(ctx, r, rt, p, body)
}

// recordClientCompaction writes a client_compaction event on the session the
// request belongs to, when that session already exists. Nothing is ingested: the
// session is looked up with the id prepareTurn would start from, and a fork is
// not resolved. With no session the request is only logged and counted. Failures
// never affect the request.
func (s *Server) recordClientCompaction(ctx context.Context, r *http.Request, rt routed, p protocol, body []byte) {
	proj := s.resolveProject(r, rt.project)
	_, msgs, err := p.Decode(body)
	if err != nil {
		msgs = nil // the id then comes from the project and no user message
	}
	sid := sessionID(proj.ID, r.Header.Get(headerConv), msgs)
	st, err := s.storeFor(proj)
	if err != nil {
		s.cfg.Logger.Warn("client compaction: project database not opened, counted only", "err", err)
		return
	}
	if _, err := st.GetSession(ctx, sid); err != nil {
		s.cfg.Logger.Info("client compaction: no session yet, counted only", "protocol", p.Name())
		return
	}
	payload := map[string]any{"protocol": p.Name(), "path": rt.path, "messages": len(msgs)}
	if err := st.AppendEvent(ctx, sid, "client_compaction", payload); err != nil {
		s.cfg.Logger.Warn("client compaction: event not recorded", "session", sid, "err", err)
		return
	}
	s.compactionLinks.set(proj.ID, sid)
}

// linkCompactionParent writes client_compaction_parent on the session of a request
// that starts a new session after a compaction, or that carries
// x-claude-code-context-compacted. The project's last client compaction is the
// parent. The request itself is compacted as usual, and no block is inherited.
func (s *Server) linkCompactionParent(ctx context.Context, t *turn) {
	parent, ok := s.compactionLinks.take(t.proj)
	if !ok || parent == t.sid {
		return
	}
	if err := s.storeOf(t.sid).AppendEvent(ctx, t.sid, "client_compaction_parent", map[string]string{"parent": parent}); err != nil {
		s.cfg.Logger.Warn("client compaction: parent link not recorded", "session", t.sid, "err", err)
	}
}
