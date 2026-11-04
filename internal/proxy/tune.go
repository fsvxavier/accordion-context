package proxy

import (
	"context"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
)

const (
	// maxGaps bounds the fold history kept per session (spec §9.1, K).
	maxGaps = 16
	// maxExpectedCalls caps K, the expected number of calls left in a session.
	maxExpectedCalls = 50
	// summaryRatio is the assumed source/summary size before a fold exists
	// (sum = src / summaryRatio in the §9.1 estimate).
	summaryRatio = 5
	// coldIdle stands for an unknown idle time (no upstream call since start).
	// The cache is then treated as cold.
	coldIdle = 1 << 30
)

// unitFactor is the 1.0 factor used for K before any history exists.
var unitFactor = *apd.New(1, 0)

// tune is the in-memory state of one session that the proxy keeps between
// turns. It is lost on restart: calibration then starts again at 1.0 (§15).
type tune struct {
	calib     kernel.Calib
	lastCall  time.Time  // last upstream call; zero until the first one
	deferred  bool       // the last nudge was deferred by the cache gate (§9.3)
	sinceFold int        // upstream calls since the last fold
	gaps      []int      // upstream calls between folds, oldest first
	held      []heldSpan // ranges a decompress expanded; not re-nudged for a few turns
}

// heldTurns is how many requests a decompressed range is kept out of the
// nudge's span list, so expanding a block is not immediately undone by a nudge
// that offers the same messages again (anti-flap).
const heldTurns = 3

type heldSpan struct {
	lo, hi int64
	left   int
}

// holdSpan keeps [lo,hi] of sid out of the nudge for the next heldTurns requests.
func (s *Server) holdSpan(sid string, lo, hi int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tuneLocked(sid)
	t.held = append(t.held, heldSpan{lo, hi, heldTurns})
}

// takeHeld returns the ranges to hold back on this request and ages them by one.
func (s *Server) takeHeld(sid string) []heldSpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tuneLocked(sid)
	cur := append([]heldSpan(nil), t.held...)
	keep := t.held[:0]
	for _, h := range t.held {
		if h.left--; h.left > 0 {
			keep = append(keep, h)
		}
	}
	t.held = keep
	return cur
}

// dropHeld removes the spans that overlap a held range.
func dropHeld(spans []kernel.Span, held []heldSpan) []kernel.Span {
	if len(held) == 0 {
		return spans
	}
	var out []kernel.Span
next:
	for _, sp := range spans {
		for _, h := range held {
			if sp.StartSeq <= h.hi && h.lo <= sp.EndSeq {
				continue next
			}
		}
		out = append(out, sp)
	}
	return out
}

// tuneLocked returns the state of sid, creating it. s.mu must be held.
func (s *Server) tuneLocked(sid string) *tune {
	t, ok := s.tunes[sid]
	if !ok {
		t = &tune{calib: kernel.NewCalib(kernel.DefaultCalibAlpha)}
		s.tunes[sid] = t
	}
	return t
}

// calibrate maps a local estimate onto the scale the provider reports (§15).
func (s *Server) calibrate(sid string, est int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tuneLocked(sid).calib.Tokens(est)
}

// observeCall marks an upstream call of sid at the current time.
func (s *Server) observeCall(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tuneLocked(sid)
	t.lastCall = s.cfg.Now()
	t.sinceFold++
}

// observeUsage folds one sample (estimated against reported input tokens) into
// the calibration, and records the gap when a fold happened since the last call.
func (s *Server) observeUsage(sid string, est, reported int, folded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tuneLocked(sid)
	t.calib.Update(est, reported)
	if folded && t.sinceFold > 0 {
		t.gaps = append(t.gaps, t.sinceFold)
		if len(t.gaps) > maxGaps {
			t.gaps = t.gaps[len(t.gaps)-maxGaps:]
		}
		t.sinceFold = 0
	}
}

// calibFactor returns the current calibration factor and the sample count.
func (s *Server) calibFactor(sid string) (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tuneLocked(sid)
	return t.calib.Factor.String(), t.calib.Samples
}

// idleSeconds returns seconds since the last upstream call of sid. An unknown
// idle time counts as a cold cache.
func (s *Server) idleSeconds(sid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tuneLocked(sid)
	if t.lastCall.IsZero() {
		return coldIdle
	}
	return int(s.cfg.Now().Sub(t.lastCall) / time.Second)
}

// expectedCalls is K of §9.1: the median gap between folds, or 0 with no history.
func (s *Server) expectedCalls(sid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return kernel.ExpectedCalls(s.tuneLocked(sid).gaps, unitFactor, maxExpectedCalls)
}

// deferredFlag reports whether the last nudge of sid was deferred.
func (s *Server) deferredFlag(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tuneLocked(sid).deferred
}

func (s *Server) setDeferred(sid string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tuneLocked(sid).deferred = v
}

// costModel takes the prices and margin of the configuration (§9.1). Without
// prices the spec ratio p_c = 0.1 * p_u is used.
func costModel(cfg config.Config) kernel.CostModel {
	m := kernel.DefaultCostModel()
	if cfg.PriceUncached.Sign() > 0 {
		m.PriceUncached = cfg.PriceUncached
		m.PriceCached = cfg.PriceCached
	}
	if cfg.CacheMargin.Sign() > 0 {
		m.Margin = cfg.CacheMargin
	}
	return m
}

// calibSeedRows is how many stored cache samples a restarted session replays into
// its calibration (spec §15).
const calibSeedRows = 64

// sessionOptions maps the configuration of a project onto the session settings.
// The session protects at least the spec's recent zone (session.RecentKeep), so
// the nudge and the compress validation agree on what may be compressed.
func sessionOptions(cfg config.Config) session.Options {
	return session.Options{
		Kernel:           cfg.Kernel(),
		MinCompressRange: cfg.MinCompressRange,
		MinSummaryChars:  cfg.MinSummaryChars,
		RecentKeep:       max(cfg.PreserveRecentMessages, session.RecentKeep),
	}
}

// seedCalibLocked replays the stored cache samples of sid into its calibration, so
// the estimate keeps its scale after a restart (spec §15). s.mu must be held.
func (s *Server) seedCalibLocked(ctx context.Context, sid string) {
	rows, err := s.cfg.Store.ListCacheLedger(ctx, sid, calibSeedRows)
	if err != nil {
		s.cfg.Logger.Warn("calibration not restored", "session", sid, "err", err)
		return
	}
	t := s.tuneLocked(sid)
	if t.calib.Samples > 0 {
		return
	}
	for _, r := range rows {
		t.calib.Update(r.EstTokens, r.InputTokens)
	}
}

// nudgeSpans lists the ranges the nudge may offer: the spans outside the session's
// recent zone and at least its MinCompressRange, because the session would refuse
// the others.
func nudgeSpans(snap kernel.Snapshot, opts session.Options) []kernel.Span {
	var out []kernel.Span
	for _, sp := range kernel.CompressibleSpans(snap, opts.RecentKeep) {
		if sp.Tokens >= opts.MinCompressRange {
			out = append(out, sp)
		}
	}
	return out
}

// gateIn is what the cache gate needs about one turn.
type gateIn struct {
	sess    *session.Session
	plan    session.Plan // Before holds the trigger state before Plan changed it
	spans   []kernel.Span
	visible []kernel.Message
	tokens  int // TokenCount given to Plan (calibrated)
	limit   int // the session's context limit
	cfg     config.Config
}

// gateNudge applies the §9.3 cache gate to a nudge decided by Plan. It reports
// whether the nudge goes to the client. A deferred nudge is undone in the
// trigger state, so it does not count as shown or refused.
func (s *Server) gateNudge(ctx context.Context, sid string, g gateIn) bool {
	d := g.plan.Decision
	var best kernel.Span
	for _, sp := range g.spans {
		if sp.Tokens > best.Tokens {
			best = sp
		}
	}
	worth := false
	var fc kernel.FoldCost
	if best.Tokens > 0 {
		tail := 0
		for _, m := range g.visible {
			if m.Seq > best.EndSeq {
				tail += m.Tokens
			}
		}
		fc = kernel.EstimateFold(costModel(g.cfg), best.Tokens, best.Tokens/summaryRatio, tail, s.expectedCalls(sid))
		worth = fc.Worth
	}
	idle := s.idleSeconds(sid)
	ttl := int(g.cfg.CacheTTL / time.Second)
	deferred := s.deferredFlag(sid)
	action := kernel.GateNudge(d, worth, deferred, idle, ttl)

	u := 0.0
	if g.limit > 0 {
		u = float64(g.tokens) / float64(g.limit)
	}
	g0 := g.tokens - g.plan.Before.LowWater
	attrs := []any{
		"session", sid, "row", d.Row, "tier", d.Tier, "voice", string(d.Voice),
		"U", u, "G", g0, "cost", fc.Cost.String(), "benefit", fc.Benefit.String(),
		"worth", worth, "idle_s", idle, "ttl_s", ttl, "deferred", deferred,
	}
	switch action {
	case kernel.GateDefer:
		if err := g.sess.DeferNudge(ctx, g.plan); err != nil {
			// Without the undo the state would count a nudge that never reached the client, so inject.
			s.cfg.Logger.Warn("nudge gate: trigger undo failed; injecting", "session", sid, "err", err)
			return true
		}
		s.setDeferred(sid, true)
		payload := map[string]any{
			"row": d.Row, "U": u, "G": g0, "cost": fc.Cost.String(), "benefit": fc.Benefit.String(),
			"idle_s": idle, "ttl_s": ttl,
		}
		if err := s.cfg.Store.AppendEvent(ctx, sid, "nudge_deferred", payload); err != nil {
			s.cfg.Logger.Warn("nudge_deferred event not written", "session", sid, "err", err)
		}
		s.cfg.Logger.Info("cache gate: nudge deferred", attrs...)
		return false
	case kernel.GateInject:
		s.setDeferred(sid, false)
		s.cfg.Logger.Info("cache gate: nudge injected", attrs...)
		return true
	}
	return false
}
