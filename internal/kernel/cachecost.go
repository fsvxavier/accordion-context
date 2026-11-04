package kernel

import (
	"sort"

	"github.com/cockroachdb/apd/v3"
)

// CostModel holds per-token prices (any consistent unit) and the worth margin (spec §9.1).
type CostModel struct {
	PriceUncached apd.Decimal
	PriceCached   apd.Decimal
	Margin        apd.Decimal
}

// DefaultCostModel uses p_c = 0.1 * p_u and margin 1.5 (spec §9.1).
func DefaultCostModel() CostModel {
	return CostModel{PriceUncached: dec("1"), PriceCached: dec("0.1"), Margin: dec("1.5")}
}

// FoldCost is the estimate for one candidate fold.
type FoldCost struct {
	Recovered int         // R = src_tokens - sum_tokens
	Tail      int         // T: tokens after the anchor, which lose cache once
	Calls     int         // K: expected remaining calls
	Cost      apd.Decimal // T * (p_u - p_c)
	Benefit   apd.Decimal // R * p_c * K
	Worth     bool        // Benefit >= Cost * Margin
}

// EstimateFold applies the spec §9.1 cost model to one fold.
func EstimateFold(m CostModel, srcTokens, sumTokens, tailTokens, calls int) FoldCost {
	r := max(srcTokens-sumTokens, 0)
	cost := mul(intDec(tailTokens), sub(m.PriceUncached, m.PriceCached))
	benefit := mul(mul(intDec(r), m.PriceCached), intDec(calls))
	return FoldCost{
		Recovered: r,
		Tail:      tailTokens,
		Calls:     calls,
		Cost:      cost,
		Benefit:   benefit,
		Worth:     cmp(benefit, mul(cost, m.Margin)) >= 0,
	}
}

// ExpectedCalls estimates K (spec §9.1): median gap between past folds, in calls,
// times factor, clamped to [0, limit]. With no history it returns 0, so a
// non-pressure nudge is not worth it until a fold exists.
func ExpectedCalls(gaps []int, factor apd.Decimal, limit int) int {
	if len(gaps) == 0 {
		return 0
	}
	s := append([]int(nil), gaps...)
	sort.Ints(s)
	n := len(s)
	var med apd.Decimal
	if n%2 == 1 {
		med = intDec(s[n/2])
	} else {
		med = quo(intDec(s[n/2-1]+s[n/2]), intDec(2))
	}
	return clamp(roundInt(mul(med, factor)), 0, limit)
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
