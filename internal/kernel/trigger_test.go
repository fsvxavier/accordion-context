package kernel

import "testing"

func TestDecideNudge(t *testing.T) {
	const L = 200_000
	cfg := DefaultConfig()
	// With ReadDensity 0, S = 50K, F = max(20K, 22.5K) = 22.5K -> 22500.
	base := TriggerInput{ContextLimit: L, Pending: map[int]int{}}

	tests := []struct {
		name    string
		in      TriggerInput
		wantRow string
		nudge   bool
		tier    int
		voice   Voice
	}{
		{
			name:    "row1 emergency with compressible",
			in:      TriggerInput{ContextLimit: L, TokenCount: 195_000, Pending: map[int]int{1: 60_000}},
			wantRow: "row1", nudge: true, tier: 1, voice: VoiceEmergency,
		},
		{
			name:    "row2 emergency nothing compressible",
			in:      TriggerInput{ContextLimit: L, TokenCount: 195_000, Pending: map[int]int{1: 100}},
			wantRow: "row2",
		},
		{
			name:    "row3 high band with compressible",
			in:      TriggerInput{ContextLimit: L, TokenCount: 160_000, Pending: map[int]int{2: 30_000, 1: 10_000}},
			wantRow: "row3", nudge: true, tier: 2, voice: VoiceFirm,
		},
		{
			name:    "high band but pending below Bmin falls through",
			in:      TriggerInput{ContextLimit: L, TokenCount: 150_000, Pending: map[int]int{1: 2_000}},
			wantRow: "row9",
		},
		{
			name:    "row4 below usage floor even with growth",
			in:      TriggerInput{ContextLimit: L, TokenCount: 50_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{1: 60_000}},
			wantRow: "row4",
		},
		{
			name:    "row5 growth below floor",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 90_000}, Pending: map[int]int{1: 60_000}},
			wantRow: "row5",
		},
		{
			name:    "row5 growth exactly at floor is not blocked",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 77_500}, Pending: map[int]int{1: 60_000}},
			wantRow: "row6", nudge: true, tier: 1, voice: VoiceGentle,
		},
		{
			name:    "row6 T1 pending reaches step",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{1: 50_000}},
			wantRow: "row6", nudge: true, tier: 1, voice: VoiceGentle,
		},
		{
			name:    "row6 pending just under step falls through",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{1: 49_999}},
			wantRow: "row9",
		},
		{
			name:    "row7 T2 beats T1 and cadence met",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0, LastShownT2: 0}, Pending: map[int]int{1: 10_000, 2: 75_000}},
			wantRow: "row7", nudge: true, tier: 2, voice: VoiceGentle,
		},
		{
			name:    "row7 blocked by cadence",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0, LastShownT2: 90_000}, Pending: map[int]int{2: 75_000}},
			wantRow: "row9",
		},
		{
			name:    "row7 not chosen when T2 not above T1",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{1: 20_000, 2: 20_000}},
			wantRow: "row9",
		},
		{
			name:    "row8 T3 dominates",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{1: 10_000, 2: 20_000, 3: 80_000}},
			wantRow: "row8", nudge: true, tier: 3, voice: VoiceGentle,
		},
		{
			name:    "equal P2 and P3: row7 checked first wins",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{2: 80_000, 3: 80_000}},
			wantRow: "row7", nudge: true, tier: 2, voice: VoiceGentle,
		},
		{
			name:    "row9 nothing pending",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{}},
			wantRow: "row9",
		},
		{
			name:    "refusals suspend nudges below UHigh",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0, Refusals: 3}, Pending: map[int]int{1: 60_000}},
			wantRow: "refusal",
		},
		{
			name:    "refusals do not suspend emergency",
			in:      TriggerInput{ContextLimit: L, TokenCount: 195_000, Trigger: TriggerState{Refusals: 3}, Pending: map[int]int{1: 60_000}},
			wantRow: "row1", nudge: true, tier: 1, voice: VoiceEmergency,
		},
		{
			name:    "nudge pending halves growth floor",
			in:      TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 88_000, NudgePending: true}, Pending: map[int]int{1: 60_000}},
			wantRow: "row6", nudge: true, tier: 1, voice: VoiceGentle,
		},
		{
			name:    "zero context limit guard",
			in:      TriggerInput{ContextLimit: 0, TokenCount: 100_000, Pending: map[int]int{1: 60_000}},
			wantRow: "guard",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			if in.Pending == nil {
				in.Pending = base.Pending
			}
			got := DecideNudge(in, cfg)
			if got.Row != tt.wantRow {
				t.Fatalf("row = %q (%s), want %q", got.Row, got.Reason, tt.wantRow)
			}
			if got.Nudge != tt.nudge || got.Tier != tt.tier || got.Voice != tt.voice {
				t.Fatalf("got %+v, want nudge=%v tier=%d voice=%q", got, tt.nudge, tt.tier, tt.voice)
			}
		})
	}
}

func TestDecideNudgeTiersDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TiersEnabled = false
	in := TriggerInput{ContextLimit: 200_000, TokenCount: 100_000, Pending: map[int]int{2: 80_000}}
	if got := DecideNudge(in, cfg); got.Row != "row9" || got.Nudge {
		t.Fatalf("tiers disabled: got %+v, want row9 no nudge", got)
	}
}

func TestAdaptiveStep(t *testing.T) {
	cfg := DefaultConfig()
	tests := []struct {
		name    string
		density string
		want    int
	}{
		{"pure dialogue", "0", 50_000},
		{"half reads", "0.5", 75_000},
		{"all reads", "1", 100_000},
		{"over 1 clamps to max", "3", 100_000},
		{"negative clamps to min", "-1", 30_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveStep(cfg, dec(tt.density)); got != tt.want {
				t.Fatalf("adaptiveStep(%s) = %d, want %d", tt.density, got, tt.want)
			}
		})
	}
}

func TestGrowthFloor(t *testing.T) {
	cfg := DefaultConfig()
	tests := []struct {
		step, want int
	}{
		{50_000, 22_500},
		{30_000, 20_000},
		{100_000, 45_000},
	}
	for _, tt := range tests {
		if got := growthFloor(cfg, tt.step); got != tt.want {
			t.Fatalf("growthFloor(%d) = %d, want %d", tt.step, got, tt.want)
		}
	}
}

func TestAdaptiveStepChangesThreshold(t *testing.T) {
	// Read-heavy session (S = 100K) needs 100K of growth to trigger row6;
	// the same pending amount in a dialogue session (S = 50K) already triggers.
	cfg := DefaultConfig()
	in := TriggerInput{ContextLimit: 200_000, TokenCount: 100_000, Trigger: TriggerState{LowWater: 0}, Pending: map[int]int{1: 60_000}}
	if got := DecideNudge(in, cfg); !got.Nudge || got.Row != "row6" {
		t.Fatalf("dialogue: got %+v, want row6", got)
	}
	in.ReadDensity = dec("1")
	if got := DecideNudge(in, cfg); got.Nudge || got.Row != "row9" {
		t.Fatalf("read-heavy: got %+v, want row9", got)
	}
}

func TestUpdateLowWater(t *testing.T) {
	tests := []struct {
		name          string
		prev, current int
		want          int
	}{
		{"unset takes current", 0, 40_000, 40_000},
		{"valley descends", 40_000, 30_000, 30_000},
		{"valley holds on growth", 30_000, 70_000, 30_000},
		{"equal keeps value", 30_000, 30_000, 30_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UpdateLowWater(tt.prev, tt.current); got != tt.want {
				t.Fatalf("UpdateLowWater(%d,%d) = %d, want %d", tt.prev, tt.current, got, tt.want)
			}
		})
	}
}

// Boundaries of spec §17.2 item 1: S=50K, F=22.5K, Bmin=2K->5K (L=200K), L=200K.
func TestDecideNudgeBoundaries(t *testing.T) {
	const L = 200_000
	cfg := DefaultConfig()
	pend := map[int]int{1: 60_000}
	tests := []struct {
		name string
		in   TriggerInput
		row  string
	}{
		{"U=UEmerg-1 is firm", TriggerInput{ContextLimit: L, TokenCount: 189_999, Pending: pend}, "row3"},
		{"U=UEmerg exactly is emergency", TriggerInput{ContextLimit: L, TokenCount: 190_000, Pending: pend}, "row1"},
		{"U=UHigh exactly is firm", TriggerInput{ContextLimit: L, TokenCount: 150_000, Pending: pend}, "row3"},
		{"U=UHigh-1 not firm", TriggerInput{ContextLimit: L, TokenCount: 149_999, Pending: pend}, "row6"},
		{"Bmin-1 at emergency", TriggerInput{ContextLimit: L, TokenCount: 190_000, Pending: map[int]int{1: 4_999}}, "row2"},
		{"Bmin exactly at emergency", TriggerInput{ContextLimit: L, TokenCount: 190_000, Pending: map[int]int{1: 5_000}}, "row1"},
		{"U=UFloor exactly is not row4", TriggerInput{ContextLimit: L, TokenCount: 60_000, Pending: pend}, "row6"},
		{"U just under UFloor is row4", TriggerInput{ContextLimit: L, TokenCount: 59_999, Pending: pend}, "row4"},
		{"G=F-1 blocked", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 77_501}, Pending: pend}, "row5"},
		{"G=F passes", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 77_500}, Pending: pend}, "row6"},
		{"pending: G=F/2-1 blocked", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 88_751, NudgePending: true}, Pending: pend}, "row5"},
		{"pending: G=F/2 passes", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LowWater: 88_750, NudgePending: true}, Pending: pend}, "row6"},
		{"Refusals=2 still nudges", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{Refusals: 2}, Pending: pend}, "row6"},
		{"Refusals=3 suspended", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{Refusals: 3}, Pending: pend}, "refusal"},
		{"Refusals=3 resumes at UHigh", TriggerInput{ContextLimit: L, TokenCount: 150_000, Trigger: TriggerState{Refusals: 3}, Pending: pend}, "row3"},
		{"P1=S-1 no", TriggerInput{ContextLimit: L, TokenCount: 100_000, Pending: map[int]int{1: 49_999}}, "row9"},
		{"P1=S yes", TriggerInput{ContextLimit: L, TokenCount: 100_000, Pending: map[int]int{1: 50_000}}, "row6"},
		{"P2=1.5S-1 no", TriggerInput{ContextLimit: L, TokenCount: 100_000, Pending: map[int]int{2: 74_999}}, "row9"},
		{"P2=1.5S yes", TriggerInput{ContextLimit: L, TokenCount: 100_000, Pending: map[int]int{2: 75_000}}, "row7"},
		{"P2==P1 not >", TriggerInput{ContextLimit: L, TokenCount: 100_000, Pending: map[int]int{1: 49_000, 2: 49_000}}, "row9"},
		{"T2 cadence F-1 blocked", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LastShownT2: 77_501}, Pending: map[int]int{2: 75_000}}, "row9"},
		{"T2 cadence F ok", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LastShownT2: 77_500}, Pending: map[int]int{2: 75_000}}, "row7"},
		{"T3 cadence F-1 blocked", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LastShownT3: 77_501}, Pending: map[int]int{3: 75_000}}, "row9"},
		{"T3 cadence F ok", TriggerInput{ContextLimit: L, TokenCount: 100_000, Trigger: TriggerState{LastShownT3: 77_500}, Pending: map[int]int{3: 75_000}}, "row8"},
		{"P3==P2 not >", TriggerInput{ContextLimit: L, TokenCount: 100_000, Pending: map[int]int{2: 74_000, 3: 74_000}}, "row9"},
		{"negative limit guard", TriggerInput{ContextLimit: -1, TokenCount: 1}, "guard"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DecideNudge(tt.in, cfg); got.Row != tt.row {
				t.Fatalf("row = %q (%s), want %q", got.Row, got.Reason, tt.row)
			}
		})
	}
}

func TestDecideNudgeUFloorZeroDisablesFloor(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UFloor = dec("0")
	got := DecideNudge(TriggerInput{ContextLimit: 200_000, TokenCount: 10_000, Pending: map[int]int{1: 60_000}, Trigger: TriggerState{LowWater: 0}}, cfg)
	if got.Row == "row4" {
		t.Fatalf("floor still active: %+v", got)
	}
}
