package kernel

import (
	"math"
	"sort"
)

// CostModel holds per-token prices (any consistent unit) and the worth margin (spec §9.1).
type CostModel struct {
	PriceUncached float64
	PriceCached   float64
	Margin        float64
}

// DefaultCostModel uses p_c = 0.1 * p_u and margin 1.5 (spec §9.1).
func DefaultCostModel() CostModel {
	return CostModel{PriceUncached: 1, PriceCached: 0.1, Margin: 1.5}
}

// FoldCost is the estimate for one candidate fold.
type FoldCost struct {
	Recovered int     // R = src_tokens - sum_tokens
	Tail      int     // T: tokens after the anchor, which lose cache once
	Calls     int     // K: expected remaining calls
	Cost      float64 // T * (p_u - p_c)
	Benefit   float64 // R * p_c * K
	Worth     bool    // Benefit >= Cost * Margin
}

// EstimateFold applies the spec §9.1 cost model to one fold.
func EstimateFold(m CostModel, srcTokens, sumTokens, tailTokens, calls int) FoldCost {
	r := max(srcTokens-sumTokens, 0)
	cost := float64(tailTokens) * (m.PriceUncached - m.PriceCached)
	benefit := float64(r) * m.PriceCached * float64(calls)
	return FoldCost{
		Recovered: r,
		Tail:      tailTokens,
		Calls:     calls,
		Cost:      cost,
		Benefit:   benefit,
		Worth:     benefit >= cost*m.Margin,
	}
}

// ExpectedCalls estimates K (spec §9.1): median gap between past folds, in calls,
// times factor, clamped to [0, limit]. With no history it returns 0, so a
// non-pressure nudge is not worth it until a fold exists.
func ExpectedCalls(gaps []int, factor float64, limit int) int {
	if len(gaps) == 0 {
		return 0
	}
	s := append([]int(nil), gaps...)
	sort.Ints(s)
	n := len(s)
	var med float64
	if n%2 == 1 {
		med = float64(s[n/2])
	} else {
		med = float64(s[n/2-1]+s[n/2]) / 2
	}
	return clamp(int(math.Round(med*factor)), 0, limit)
}

// GateAction is the cache gate outcome for a nudge (spec §9.3).
type GateAction int

const (
	GateNone GateAction = iota
	GateInject
	GateDefer
)

// GateNudge applies the spec §9.3 cacheGate to a §8 decision.
// Pressure nudges (firm, emergency) are never deferred. A worthwhile nudge is
// injected now. Otherwise the nudge is deferred, unless it was already deferred
// and the cache is cold (idleSeconds > cacheTTL), when injecting costs no cache.
func GateNudge(d Decision, worth, deferred bool, idleSeconds, cacheTTL int) GateAction {
	if !d.Nudge {
		return GateNone
	}
	if d.Voice == VoiceFirm || d.Voice == VoiceEmergency {
		return GateInject
	}
	if worth {
		return GateInject
	}
	if deferred && idleSeconds > cacheTTL {
		return GateInject
	}
	return GateDefer
}
