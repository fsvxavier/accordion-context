package kernel

import "github.com/cockroachdb/apd/v3"

// Step returns the growth step S of spec §8.4 for the ledger in s: the adaptive
// step computed from its read density. It is the value DecideNudge uses, exported
// for the per-turn decision log (spec §20).
func Step(cfg Config, s Snapshot) int {
	return adaptiveStep(cfg, ReadDensity(s))
}

// ReadDensity is the share of tool_result tokens in the whole ledger (spec §8.4
// input). It is 0 for an empty ledger.
func ReadDensity(s Snapshot) apd.Decimal {
	total, reads := 0, 0
	for _, m := range s.Messages {
		total += m.Tokens
		if m.Kind == KindToolResult {
			reads += m.Tokens
		}
	}
	var r apd.Decimal
	if total == 0 {
		return r
	}
	_, _ = decCtx.Quo(&r, stepDecimal(reads), stepDecimal(total))
	return r
}

// PendingTokens is the compressible tokens per tier, the P1..P3 of the decision
// log (spec §8.3). Tier 1 is the uncovered, non-system, non-first-user messages
// outside the last keep messages. Tiers 2 and 3 are FoldablePending. It mirrors
// the pending computation of the session that decides the nudge.
func PendingTokens(s Snapshot, keep int) map[int]int {
	p := map[int]int{}
	var first int64
	for _, m := range s.Messages {
		if m.Role == RoleUser && m.Kind == KindText {
			first = m.Seq
			break
		}
	}
	var last int64
	if n := len(s.Messages); n > 0 {
		last = s.Messages[n-1].Seq
	}
	for _, m := range s.Messages {
		if m.Role == RoleSystem || m.Seq == first || m.Seq > last-int64(keep) {
			continue
		}
		if stepCovered(s.Blocks, m.Seq) {
			continue
		}
		p[1] += m.Tokens
	}
	p[2] = FoldablePending(s, 2)
	p[3] = FoldablePending(s, 3)
	return p
}

// stepCovered reports whether seq lies inside an active block.
func stepCovered(blocks []Block, seq int64) bool {
	for _, b := range blocks {
		if b.Active && seq >= b.StartSeq && seq <= b.EndSeq {
			return true
		}
	}
	return false
}

func stepDecimal(n int) *apd.Decimal {
	d := intDec(n)
	return &d
}
