package kernel

import (
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// t1 builds a T1 block over [start, end].
func t1(id string, start, end int64, src, sum int, active bool) Block {
	return Block{ID: id, Tier: 1, StartSeq: start, EndSeq: end, SrcTokens: src, SumTokens: sum, Active: active}
}

// t2 builds a T2 (or T3, by tier) block over the given sources. Its SrcTokens is
// the sum of the sources' SumTokens, as in ApplyTierPlan.
func t2(id string, tier int, sources []Block, sum int, active bool) Block {
	b := Block{ID: id, Tier: tier, StartSeq: sources[0].StartSeq, EndSeq: sources[len(sources)-1].EndSeq, SumTokens: sum, Active: active}
	for _, s := range sources {
		b.Sources = append(b.Sources, s.ID)
		b.SrcTokens += s.SumTokens
	}
	return b
}

func TestPlanTierFold(t *testing.T) {
	cfg := DefaultConfig()
	// density 0: S = 50K, threshold 1.5·S = 75K, floor F = 22.5K.
	fourBlocks := []Block{
		t1("b1", 0, 9, 40000, 20000, true),
		t1("b2", 10, 19, 40000, 20000, true),
		t1("b3", 20, 29, 40000, 20000, true),
		t1("b4", 30, 39, 40000, 20000, true),
	}

	tests := []struct {
		name     string
		blocks   []Block
		tier     int
		tokens   int
		lastT2   int
		lastT3   int
		cfg      *Config
		wantOK   bool
		wantSrcs []string
		wantSrc  int
	}{
		{
			name: "T2 fold of four contiguous blocks at threshold", blocks: fourBlocks,
			tier: 2, tokens: 100000, wantOK: true, wantSrcs: []string{"b1", "b2", "b3", "b4"}, wantSrc: 80000,
		},
		{
			name: "pending below 1.5S", blocks: fourBlocks[:3], tier: 2, tokens: 100000, wantOK: false,
		},
		{
			name: "cadence not met for T2", blocks: fourBlocks, tier: 2, tokens: 100000, lastT2: 90000, wantOK: false,
		},
		{
			name: "cadence exactly at the floor passes", blocks: fourBlocks, tier: 2, tokens: 22500,
			wantOK: true, wantSrcs: []string{"b1", "b2", "b3", "b4"}, wantSrc: 80000,
		},
		{
			name: "cadence for T3 uses LastShownT3, not T2", blocks: []Block{
				t2("b5", 2, fourBlocks[:2], 39000, true), // 78K pending: threshold met
				t2("b6", 2, fourBlocks[2:], 39000, true),
			}, tier: 3, tokens: 100000, lastT2: 0, lastT3: 90000, wantOK: false,
		},
		{
			name: "gap splits the run, no run reaches threshold",
			blocks: []Block{
				t1("b1", 0, 9, 40000, 20000, true),
				t1("b2", 10, 19, 40000, 20000, true),
				t1("b3", 25, 34, 40000, 20000, true), // gap at 20..24
				t1("b4", 35, 44, 40000, 20000, true),
			},
			tier: 2, tokens: 100000, wantOK: false,
		},
		{
			name: "run with the most pending wins over an earlier qualifying run",
			blocks: []Block{
				t1("b1", 0, 9, 40000, 30000, true),
				t1("b2", 10, 19, 40000, 30000, true),
				t1("b3", 20, 29, 40000, 30000, true), // run A: 90K
				t1("b4", 35, 44, 40000, 40000, true),
				t1("b5", 45, 54, 40000, 40000, true),
				t1("b6", 55, 64, 40000, 40000, true), // run B: 120K
			},
			tier: 2, tokens: 100000, wantOK: true, wantSrcs: []string{"b4", "b5", "b6"}, wantSrc: 120000,
		},
		{
			name: "inactive T1 blocks are not foldable",
			blocks: []Block{
				t1("b1", 0, 9, 40000, 20000, false),
				t1("b2", 10, 19, 40000, 20000, false),
				t1("b3", 20, 29, 40000, 20000, false),
				t1("b4", 30, 39, 40000, 20000, false),
			},
			tier: 2, tokens: 100000, wantOK: false,
		},
		{
			name: "T3 folds T2 blocks and reaches threshold on their summaries",
			blocks: []Block{
				t2("b5", 2, []Block{fourBlocks[0], fourBlocks[1]}, 39000, true),
				t2("b6", 2, []Block{fourBlocks[2], fourBlocks[3]}, 39000, true),
			},
			tier: 3, tokens: 100000, wantOK: true, wantSrcs: []string{"b5", "b6"}, wantSrc: 78000,
		},
		{
			name:   "T3 does not fold T1 blocks",
			blocks: fourBlocks, tier: 3, tokens: 100000, wantOK: false,
		},
		{
			name: "tiers disabled", blocks: fourBlocks, tier: 2, tokens: 100000, cfg: &Config{}, wantOK: false,
		},
		{
			name: "tier 1 is not a fold target", blocks: fourBlocks, tier: 1, tokens: 100000, wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			if tc.cfg != nil {
				c = *tc.cfg
			}
			snap := Snapshot{Blocks: tc.blocks, Trigger: TriggerState{LastShownT2: tc.lastT2, LastShownT3: tc.lastT3}}
			plan, ok := PlanTierFold(snap, c, tc.tier, dec("0"), tc.tokens)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (plan %+v)", ok, tc.wantOK, plan)
			}
			if !ok {
				return
			}
			if fmt.Sprint(plan.Sources) != fmt.Sprint(tc.wantSrcs) {
				t.Errorf("sources = %v, want %v", plan.Sources, tc.wantSrcs)
			}
			if plan.SrcTokens != tc.wantSrc {
				t.Errorf("SrcTokens = %d, want %d", plan.SrcTokens, tc.wantSrc)
			}
			if plan.Tier != tc.tier {
				t.Errorf("Tier = %d, want %d", plan.Tier, tc.tier)
			}
		})
	}
}

func TestFoldablePending(t *testing.T) {
	s := Snapshot{Blocks: []Block{
		t1("b1", 0, 9, 40000, 10000, true),
		t1("b2", 10, 19, 40000, 10000, true),
		t1("b3", 30, 39, 40000, 90000, true), // singleton run, ignored
	}}
	if got := FoldablePending(s, 2); got != 20000 {
		t.Errorf("FoldablePending(2) = %d, want 20000", got)
	}
	if got := FoldablePending(s, 3); got != 0 {
		t.Errorf("FoldablePending(3) = %d, want 0", got)
	}
	if got := FoldablePending(s, 1); got != 0 {
		t.Errorf("FoldablePending(1) = %d, want 0", got)
	}
}

func TestApplyTierPlan(t *testing.T) {
	blocks := []Block{
		t1("b1", 0, 9, 40000, 20000, true),
		t1("b2", 10, 19, 40000, 20000, true),
		t1("b3", 20, 29, 40000, 20000, true),
		t1("b4", 30, 39, 40000, 20000, true),
	}
	plan, ok := PlanTierFold(Snapshot{Blocks: blocks}, DefaultConfig(), 2, dec("0"), 100000)
	if !ok {
		t.Fatal("expected a plan")
	}

	t.Run("applies: sources inactive, new block active, I6 holds", func(t *testing.T) {
		out, err := ApplyTierPlan(blocks, plan, Block{ID: "b5", Topic: "x", Summary: "s", SumTokens: 30000})
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckBlockInvariants(out); err != nil {
			t.Fatal(err)
		}
		st := indexBlocks(out)
		if nb := st["b5"]; !nb.Active || nb.Tier != 2 || nb.SrcTokens != 80000 || nb.StartSeq != 0 || nb.EndSeq != 39 {
			t.Errorf("new block = %+v", nb)
		}
		for _, id := range []string{"b1", "b2", "b3", "b4"} {
			if st[id].Active {
				t.Errorf("%s should be inactive", id)
			}
		}
		if !blocks[0].Active || blocks[0].ID != "b1" {
			t.Error("input was mutated")
		}
	})

	t.Run("summary must shrink (I5)", func(t *testing.T) {
		if _, err := ApplyTierPlan(blocks, plan, Block{ID: "b5", SumTokens: 80000}); !errors.Is(err, ErrInvariant) {
			t.Errorf("err = %v, want ErrInvariant", err)
		}
	})

	t.Run("stale plan tokens are rejected", func(t *testing.T) {
		bad := plan
		bad.SrcTokens = 1
		if _, err := ApplyTierPlan(blocks, bad, Block{ID: "b5", SumTokens: 10}); !errors.Is(err, ErrInvariant) {
			t.Errorf("err = %v, want ErrInvariant", err)
		}
	})

	t.Run("non-contiguous sources are rejected", func(t *testing.T) {
		bad := TierPlan{Tier: 2, Sources: []string{"b1", "b3"}, StartSeq: 0, EndSeq: 29, SrcTokens: 40000}
		if _, err := ApplyTierPlan(blocks, bad, Block{ID: "b5", SumTokens: 10}); !errors.Is(err, ErrInvariant) {
			t.Errorf("err = %v, want ErrInvariant", err)
		}
	})

	t.Run("id collision is rejected", func(t *testing.T) {
		if _, err := ApplyTierPlan(blocks, plan, Block{ID: "b2", SumTokens: 10}); !errors.Is(err, ErrInvariant) {
			t.Errorf("err = %v, want ErrInvariant", err)
		}
	})
}

// decompressFixture: b5 is an active T2 over inactive T1 b1,b2. b7 is an active
// T3 over inactive T2 b6 (over T1 b3,b4) and inactive T2 b9 (over T1 b10,b11).
func decompressFixture() []Block {
	b1 := t1("b1", 0, 9, 40000, 20000, false)
	b2 := t1("b2", 10, 19, 40000, 20000, false)
	b3 := t1("b3", 20, 29, 40000, 20000, false)
	b4 := t1("b4", 30, 39, 40000, 20000, false)
	b10 := t1("b10", 40, 49, 40000, 20000, false)
	b11 := t1("b11", 50, 59, 40000, 20000, false)
	b6 := t2("b6", 2, []Block{b3, b4}, 30000, false)
	b9 := t2("b9", 2, []Block{b10, b11}, 30000, false)
	b7 := t2("b7", 3, []Block{b6, b9}, 50000, true)
	b5 := t2("b5", 2, []Block{b1, b2}, 30000, true)
	return []Block{b5, b7, b6, b9, b1, b2, b3, b4, b10, b11}
}

func TestPlanDecompress(t *testing.T) {
	blocks := decompressFixture()
	if err := CheckBlockInvariants(blocks); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}

	t.Run("active T3 reactivates its direct T2 sources only", func(t *testing.T) {
		p, err := PlanDecompress(Snapshot{Blocks: blocks}, "b7")
		if err != nil {
			t.Fatal(err)
		}
		if p.Target != "b7" || p.Redirected || fmt.Sprint(p.Reactivate) != "[b6 b9]" {
			t.Errorf("plan = %+v", p)
		}
		out, err := ApplyDecompress(blocks, p)
		if err != nil {
			t.Fatal(err)
		}
		st := indexBlocks(out)
		if st["b7"].Active || !st["b6"].Active || !st["b9"].Active || st["b3"].Active || st["b10"].Active {
			t.Errorf("state after decompress = %+v", st)
		}
		if err := CheckBlockInvariants(out); err != nil {
			t.Errorf("invariants after decompress: %v", err)
		}
	})

	t.Run("inactive T1 is redirected to its active ancestor", func(t *testing.T) {
		p, err := PlanDecompress(Snapshot{Blocks: blocks}, "b3")
		if err != nil {
			t.Fatal(err)
		}
		if !p.Redirected || p.Target != "b7" || p.Requested != "b3" {
			t.Errorf("plan = %+v, want redirect to b7", p)
		}
	})

	t.Run("active T2 reactivates its T1 sources", func(t *testing.T) {
		p, err := PlanDecompress(Snapshot{Blocks: blocks}, "b5")
		if err != nil {
			t.Fatal(err)
		}
		out, err := ApplyDecompress(blocks, p)
		if err != nil {
			t.Fatal(err)
		}
		if st := indexBlocks(out); !st["b1"].Active || !st["b2"].Active || st["b5"].Active {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("active T1 is simply deactivated", func(t *testing.T) {
		snap := []Block{t1("b1", 0, 9, 40000, 20000, true)}
		p, err := PlanDecompress(Snapshot{Blocks: snap}, "b1")
		if err != nil {
			t.Fatal(err)
		}
		out, err := ApplyDecompress(snap, p)
		if err != nil {
			t.Fatal(err)
		}
		if out[0].Active {
			t.Error("b1 should be inactive")
		}
	})

	t.Run("unknown block", func(t *testing.T) {
		if _, err := PlanDecompress(Snapshot{Blocks: blocks}, "b99"); !errors.Is(err, ErrNoSuchBlock) {
			t.Errorf("err = %v, want ErrNoSuchBlock", err)
		}
	})

	t.Run("orphan inactive block is not reachable", func(t *testing.T) {
		snap := []Block{t1("b1", 0, 9, 40000, 20000, false)}
		if _, err := PlanDecompress(Snapshot{Blocks: snap}, "b1"); !errors.Is(err, ErrNotReachable) {
			t.Errorf("err = %v, want ErrNotReachable", err)
		}
	})

	t.Run("ApplyDecompress rejects a stale or wrong plan", func(t *testing.T) {
		cases := map[string]DecompressPlan{
			"target inactive":         {Target: "b6", Reactivate: []string{"b3", "b4"}},
			"reactivate not a source": {Target: "b5", Reactivate: []string{"b1", "b3"}},
			"source already active":   {Target: "b7", Reactivate: []string{"b6", "b9"}},
		}
		// Make b9 already active so "source already active" trips on it: b9 is
		// then claimed by an active parent, which is itself invalid, so only the
		// plan-level check is under test here: use a copy with b9 active.
		withActive := append([]Block(nil), blocks...)
		for i := range withActive {
			if withActive[i].ID == "b9" {
				withActive[i].Active = true
			}
		}
		for name, p := range cases {
			src := blocks
			if name == "source already active" {
				src = withActive
			}
			if _, err := ApplyDecompress(src, p); err == nil {
				t.Errorf("%s: expected an error", name)
			}
		}
	})
}

func TestCheckBlockInvariantsRejects(t *testing.T) {
	base := func() []Block {
		b1 := t1("b1", 0, 9, 40000, 20000, false)
		b2 := t1("b2", 10, 19, 40000, 20000, false)
		return []Block{b1, b2, t2("b5", 2, []Block{b1, b2}, 30000, true)}
	}
	if err := CheckBlockInvariants(base()); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func([]Block) []Block
	}{
		{"active overlap (I1/I2)", func(b []Block) []Block {
			b[1].Active = true
			return b
		}},
		{"source still active (I6)", func(b []Block) []Block {
			b[0].Active = true
			return b
		}},
		{"source claimed by two parents (I6)", func(b []Block) []Block {
			return append(b, Block{ID: "b6", Tier: 2, StartSeq: 0, EndSeq: 19, SrcTokens: 40000, SumTokens: 100, Active: true, Sources: []string{"b1", "b2"}})
		}},
		{"src tokens disagree with sources (I6)", func(b []Block) []Block {
			b[2].SrcTokens = 1
			return b
		}},
		{"summary does not shrink (I5)", func(b []Block) []Block {
			b[2].SumTokens = b[2].SrcTokens
			return b
		}},
		{"missing source (I6)", func(b []Block) []Block {
			b[2].Sources = []string{"b1", "b9"}
			return b
		}},
		{"duplicate id", func(b []Block) []Block {
			b[1].ID = "b1"
			return b
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckBlockInvariants(tc.mutate(base())); !errors.Is(err, ErrInvariant) {
				t.Errorf("err = %v, want ErrInvariant", err)
			}
		})
	}
}

// --- property tests (spec §17.2) ---

// checkI1Messages is the literal I1 check: no message is covered by two active blocks.
func checkI1Messages(blocks []Block, maxSeq int64) error {
	for seq := int64(0); seq <= maxSeq; seq++ {
		n := 0
		for _, b := range blocks {
			if b.Active && seq >= b.StartSeq && seq <= b.EndSeq {
				n++
			}
		}
		if n > 1 {
			return fmt.Errorf("message %d covered by %d active blocks", seq, n)
		}
	}
	return nil
}

// genState draws T1 blocks with gaps, some active. Then it folds random adjacent
// pairs into active T2 blocks, so tier 3 has material as well.
func genState(t *rapid.T) []Block {
	n := rapid.IntRange(2, 14).Draw(t, "n")
	var t1s []Block
	seq := int64(0)
	for i := 0; i < n; i++ {
		if rapid.IntRange(0, 3).Draw(t, "gap") == 0 { // gaps are rare, so T2 runs form
			seq++
		}
		span := int64(rapid.IntRange(1, 4).Draw(t, "span"))
		src := rapid.IntRange(2000, 30000).Draw(t, "src")
		sum := rapid.IntRange(1, src-1).Draw(t, "sum")
		t1s = append(t1s, Block{
			ID: fmt.Sprintf("b%d", i+1), Tier: 1,
			StartSeq: seq, EndSeq: seq + span - 1,
			SrcTokens: src, SumTokens: sum,
			Active: rapid.Bool().Draw(t, "active"),
		})
		seq += span
	}

	var out []Block
	for i := 0; i < len(t1s); i++ {
		a := t1s[i]
		if i+1 < len(t1s) && a.EndSeq+1 == t1s[i+1].StartSeq && rapid.IntRange(0, 1).Draw(t, "pair") == 0 {
			b := t1s[i+1]
			a.Active, b.Active = false, false
			srcSum := a.SumTokens + b.SumTokens
			t2b := Block{
				ID: "t2_" + a.ID, Tier: 2, StartSeq: a.StartSeq, EndSeq: b.EndSeq,
				SrcTokens: srcSum, SumTokens: rapid.IntRange(1, srcSum-1).Draw(t, "t2sum"),
				Active: true, Sources: []string{a.ID, b.ID},
			}
			out = append(out, a, b, t2b)
			i++
			continue
		}
		out = append(out, a)
	}
	if err := CheckBlockInvariants(out); err != nil {
		t.Fatalf("generator produced an invalid state: %v", err)
	}
	return out
}

// scaledConfig makes the step and floor small, so folds fire in short sequences.
func scaledConfig() Config {
	cfg := DefaultConfig()
	cfg.StepBase, cfg.StepMin, cfg.StepMax = 100, 100, 100
	cfg.MinGrowthFloor = 10
	return cfg
}

// TestTierPlansKeepInvariantsProperty applies random plans and decompresses to
// random states. After every step, I1 (message level and block level), I2, I5
// and I6 must hold. The test also requires that folds and decompresses really
// happened, so it cannot pass vacuously. A T3 fold needs two adjacent T2
// blocks, so a single run of 100 cases misses it about once in 50 random
// seeds; the check runs up to vacuityRounds times until every count is set.
func TestTierPlansKeepInvariantsProperty(t *testing.T) {
	cfg := scaledConfig()
	var folds2, folds3, decomps int
	const vacuityRounds = 5
	for round := 0; round < vacuityRounds && (folds2 == 0 || folds3 == 0 || decomps == 0); round++ {
		checkTierPlans(t, cfg, &folds2, &folds3, &decomps)
	}
	if folds2 == 0 || folds3 == 0 || decomps == 0 {
		t.Fatalf("property is vacuous after %d rounds: folds2=%d folds3=%d decompresses=%d", vacuityRounds, folds2, folds3, decomps)
	}
}

// checkTierPlans is one rapid run of TestTierPlansKeepInvariantsProperty. It
// adds the folds and decompresses it applied to the counters.
func checkTierPlans(t *testing.T, cfg Config, folds2, folds3, decomps *int) {
	rapid.Check(t, func(t *rapid.T) {
		blocks := genState(t)
		var maxSeq int64
		for _, b := range blocks {
			maxSeq = max(maxSeq, b.EndSeq)
		}
		nextID := len(blocks) + 1
		steps := rapid.IntRange(1, 25).Draw(t, "steps")

		for step := 0; step < steps; step++ {
			snap := Snapshot{Blocks: blocks, Trigger: TriggerState{
				LastShownT2: rapid.IntRange(0, 300).Draw(t, "lastT2"),
				LastShownT3: rapid.IntRange(0, 300).Draw(t, "lastT3"),
			}}
			switch rapid.IntRange(0, 2).Draw(t, "op") {
			case 0, 1:
				tier := 2 + rapid.IntRange(0, 1).Draw(t, "tier")
				density := quo(intDec(rapid.IntRange(0, 100).Draw(t, "density")), intDec(100))
				tokens := rapid.IntRange(0, 400).Draw(t, "tokens")
				plan, ok := PlanTierFold(snap, cfg, tier, density, tokens)
				if !ok {
					continue
				}
				nb := Block{
					ID: fmt.Sprintf("b%d", nextID), Topic: "t", Summary: "s",
					SumTokens: rapid.IntRange(1, plan.SrcTokens-1).Draw(t, "sumTokens"),
				}
				next, err := ApplyTierPlan(blocks, plan, nb)
				if err != nil {
					t.Fatalf("a plan from PlanTierFold must apply: %v (plan %+v)", err, plan)
				}
				nextID++
				blocks = next
				if tier == 2 {
					*folds2++
				} else {
					*folds3++
				}
			case 2:
				id := blocks[rapid.IntRange(0, len(blocks)-1).Draw(t, "pick")].ID
				plan, err := PlanDecompress(snap, id)
				if errors.Is(err, ErrNotReachable) {
					continue
				}
				if err != nil {
					t.Fatalf("PlanDecompress(%s): %v", id, err)
				}
				next, err := ApplyDecompress(blocks, plan)
				if err != nil {
					t.Fatalf("ApplyDecompress(%+v): %v", plan, err)
				}
				blocks = next
				*decomps++
			}

			if err := CheckBlockInvariants(blocks); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			if err := checkI1Messages(blocks, maxSeq); err != nil {
				t.Fatalf("step %d: I1: %v", step, err)
			}
		}
	})
}

// TestPlanFoldAgreesWithDecideNudge checks that the kernel never nudges a tier
// it cannot plan (spec §10.2): whenever DecideNudge asks for a T2 or T3 fold,
// PlanFold must return a plan that ApplyTierPlan accepts.
func TestPlanFoldAgreesWithDecideNudge(t *testing.T) {
	cfg := DefaultConfig()
	nudged := map[int]int{}
	rapid.Check(t, func(t *rapid.T) {
		blocks := genMixed(t)
		// Several decisions per generated state: each draws its own usage and
		// pending values, which keeps the state generation cost down.
		for i := 0; i < 25; i++ {
			snap := Snapshot{Blocks: blocks, Trigger: TriggerState{
				LastShownT2: rapid.IntRange(0, 400000).Draw(t, "lastT2"),
				LastShownT3: rapid.IntRange(0, 400000).Draw(t, "lastT3"),
			}}
			// A small limit puts usage in the high and emergency bands often, so the
			// pressure rows (1 and 3) are reached, not only the growth rows (7 and 8).
			limit := rapid.IntRange(1000, 20000).Draw(t, "limit")
			tokens := rapid.IntRange(0, 2*limit).Draw(t, "tokens")
			density := quo(intDec(rapid.IntRange(0, 100).Draw(t, "density")), intDec(100))
			in := TriggerInput{
				TokenCount:   tokens,
				ContextLimit: limit,
				ReadDensity:  density,
				Pending: map[int]int{
					1: rapid.IntRange(0, 4000).Draw(t, "p1"), // small, so tiers 2 and 3 can win the argmax
					2: FoldablePending(snap, 2),
					3: FoldablePending(snap, 3),
				},
				Trigger: snap.Trigger,
			}
			d := DecideNudge(in, cfg)
			if !d.Nudge || d.Tier < 2 {
				continue
			}
			nudged[d.Tier]++
			plan, ok := PlanFold(snap, cfg, d, limit, density, tokens)
			if !ok {
				t.Fatalf("decision %s asks for tier %d but PlanFold has no plan (pending %v)", d.Row, d.Tier, in.Pending)
			}
			nb := Block{ID: "bx", SumTokens: rapid.IntRange(1, plan.SrcTokens-1).Draw(t, "sum")}
			if _, err := ApplyTierPlan(blocks, plan, nb); err != nil {
				t.Fatalf("decision %s: plan does not apply: %v", d.Row, err)
			}
		}
	})
	if nudged[2]+nudged[3] == 0 {
		t.Fatalf("property is vacuous: no T2 or T3 nudge in any run (%v)", nudged)
	}
}

// TestPlanFoldRows pins one state per DecideNudge row that asks for a fold. Each
// must produce a plan that ApplyTierPlan accepts.
func TestPlanFoldRows(t *testing.T) {
	cfg := DefaultConfig()
	fourT1 := []Block{
		t1("b1", 0, 9, 40000, 20000, true),
		t1("b2", 10, 19, 40000, 20000, true),
		t1("b3", 20, 29, 40000, 20000, true),
		t1("b4", 30, 39, 40000, 20000, true),
	}
	twoT2 := []Block{
		t2("b5", 2, []Block{t1("b1", 0, 9, 40000, 20000, false), t1("b2", 10, 19, 40000, 20000, false)}, 39000, true),
		t2("b6", 2, []Block{t1("b3", 20, 29, 40000, 20000, false), t1("b4", 30, 39, 40000, 20000, false)}, 39000, true),
	}
	tests := []struct {
		name     string
		blocks   []Block
		limit    int
		tokens   int
		in       map[int]int
		wantRow  string
		wantTier int
	}{
		{name: "row1 emergency fold into T2", blocks: fourT1[:2], limit: 10000, tokens: 9600,
			in: map[int]int{2: 6000}, wantRow: "row1", wantTier: 2},
		{name: "row3 high band fold into T2", blocks: fourT1[:2], limit: 10000, tokens: 8000,
			in: map[int]int{2: 6000}, wantRow: "row3", wantTier: 2},
		{name: "row7 growth fold into T2", blocks: fourT1, limit: 200000, tokens: 100000,
			in: map[int]int{1: 0, 2: 80000}, wantRow: "row7", wantTier: 2},
		{name: "row8 growth fold into T3", blocks: twoT2, limit: 200000, tokens: 100000,
			in: map[int]int{1: 0, 2: 0, 3: 78000}, wantRow: "row8", wantTier: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := Snapshot{Blocks: tc.blocks}
			in := TriggerInput{TokenCount: tc.tokens, ContextLimit: tc.limit, ReadDensity: dec("0"), Pending: tc.in}
			d := DecideNudge(in, cfg)
			if !d.Nudge || d.Row != tc.wantRow || d.Tier != tc.wantTier {
				t.Fatalf("decision = %+v, want %s tier %d", d, tc.wantRow, tc.wantTier)
			}
			plan, ok := PlanFold(snap, cfg, d, tc.limit, dec("0"), tc.tokens)
			if !ok {
				t.Fatalf("PlanFold has no plan for %s", d.Row)
			}
			if plan.Tier != tc.wantTier {
				t.Errorf("plan tier = %d, want %d", plan.Tier, tc.wantTier)
			}
			if _, err := ApplyTierPlan(tc.blocks, plan, Block{ID: "bx", SumTokens: 1}); err != nil {
				t.Errorf("plan does not apply: %v", err)
			}
		})
	}

	t.Run("PlanFold ignores non-fold decisions", func(t *testing.T) {
		snap := Snapshot{Blocks: fourT1}
		for _, d := range []Decision{{}, {Nudge: true, Tier: 1, Row: "row6"}, {Nudge: true, Tier: 2, Row: "row9"}} {
			if _, ok := PlanFold(snap, cfg, d, 200000, dec("0"), 100000); ok {
				t.Errorf("decision %+v should not plan a fold", d)
			}
		}
	})
}

// genRuns draws contiguous T1 blocks, most of them active, so tier 2 has
// candidate runs. genState alone rarely leaves adjacent active blocks.
func genRuns(t *rapid.T) []Block {
	n := rapid.IntRange(2, 12).Draw(t, "n")
	var out []Block
	seq := int64(0)
	for i := 0; i < n; i++ {
		src := rapid.IntRange(2000, 30000).Draw(t, "src")
		sum := rapid.IntRange(1, src-1).Draw(t, "sum")
		out = append(out, Block{
			ID: fmt.Sprintf("b%d", i+1), Tier: 1,
			StartSeq: seq, EndSeq: seq + 3,
			SrcTokens: src, SumTokens: sum,
			Active: rapid.IntRange(0, 4).Draw(t, "active") != 0,
		})
		seq += 4
	}
	return out
}

// genMixed alternates between genRuns (tier 2 candidates) and genState (which
// also has T2 blocks, so tier 3 gets candidates).
func genMixed(t *rapid.T) []Block {
	if rapid.Bool().Draw(t, "mixed") {
		return genState(t)
	}
	return genRuns(t)
}
