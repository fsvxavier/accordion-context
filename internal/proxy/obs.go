package proxy

// Observability of the proxy (spec §20): the metrics endpoint, the session status
// and trace endpoints, the per-turn decision log and the metric hooks. Every
// endpoint here answers only to loopback clients. Message text never reaches the
// metrics or the status report; the trace carries text only when asked for it.

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/obs"
	"github.com/fsvxavier/accordion-context/internal/session"
)

const (
	pathMetrics  = "/__acd-ctx/metrics"
	pathSessions = "/__acd-ctx/sessions/"
	// maxDecisions caps the ?decisions= parameter of the status endpoint.
	maxDecisions = 200
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// telemetry is the observability state of one Server: its private metrics and
// the recall hints still waiting for the model's answer.
type telemetry struct {
	m *obs.Metrics

	mu    sync.Mutex
	hints map[string]*hintTrack // per session id
}

// hintTrack follows the recall hints of one session. A hint offered by a Plan
// counts as accepted when the model decompresses its block before the next turn.
type hintTrack struct {
	pending map[string]string // block id -> signal of the offer
	acted   map[string]bool   // blocks decompressed since the last offer resolved
}

func newTelemetry() *telemetry {
	return &telemetry{m: obs.NewMetrics(), hints: map[string]*hintTrack{}}
}

// turnHints resolves the hints offered by the previous turn and records the ones
// offered now.
func (t *telemetry) turnHints(sid string, hints []kernel.RecallHint) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.hints[sid]
	if !ok {
		tr = &hintTrack{}
		t.hints[sid] = tr
	}
	for block, signal := range tr.pending {
		t.m.RecallHints.WithLabelValues(signal, obs.Bool(tr.acted[block])).Inc()
	}
	tr.pending = map[string]string{}
	tr.acted = map[string]bool{}
	for _, h := range hints {
		tr.pending[h.BlockID] = string(h.Kind)
	}
}

// markActed records that the model decompressed block in session sid.
func (t *telemetry) markActed(sid, block string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.hints[sid]
	if !ok {
		tr = &hintTrack{}
		t.hints[sid] = tr
	}
	if tr.acted == nil {
		tr.acted = map[string]bool{}
	}
	tr.acted[block] = true
}

// observeCompress counts a compress call and, when it was accepted, the ratio of
// each block it produced. The call is counted once, under the highest tier it made.
func (s *Server) observeCompress(ctx context.Context, sid string, args session.CompressArgs, res session.CompressResult, err error) {
	m := s.telemetry.m
	if err != nil {
		m.CompressTotal.WithLabelValues(requestedTier(args), obs.OutcomeRejected).Inc()
		return
	}
	snap, serr := s.cfg.Store.Snapshot(ctx, sid)
	if serr != nil {
		s.cfg.Logger.Warn("compress metrics skipped", "session", sid, "err", serr)
		return
	}
	byID := make(map[string]kernel.Block, len(snap.Blocks))
	for _, b := range snap.Blocks {
		byID[b.ID] = b
	}
	top := 0
	for _, id := range res.BlockIDs {
		b, ok := byID[id]
		if !ok {
			continue
		}
		top = max(top, b.Tier)
		if b.SumTokens > 0 {
			m.CompressRatio.Observe(float64(b.SrcTokens) / float64(b.SumTokens))
		}
	}
	if top == 0 {
		return
	}
	m.CompressTotal.WithLabelValues(strconv.Itoa(top), obs.OutcomeAccepted).Inc()
}

// requestedTier labels a rejected call by the tier it asked for.
func requestedTier(args session.CompressArgs) string {
	if len(args.Ranges) == 0 && len(args.Folds) > 0 {
		return strconv.Itoa(args.Folds[0].Tier)
	}
	return "1"
}

// observeTurn writes the decision line of one turn (spec §20): the row of the
// decision table, U, G, S, P1..P3, the cache cost and benefit of the best candidate
// fold, and the recall hints. It also feeds the fold-cost histogram and the
// refusal counter.
func (s *Server) observeTurn(ctx context.Context, sid string, sess *session.Session, snap kernel.Snapshot,
	plan session.Plan, tokenCount int, cfg config.Config) {
	d := plan.Decision
	opts := sess.Settings()
	S := kernel.Step(opts.Kernel, snap)
	P := kernel.PendingTokens(snap, opts.RecentKeep)

	u := 0.0
	if snap.ContextLimit > 0 {
		u = float64(tokenCount) / float64(snap.ContextLimit)
	}
	g := tokenCount - plan.Before.LowWater

	var cost, benefit string
	spans := nudgeSpans(snap, opts)
	var best kernel.Span
	for _, sp := range spans {
		if sp.Tokens > best.Tokens {
			best = sp
		}
	}
	if best.Tokens > 0 {
		tail := 0
		for _, m := range plan.Visible {
			if m.Seq > best.EndSeq {
				tail += m.Tokens
			}
		}
		fc := kernel.EstimateFold(costModel(cfg), best.Tokens, best.Tokens/summaryRatio, tail, s.expectedCalls(sid))
		cost, benefit = fc.Cost.String(), fc.Benefit.String()
		if c, err := fc.Cost.Float64(); err == nil {
			s.telemetry.m.FoldCacheCost.WithLabelValues("cost").Observe(c)
		}
		if b, err := fc.Benefit.Float64(); err == nil {
			s.telemetry.m.FoldCacheCost.WithLabelValues("benefit").Observe(b)
		}
	}

	signals := make([]string, 0, len(plan.Hints))
	for _, h := range plan.Hints {
		signals = append(signals, string(h.Kind))
	}
	if plan.After.Refusals > plan.Before.Refusals {
		s.telemetry.m.CompressTotal.WithLabelValues(obs.TierNone, obs.OutcomeRefused).Inc()
	}
	s.cfg.Logger.Info("turn decision", "session", sid, "row", d.Row, "nudge", d.Nudge,
		"tier", d.Tier, "voice", string(d.Voice), "reason", d.Reason,
		"tokens", tokenCount, "U", u, "G", g, "S", S,
		"P1", P[1], "P2", P[2], "P3", P[3],
		"cost", cost, "benefit", benefit,
		"recall_hints", len(plan.Hints), "recall_signals", signals)
}

// observeCache feeds the cache-read ratio and the calibration factor of an upstream
// call that reported usage.
func (s *Server) observeCache(sid string, u usage) {
	if u.input > 0 {
		s.telemetry.m.CacheReadRatio.Observe(float64(u.cached) / float64(u.input))
	}
	if f, _ := s.calibFactor(sid); f != "" {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			s.telemetry.m.CalibFactor.Set(v)
		}
	}
}

// observeUpstream records the latency of one upstream call. A Server built
// without New has no telemetry, and then nothing is recorded.
func (s *Server) observeUpstream(d time.Duration) {
	if s.telemetry != nil {
		s.telemetry.m.UpstreamSeconds.Observe(d.Seconds())
	}
}

// countRequest counts a compaction request under its protocol and outcome.
func (s *Server) countRequest(protocol, outcome string) {
	s.telemetry.m.Requests.WithLabelValues(protocol, outcome).Inc()
}

// loopbackClient reports whether the peer of a request is a loopback address.
func loopbackClient(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// internalGET checks the method and the peer of an observability request.
func internalGET(w http.ResponseWriter, r *http.Request) bool {
	if !loopbackClient(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// metricsEndpoint serves GET /__acd-ctx/metrics.
func (s *Server) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	if !internalGET(w, r) {
		return
	}
	s.telemetry.m.Handler().ServeHTTP(w, r)
}

// sessionEndpoint routes GET /__acd-ctx/sessions/{id}/status and .../trace.jsonl.
// rest is the path after the sessions prefix.
func (s *Server) sessionEndpoint(w http.ResponseWriter, r *http.Request, rest string) {
	if !internalGET(w, r) {
		return
	}
	id, tail, ok := strings.Cut(rest, "/")
	if !ok || !sessionIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	switch tail {
	case "status":
		n := obs.DefaultDecisions
		if q := r.URL.Query().Get("decisions"); q != "" {
			v, err := strconv.Atoi(q)
			if err != nil || v <= 0 || v > maxDecisions {
				http.Error(w, "decisions must be 1.."+strconv.Itoa(maxDecisions), http.StatusBadRequest)
				return
			}
			n = v
		}
		rep, err := obs.BuildStatus(ctx, s.cfg.Store, id, n)
		if notFound(w, r, err) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, rep)
	case "trace.jsonl":
		anon := false
		switch r.URL.Query().Get("anonymize") {
		case "", "0", "false":
		case "1", "true":
			anon = true
		default:
			http.Error(w, "anonymize must be 0 or 1", http.StatusBadRequest)
			return
		}
		tr, err := obs.ExportTrace(ctx, s.cfg.Store, id, anon)
		if notFound(w, r, err) {
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if err := obs.WriteTrace(w, tr); err != nil {
			s.cfg.Logger.Warn("trace export interrupted", "session", id, "err", err)
		}
	default:
		http.NotFound(w, r)
	}
}

// notFound answers 404 for an unknown session and 500 for any other store error.
// It reports whether it wrote a response.
func notFound(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return true
	}
	http.Error(w, "store read failed", http.StatusInternalServerError)
	return true
}
