package obs

import (
	"context"
	"encoding/json"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// DefaultDecisions is how many recent events the status report lists.
const DefaultDecisions = 20

// Status is the report of GET /__acd-ctx/sessions/{id}/status (spec §20). It
// carries counts, identifiers and decision payloads, never message or block text.
type Status struct {
	Session         store.SessionRow `json:"session"`
	Trigger         TriggerInfo      `json:"trigger"`
	Window          WindowInfo       `json:"window"`
	Blocks          []TierInfo       `json:"blocks"`
	Intents         IntentInfo       `json:"intents"`
	RecentDecisions []DecisionInfo   `json:"recent_decisions"`
	Cache           CacheInfo        `json:"cache"`
}

// TriggerInfo is the trigger state (spec §8.2, §8.3).
type TriggerInfo struct {
	LowWaterTokens int  `json:"low_water_tokens"`
	NudgePending   bool `json:"nudge_pending"`
	NudgeShownAt   int  `json:"nudge_shown_at"`
	LastShownT2    int  `json:"last_shown_t2"`
	LastShownT3    int  `json:"last_shown_t3"`
	Refusals       int  `json:"refusals"`
}

// WindowInfo describes the rendered window: what the next request would carry.
type WindowInfo struct {
	Tokens        int            `json:"tokens"`
	Messages      int            `json:"messages"`
	MessagesTotal int            `json:"messages_total"`
	ByKind        map[string]int `json:"by_kind"`
}

// TierInfo counts the active blocks of one tier.
type TierInfo struct {
	Tier      int `json:"tier"`
	Active    int `json:"active"`
	SrcTokens int `json:"src_tokens"`
	SumTokens int `json:"sum_tokens"`
}

// IntentInfo lists the open intents (spec §11.3). Intent text is left out.
type IntentInfo struct {
	Open      []OpenIntent `json:"open"`
	OpenCount int          `json:"open_count"`
	DoneCount int          `json:"done_count"`
}

// OpenIntent identifies an open intent by its id and origin.
type OpenIntent struct {
	ID        int64 `json:"id"`
	OriginSeq int64 `json:"origin_seq"`
}

// DecisionInfo is one recent event. Payload holds the decision keys of its type
// (see decisionKeys).
type DecisionInfo struct {
	Seq     int64          `json:"seq"`
	At      int64          `json:"at"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}

// CacheInfo is the measured cache cost of the upstream calls (spec §9.4).
type CacheInfo struct {
	Calls         int64   `json:"calls"`
	InputTokens   int64   `json:"input_tokens"`
	CachedTokens  int64   `json:"cached_tokens"`
	ReadRatio     float64 `json:"read_ratio"`
	AfterCompress int64   `json:"after_compress"`
}

// BuildStatus reads the status of session sid. decisions bounds the recent events
// (DefaultDecisions when <= 0).
func BuildStatus(ctx context.Context, st *store.Store, sid string, decisions int) (Status, error) {
	if decisions <= 0 {
		decisions = DefaultDecisions
	}
	var out Status
	row, err := st.GetSession(ctx, sid)
	if err != nil {
		return out, err
	}
	out.Session = row
	snap, err := st.Snapshot(ctx, sid)
	if err != nil {
		return out, err
	}
	tr := snap.Trigger
	out.Trigger = TriggerInfo{
		LowWaterTokens: tr.LowWater, NudgePending: tr.NudgePending, NudgeShownAt: tr.NudgeShownAt,
		LastShownT2: tr.LastShownT2, LastShownT3: tr.LastShownT3, Refusals: tr.Refusals,
	}

	out.Window = WindowInfo{MessagesTotal: len(snap.Messages), ByKind: map[string]int{}}
	for _, m := range kernel.Render(snap) {
		out.Window.Messages++
		out.Window.Tokens += m.Tokens
		out.Window.ByKind[string(m.Role)+"/"+string(m.Kind)]++
	}

	byTier := map[int]*TierInfo{1: {Tier: 1}, 2: {Tier: 2}, 3: {Tier: 3}}
	for _, b := range snap.Blocks {
		if !b.Active {
			continue
		}
		ti, ok := byTier[b.Tier]
		if !ok {
			continue
		}
		ti.Active++
		ti.SrcTokens += b.SrcTokens
		ti.SumTokens += b.SumTokens
	}
	for tier := 1; tier <= 3; tier++ {
		out.Blocks = append(out.Blocks, *byTier[tier])
	}

	out.Intents.Open = []OpenIntent{}
	for _, in := range snap.Intents {
		switch in.Status {
		case "open":
			out.Intents.OpenCount++
			out.Intents.Open = append(out.Intents.Open, OpenIntent{ID: in.ID, OriginSeq: in.OriginSeq})
		case "done":
			out.Intents.DoneCount++
		}
	}

	evs, err := st.ListEvents(ctx, sid, decisions)
	if err != nil {
		return out, err
	}
	out.RecentDecisions = []DecisionInfo{}
	for _, e := range evs {
		out.RecentDecisions = append(out.RecentDecisions, DecisionInfo{
			Seq: e.Seq, At: e.At, Type: e.Type, Payload: payloadMap(e.Type, e.Payload),
		})
	}

	c, err := st.CacheTotals(ctx, sid)
	if err != nil {
		return out, err
	}
	out.Cache = CacheInfo{Calls: c.Calls, InputTokens: c.InputTokens, CachedTokens: c.CachedTokens,
		AfterCompress: c.AfterCompress}
	if c.InputTokens > 0 {
		out.Cache.ReadRatio = float64(c.CachedTokens) / float64(c.InputTokens)
	}
	return out, nil
}

// decisionKeys lists, per event type, the payload keys the status report shows.
// Anything else is left out, so a new writer cannot leak text through the report
// by default. Types not listed show no payload.
var decisionKeys = map[string][]string{
	"turn":           {"tokens", "messages", "context_limit"},
	"compress":       {"blocks", "deactivated", "closedIntents", "tokensAfter"},
	"decompress":     {"requested", "target", "reactivated", "redirected"},
	"recall_hint":    {"kind", "block", "ref"},
	"refuse":         {"refusals"},
	"nudge_deferred": {"row", "U", "G", "cost", "benefit", "idle_s", "ttl_s"},
	"truncate":       {"limit", "tokens", "dropped", "overflow", "from", "to"},
}

// payloadMap decodes the listed keys of an event payload. A payload that is not
// an object, or a type with no listed keys, yields nil.
func payloadMap(typ string, raw json.RawMessage) map[string]any {
	keys := decisionKeys[typ]
	if len(keys) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := map[string]any{}
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		var x any
		if err := json.Unmarshal(v, &x); err == nil {
			out[k] = x
		}
	}
	return out
}
