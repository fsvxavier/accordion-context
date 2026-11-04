package kernel

import (
	"errors"
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
)

var (
	// ErrNoSuchBlock is returned when a block id is not in the snapshot.
	ErrNoSuchBlock = errors.New("kernel: no such block")
	// ErrNotReachable is returned when a block is inactive and no active block
	// covers it, so decompressing it would not change the visible window.
	ErrNotReachable = errors.New("kernel: block is not covered by an active block")
	// ErrInvariant is returned when a plan would leave the block set in a state
	// that breaks I1, I2, I5 or I6 (spec §17.1).
	ErrInvariant = errors.New("kernel: block invariant violated")
)

// TierPlan describes a T2 or T3 fold: a contiguous run of active blocks one
// tier below, consumed by one new block of the target tier (spec §12). It is
// pure data. The summary is written later by the model, and ApplyTierPlan
// validates the result.
type TierPlan struct {
	Tier      int      // target tier: 2 or 3
	Sources   []string // consumed blocks, ascending by StartSeq, contiguous
	StartSeq  int64    // first source's StartSeq
	EndSeq    int64    // last source's EndSeq
	SrcTokens int      // sum of the sources' SumTokens (tokens folded)
}

// tierThreshold returns the fold threshold 1.5·S (spec §8.3, rows 7 and 8) and
// the growth floor F, computed the same way as DecideNudge.
func tierThreshold(cfg Config, readDensity apd.Decimal) (threshold, floor int) {
	S := adaptiveStep(cfg, readDensity)
	return truncInt(mul(dec("1.5"), intDec(S))), growthFloor(cfg, S)
}

// foldableRuns groups the active blocks of tier-1 into maximal contiguous runs,
// in ledger order, and keeps the runs of at least two blocks. A single block
// folded alone would not shrink anything useful.
func foldableRuns(s Snapshot, tier int) [][]Block {
	var runs [][]Block
	var cur []Block
	for _, b := range activeSorted(s.Blocks) {
		if b.Tier != tier-1 {
			continue
		}
		if len(cur) > 0 && cur[len(cur)-1].EndSeq+1 != b.StartSeq {
			if len(cur) >= 2 {
				runs = append(runs, cur)
			}
			cur = nil
		}
		cur = append(cur, b)
	}
	if len(cur) >= 2 {
		runs = append(runs, cur)
	}
	return runs
}

func runPending(run []Block) int {
	n := 0
	for _, b := range run {
		n += b.SumTokens
	}
	return n
}

// FoldablePending returns the largest pending amount for a fold into the given
// tier (2 or 3): the summed SumTokens of the best contiguous run of tier-1
// active blocks. It is 0 when no run is foldable. It is the value to put in
// TriggerInput.Pending[tier].
func FoldablePending(s Snapshot, tier int) int {
	if tier != 2 && tier != 3 {
		return 0
	}
	best := 0
	for _, run := range foldableRuns(s, tier) {
		best = max(best, runPending(run))
	}
	return best
}

// PlanTierFold selects the run to fold into a new block of the given tier (2 or
// 3), using the thresholds and cadence of spec §8.3 rows 7 and 8 and §12. A run
// qualifies when it has at least two blocks, its pending tokens reach 1.5·S,
// and the cadence for that tier has passed, meaning tokenCount minus the last
// tier nudge is at least F. Among qualifying runs the one with the most pending
// tokens wins, and ties go to the earlier run. It returns false when nothing
// qualifies or tiers are disabled.
func PlanTierFold(s Snapshot, cfg Config, tier int, readDensity apd.Decimal, tokenCount int) (TierPlan, bool) {
	if !cfg.TiersEnabled || (tier != 2 && tier != 3) {
		return TierPlan{}, false
	}
	threshold, floor := tierThreshold(cfg, readDensity)
	last := s.Trigger.LastShownT2
	if tier == 3 {
		last = s.Trigger.LastShownT3
	}
	if tokenCount-last < floor {
		return TierPlan{}, false
	}
	return bestRun(s, tier, threshold)
}

// PlanFold plans the fold that a DecideNudge decision asks for, so the kernel
// never nudges a tier it cannot plan (spec §10.2). Rows 1 and 3 (pressure) take
// the best run with at least Bmin pending and ignore the growth threshold and
// the cadence, as the decision table does. Rows 7 and 8 use PlanTierFold. Other
// decisions, and tier 1, return false. contextLimit is TriggerInput.ContextLimit.
func PlanFold(s Snapshot, cfg Config, d Decision, contextLimit int, readDensity apd.Decimal, tokenCount int) (TierPlan, bool) {
	if !d.Nudge {
		return TierPlan{}, false
	}
	switch d.Row {
	case "row1", "row3":
		return bestRun(s, d.Tier, max(5000, contextLimit/100))
	case "row7", "row8":
		return PlanTierFold(s, cfg, d.Tier, readDensity, tokenCount)
	}
	return TierPlan{}, false
}

// bestRun returns the foldable run with the most pending tokens among those
// that reach minPending. Ties go to the earlier run.
func bestRun(s Snapshot, tier, minPending int) (TierPlan, bool) {
	if tier != 2 && tier != 3 {
		return TierPlan{}, false
	}
	var best []Block
	bestP := -1
	for _, run := range foldableRuns(s, tier) {
		p := runPending(run)
		if p >= minPending && p > bestP {
			best, bestP = run, p
		}
	}
	if best == nil {
		return TierPlan{}, false
	}
	plan := TierPlan{
		Tier:      tier,
		StartSeq:  best[0].StartSeq,
		EndSeq:    best[len(best)-1].EndSeq,
		SrcTokens: bestP,
	}
	for _, b := range best {
		plan.Sources = append(plan.Sources, b.ID)
	}
	return plan, true
}

// ParentOf returns the store's link map for this plan: each consumed source
// points at newID, the block the fold creates. The store writes it as
// parent_block when it deactivates the sources (CompressPlan.ParentOf).
func (p TierPlan) ParentOf(newID string) map[string]string {
	m := make(map[string]string, len(p.Sources))
	for _, id := range p.Sources {
		m[id] = newID
	}
	return m
}

// ApplyTierPlan returns a new block set with the plan applied, and leaves the
// input unchanged. The sources become inactive, and nb is added as an active
// block of the plan's tier with Sources set. nb carries the model's ID, Topic,
// Summary and SumTokens. It returns an error if the plan does not match the
// blocks, or if the result breaks I1, I2, I5 or I6.
func ApplyTierPlan(blocks []Block, p TierPlan, nb Block) ([]Block, error) {
	if p.Tier != 2 && p.Tier != 3 {
		return nil, fmt.Errorf("%w: tier %d is not a fold target", ErrInvariant, p.Tier)
	}
	if len(p.Sources) < 2 {
		return nil, fmt.Errorf("%w: a fold needs at least two sources", ErrInvariant)
	}
	byID := indexBlocks(blocks)
	if _, dup := byID[nb.ID]; dup || nb.ID == "" {
		return nil, fmt.Errorf("%w: block id %q is empty or taken", ErrInvariant, nb.ID)
	}
	var srcTokens int
	for i, id := range p.Sources {
		b, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%w: source %q", ErrNoSuchBlock, id)
		}
		if !b.Active || b.Tier != p.Tier-1 {
			return nil, fmt.Errorf("%w: source %q must be active and tier %d", ErrInvariant, id, p.Tier-1)
		}
		if i > 0 && byID[p.Sources[i-1]].EndSeq+1 != b.StartSeq {
			return nil, fmt.Errorf("%w: sources are not contiguous at %q", ErrInvariant, id)
		}
		srcTokens += b.SumTokens
	}
	if p.StartSeq != byID[p.Sources[0]].StartSeq || p.EndSeq != byID[p.Sources[len(p.Sources)-1]].EndSeq {
		return nil, fmt.Errorf("%w: plan range does not match its sources", ErrInvariant)
	}
	if p.SrcTokens != srcTokens {
		return nil, fmt.Errorf("%w: plan src tokens %d, sources sum to %d", ErrInvariant, p.SrcTokens, srcTokens)
	}

	out := cloneBlocks(blocks)
	consumed := make(map[string]bool, len(p.Sources))
	for _, id := range p.Sources {
		consumed[id] = true
	}
	for i := range out {
		if consumed[out[i].ID] {
			out[i].Active = false
		}
	}
	nb.Tier = p.Tier
	nb.StartSeq = p.StartSeq
	nb.EndSeq = p.EndSeq
	nb.SrcTokens = p.SrcTokens
	nb.Active = true
	nb.Sources = append([]string(nil), p.Sources...)
	out = append(out, nb)

	if err := CheckBlockInvariants(out); err != nil {
		return nil, err
	}
	return out, nil
}

// DecompressPlan reactivates the direct sources of Target and deactivates
// Target (spec §13.1, mode "context"). Target is the requested block when that
// block is active. When the requested block is inactive, Target is its active
// ancestor and Redirected is true: its content is still folded under that
// ancestor, so one level is expanded per call.
//
// The store must clear parent_block on every block in Reactivate. Target is
// active, so it has no parent to clear. An active T1 target has no sources, so it becomes
// an inactive T1 with no parent. The store's I7 query flags that state
// (see internal/store/invariants.go); the caller has to resolve it.
type DecompressPlan struct {
	Requested  string
	Target     string
	Reactivate []string // direct sources of Target, ascending by StartSeq
	Redirected bool
	StartSeq   int64 // range of Target, for the cache anchor
	EndSeq     int64
}

// PlanDecompress builds the reactivation plan for a block. It fails with
// ErrNoSuchBlock for an unknown id, and with ErrNotReachable when the block is
// inactive and no active block covers it. It also fails when applying the plan
// would overlap another active block (I2).
func PlanDecompress(s Snapshot, id string) (DecompressPlan, error) {
	byID := indexBlocks(s.Blocks)
	req, ok := byID[id]
	if !ok {
		return DecompressPlan{}, fmt.Errorf("%w: %q", ErrNoSuchBlock, id)
	}
	target, redirected := req, false
	if !req.Active {
		anc, ok := activeAncestor(s.Blocks, id)
		if !ok {
			return DecompressPlan{}, fmt.Errorf("%w: %q", ErrNotReachable, id)
		}
		target, redirected = anc, true
	}
	plan := DecompressPlan{
		Requested:  id,
		Target:     target.ID,
		Reactivate: append([]string(nil), target.Sources...),
		Redirected: redirected,
		StartSeq:   target.StartSeq,
		EndSeq:     target.EndSeq,
	}
	sort.SliceStable(plan.Reactivate, func(i, j int) bool {
		return byID[plan.Reactivate[i]].StartSeq < byID[plan.Reactivate[j]].StartSeq
	})
	if _, err := ApplyDecompress(s.Blocks, plan); err != nil {
		return plan, err
	}
	return plan, nil
}

// ApplyDecompress returns a new block set with the plan applied, and leaves the
// input unchanged. It checks that Target is active, and that Reactivate is
// exactly Target's direct sources, all inactive. Overlap is rejected: if
// reactivation would break I1 or I2, it returns ErrInvariant and does not
// change anything.
func ApplyDecompress(blocks []Block, p DecompressPlan) ([]Block, error) {
	out := cloneBlocks(blocks)
	idx := make(map[string]int, len(out))
	for i, b := range out {
		idx[b.ID] = i
	}
	ti, ok := idx[p.Target]
	if !ok {
		return nil, fmt.Errorf("%w: target %q", ErrNoSuchBlock, p.Target)
	}
	t := &out[ti]
	if !t.Active {
		return nil, fmt.Errorf("%w: target %q is not active", ErrInvariant, p.Target)
	}
	if len(p.Reactivate) != len(t.Sources) {
		return nil, fmt.Errorf("%w: reactivate list does not match sources of %q", ErrInvariant, p.Target)
	}
	want := make(map[string]bool, len(t.Sources))
	for _, id := range t.Sources {
		want[id] = true
	}
	for _, id := range p.Reactivate {
		si, ok := idx[id]
		if !ok {
			return nil, fmt.Errorf("%w: source %q", ErrNoSuchBlock, id)
		}
		if !want[id] {
			return nil, fmt.Errorf("%w: %q is not a source of %q", ErrInvariant, id, p.Target)
		}
		if out[si].Active {
			return nil, fmt.Errorf("%w: source %q is already active", ErrInvariant, id)
		}
		out[si].Active = true
	}
	t.Active = false

	if err := CheckBlockInvariants(out); err != nil {
		return nil, err
	}
	return out, nil
}

// CheckBlockInvariants verifies the block-level invariants of spec §17.1 on an
// in-memory block set:
//
//   - I1/I2: active blocks do not overlap in [StartSeq, EndSeq] (so no message is
//     covered by two active blocks);
//   - I5: EndSeq >= StartSeq, and SumTokens < SrcTokens for every block;
//   - I6: every active T2/T3 block has sources that are one tier lower, inactive,
//     contiguous, and exactly cover its range. SrcTokens equals the sources'
//     summed SumTokens. Each source is claimed by at most one active parent.
//
// IDs must be unique and tiers must be 1..3. It returns the first violation.
func CheckBlockInvariants(blocks []Block) error {
	byID := make(map[string]Block, len(blocks))
	for _, b := range blocks {
		if _, dup := byID[b.ID]; dup {
			return fmt.Errorf("%w: duplicate id %q", ErrInvariant, b.ID)
		}
		if b.Tier < 1 || b.Tier > 3 {
			return fmt.Errorf("%w: block %q has tier %d", ErrInvariant, b.ID, b.Tier)
		}
		if b.EndSeq < b.StartSeq {
			return fmt.Errorf("%w: block %q has end before start", ErrInvariant, b.ID)
		}
		if b.SumTokens >= b.SrcTokens {
			return fmt.Errorf("%w: block %q does not shrink (I5)", ErrInvariant, b.ID)
		}
		byID[b.ID] = b
	}

	active := activeSorted(blocks)
	var maxEnd int64 = -1
	for _, b := range active {
		if b.StartSeq <= maxEnd {
			return fmt.Errorf("%w: active block %q overlaps an earlier one (I1/I2)", ErrInvariant, b.ID)
		}
		maxEnd = b.EndSeq
	}

	claimed := map[string]string{}
	for _, b := range active {
		if b.Tier == 1 {
			continue
		}
		if len(b.Sources) < 2 {
			return fmt.Errorf("%w: block %q has fewer than two sources (I6)", ErrInvariant, b.ID)
		}
		var sum int
		for i, id := range b.Sources {
			src, ok := byID[id]
			if !ok {
				return fmt.Errorf("%w: block %q lists missing source %q (I6)", ErrInvariant, b.ID, id)
			}
			if src.Tier != b.Tier-1 {
				return fmt.Errorf("%w: source %q of %q has tier %d (I6)", ErrInvariant, id, b.ID, src.Tier)
			}
			if src.Active {
				return fmt.Errorf("%w: source %q of active %q is still active (I6)", ErrInvariant, id, b.ID)
			}
			if other, dup := claimed[id]; dup {
				return fmt.Errorf("%w: source %q is claimed by %q and %q (I6)", ErrInvariant, id, other, b.ID)
			}
			claimed[id] = b.ID
			if i > 0 && byID[b.Sources[i-1]].EndSeq+1 != src.StartSeq {
				return fmt.Errorf("%w: sources of %q are not contiguous (I6)", ErrInvariant, b.ID)
			}
			sum += src.SumTokens
		}
		if byID[b.Sources[0]].StartSeq != b.StartSeq || byID[b.Sources[len(b.Sources)-1]].EndSeq != b.EndSeq {
			return fmt.Errorf("%w: range of %q is not the range of its sources (I6)", ErrInvariant, b.ID)
		}
		if b.SrcTokens != sum {
			return fmt.Errorf("%w: block %q src tokens %d, sources sum to %d", ErrInvariant, b.ID, b.SrcTokens, sum)
		}
	}
	return nil
}

func cloneBlocks(in []Block) []Block {
	out := make([]Block, len(in))
	for i, b := range in {
		b.Sources = append([]string(nil), b.Sources...)
		out[i] = b
	}
	return out
}
