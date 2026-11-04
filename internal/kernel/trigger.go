package kernel

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// Voice is the tone of a nudge shown to the model (spec §8.3).
type Voice string

const (
	VoiceNone      Voice = ""
	VoiceGentle    Voice = "gentle"
	VoiceFirm      Voice = "firm"
	VoiceEmergency Voice = "emergency"
)

// Config holds the compression trigger thresholds (spec §8.1, §8.4).
// DefaultConfig returns the values from the spec.
type Config struct {
	StepBase       int         // S before adaptation: 50K
	StepMin        int         // 30K
	StepMax        int         // 100K
	StepReadGain   apd.Decimal // 1.0
	MinGrowthFloor int         // 20K
	UFloor         apd.Decimal // 0.30; 0 disables the floor (original behaviour)
	UHigh          apd.Decimal // 0.75
	UEmerg         apd.Decimal // 0.95
	TiersEnabled   bool        // gates rows 7 and 8
}

// DefaultConfig returns the spec §8.1 defaults.
func DefaultConfig() Config {
	return Config{
		StepBase:       50_000,
		StepMin:        30_000,
		StepMax:        100_000,
		StepReadGain:   dec("1"),
		MinGrowthFloor: 20_000,
		UFloor:         dec("0.30"),
		UHigh:          dec("0.75"),
		UEmerg:         dec("0.95"),
		TiersEnabled:   true,
	}
}

// TriggerInput is the per-turn observation fed to DecideNudge.
type TriggerInput struct {
	TokenCount   int         // current context size in tokens
	ContextLimit int         // L
	ReadDensity  apd.Decimal // share of recent growth from tool_result (0..1)
	Pending      map[int]int // compressible tokens per tier (keys 1..3)
	Trigger      TriggerState
}

// Decision is the outcome of DecideNudge. Row is the spec §8.3 row label
// ("row1".."row9"), or "guard" for invalid input and "refusal" when refusals
// suspend nudges. Every decision carries a label so behaviour can be read from events.
type Decision struct {
	Nudge  bool
	Tier   int
	Voice  Voice
	Row    string
	Reason string
}

// DecideNudge implements the spec §8.3 decision table (§8.5 code), evaluated top-down.
func DecideNudge(in TriggerInput, cfg Config) Decision {
	if in.ContextLimit <= 0 {
		return none("guard", "context limit not set")
	}
	S := adaptiveStep(cfg, in.ReadDensity)
	F := growthFloor(cfg, S)
	G := in.TokenCount - in.Trigger.LowWater
	Bmin := max(5000, in.ContextLimit/100)
	best, bestP := argmaxTier(in.Pending)

	// U = TokenCount / ContextLimit, compared without dividing (exact).
	L := in.ContextLimit
	switch {
	case geFrac(in.TokenCount, L, cfg.UEmerg) && bestP >= Bmin:
		return nudge(best, VoiceEmergency, "row1")
	case geFrac(in.TokenCount, L, cfg.UEmerg):
		return none("row2", "emergency, nothing compressible")
	case geFrac(in.TokenCount, L, cfg.UHigh) && bestP >= Bmin:
		return nudge(best, VoiceFirm, "row3")
	case ltFrac(in.TokenCount, L, cfg.UFloor):
		return none("row4", "below usage floor")
	}

	if in.Trigger.Refusals >= 3 {
		return none("refusal", "nudges suspended until high band")
	}
	need := F
	if in.Trigger.NudgePending {
		need = F / 2
	}
	if G < need {
		return none("row5", fmt.Sprintf("growth %d < floor %d", G, need))
	}

	p1, p2, p3 := in.Pending[1], in.Pending[2], in.Pending[3]
	t23 := truncInt(mul(dec("1.5"), intDec(S)))
	switch {
	case p1 >= S:
		return nudge(1, VoiceGentle, "row6")
	case cfg.TiersEnabled && p2 >= t23 && p2 > p1 && in.TokenCount-in.Trigger.LastShownT2 >= F:
		return nudge(2, VoiceGentle, "row7")
	case cfg.TiersEnabled && p3 >= t23 && p3 > p2 && p3 > p1 && in.TokenCount-in.Trigger.LastShownT3 >= F:
		return nudge(3, VoiceGentle, "row8")
	}
	return none("row9", "no tier pending")
}

// UpdateLowWater applies spec §8.2: the valley only goes down. prev == 0 means
// unset (first observation), so the current count becomes the valley.
// After a successful compression the caller sets LowWater = TokenCount directly.
func UpdateLowWater(prev, tokenCount int) int {
	if prev == 0 || tokenCount < prev {
		return tokenCount
	}
	return prev
}

// adaptiveStep returns the growth step S for a read density (spec §8.4).
func adaptiveStep(cfg Config, readDensity apd.Decimal) int {
	s := mul(intDec(cfg.StepBase), add(dec("1"), mul(cfg.StepReadGain, readDensity)))
	return clamp(truncInt(s), cfg.StepMin, cfg.StepMax)
}

// growthFloor is F = max(MinGrowthFloor, 0.45 * S) (spec §8.1).
func growthFloor(cfg Config, step int) int {
	return max(cfg.MinGrowthFloor, truncInt(mul(dec("0.45"), intDec(step))))
}

// argmaxTier returns the tier with the most pending tokens among 1..3.
// Ties go to the lower tier.
func argmaxTier(p map[int]int) (int, int) {
	best, bestP := 0, -1
	for t := 1; t <= 3; t++ {
		if p[t] > bestP {
			best, bestP = t, p[t]
		}
	}
	return best, bestP
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }

func none(row, reason string) Decision {
	return Decision{Row: row, Reason: reason}
}

func nudge(tier int, v Voice, row string) Decision {
	return Decision{Nudge: true, Tier: tier, Voice: v, Row: row}
}
