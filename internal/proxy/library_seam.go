package proxy

import (
	"context"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// This file is the exported seam of the proxy for the library package pkg/acdctx.
// It only wraps unexported helpers so the library runs the same code as the
// server: the same session identity, the same nudge, the same window reduction.

// TurnView is one planned turn as the library returns it.
type TurnView struct {
	Visible []kernel.Message
	Nudge   string // "" when no nudge is due
	Hints   []kernel.RecallHint
	Row     string // decision row of spec §8.3, for diagnostics
}

// PlanTurn runs the per-turn steps of compactTurn that follow Ingest: it applies
// the project settings, plans the trigger, reduces the window (CCR and crush) and
// writes the nudge text. The caller ingests the transcript first. The cache gate
// of the server is not applied: a library caller has no upstream cache to measure.
func PlanTurn(ctx context.Context, st *store.Store, sess *session.Session, sid string, cfg config.Config, tokenCount int) (TurnView, error) {
	sess.SetOptions(sessionOptions(cfg))
	snap, err := st.Snapshot(ctx, sid)
	if err != nil {
		return TurnView{}, err
	}
	plan, err := sess.Plan(ctx, tokenCount)
	if err != nil {
		return TurnView{}, err
	}
	v := TurnView{
		Visible: reduceWindow(plan.Visible, cfg.CCRThresholdTokens, cfg.CrushEnabled),
		Hints:   plan.Hints,
		Row:     plan.Decision.Row,
	}
	if plan.Decision.Nudge {
		v.Nudge = nudgeText(plan.Decision.Voice, nudgeSpans(snap, sess.Settings()))
	}
	return v, nil
}

// SessionID returns the session id the proxy derives for a project and a client
// transcript, so a library client and a proxy on the same database share sessions.
func SessionID(projectID, conv string, msgs []kernel.Message) string {
	return sessionID(projectID, conv, msgs)
}

// ProjectMarkers returns the project root markers of the proxy.
func ProjectMarkers() []string {
	return append([]string(nil), defaultMarkers...)
}

// IsCompactedPath reports whether a request path is one of the endpoints the
// proxy compacts (any dialect, with or without a gateway prefix).
func IsCompactedPath(path string) bool {
	return protocolFor(routed{path: path, viaCtx: true}) != nil
}
