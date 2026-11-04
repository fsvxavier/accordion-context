package proxy

import (
	"context"
	"net/http"
	"sync"

	"github.com/fsvxavier/accordion-context/internal/obs"
)

// Client compaction is the coding client's own request that shrinks its
// transcript (see docs/compaction-commands.md). acd does not compact it: the
// request is forwarded unchanged, with no ingest, tools or nudge, so the client
// summarizes its real transcript. The ledger records the event when the session
// already exists, and the first request after the compaction is linked to it.

// compactionLinks remembers, per project, the session of the last client
// compaction, so that the first request after it can be linked to that session.
// It lives in memory only: after a restart the link is simply not written.
type compactionLinks struct {
	mu   sync.Mutex
	last map[string]string // project id -> session id
}

func (c *compactionLinks) set(project, sid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = make(map[string]string)
	}
	c.last[project] = sid
}

// take returns and forgets the session of the last client compaction of a project.
// Only the first request after a compaction consumes it.
func (c *compactionLinks) take(project string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sid, ok := c.last[project]
	delete(c.last, project)
	return sid, ok
}

// clientCompaction forwards the client's own compaction call unchanged. body is
// the raw request body, or nil when the caller has not read it yet. The outcome is
// counted as client_compaction, never as a fail-open.
func (s *Server) clientCompaction(w http.ResponseWriter, r *http.Request, rt routed, p protocol, body []byte) {
	if body == nil {
		b, err := readLimited(r.Body)
		if err != nil {
			http.Error(w, "request body unreadable or too large", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
	}
	s.countRequest(p.Name(), obs.OutcomeClientCompaction)
	s.cfg.Logger.Info("client compaction forwarded unchanged", "protocol", p.Name(), "path", rt.path)
	s.recordClientCompaction(context.WithoutCancel(r.Context()), r, rt, p, body)
	s.passthrough(w, r, rt, body)
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
	if _, err := s.cfg.Store.GetSession(ctx, sid); err != nil {
		s.cfg.Logger.Info("client compaction: no session yet, counted only", "protocol", p.Name())
		return
	}
	payload := map[string]any{"protocol": p.Name(), "path": rt.path, "messages": len(msgs)}
	if err := s.cfg.Store.AppendEvent(ctx, sid, "client_compaction", payload); err != nil {
		s.cfg.Logger.Warn("client compaction: event not recorded", "session", sid, "err", err)
		return
	}
	s.compactionLinks.set(proj.ID, sid)
}

// linkCompactionParent writes client_compaction_parent on the session of a request
// that carries x-claude-code-context-compacted, when the project's last client
// compaction is known. The request itself is compacted as usual.
func (s *Server) linkCompactionParent(ctx context.Context, t *turn) {
	parent, ok := s.compactionLinks.take(t.proj)
	if !ok || parent == t.sid {
		return
	}
	if err := s.cfg.Store.AppendEvent(ctx, t.sid, "client_compaction_parent", map[string]string{"parent": parent}); err != nil {
		s.cfg.Logger.Warn("client compaction: parent link not recorded", "session", t.sid, "err", err)
	}
}
