package kernel

import "testing"

func TestEstimateFold(t *testing.T) {
	m := DefaultCostModel()
	tests := []struct {
		name                  string
		src, sum, tail, calls int
		wantWorth             bool
	}{
		// Spec §9.1 worked example: R=50K, T=60K, K=20 -> benefit 100K, cost 54K -> worth.
		{"spec example worth", 100_000, 50_000, 60_000, 20, true},
		// K=5 -> benefit 25K, cost 54K*1.5 -> not worth.
		{"spec example not worth", 100_000, 50_000, 60_000, 5, false},
		{"no recovery never worth", 50_000, 50_000, 60_000, 100, false},
		{"summary larger than source recovers zero", 40_000, 50_000, 10_000, 100, false},
		{"no calls left not worth", 100_000, 0, 1_000, 0, false},
		{"zero tail is always worth when recovering", 100_000, 0, 0, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateFold(m, tt.src, tt.sum, tt.tail, tt.calls)
			if got.Worth != tt.wantWorth {
				t.Fatalf("Worth = %v (cost %.0f benefit %.0f), want %v", got.Worth, got.Cost, got.Benefit, tt.wantWorth)
			}
		})
	}
}

func TestEstimateFoldValues(t *testing.T) {
	got := EstimateFold(DefaultCostModel(), 100_000, 50_000, 60_000, 20)
	if got.Recovered != 50_000 || got.Cost != 54_000 || got.Benefit != 100_000 {
		t.Fatalf("got %+v, want R=50000 cost=54000 benefit=100000", got)
	}
}

func TestExpectedCalls(t *testing.T) {
	tests := []struct {
		name   string
		gaps   []int
		factor float64
		limit  int
		want   int
	}{
		{"no history", nil, 2, 50, 0},
		{"odd median", []int{3, 9, 5}, 2, 50, 10},
		{"even median averages middle pair", []int{4, 8, 2, 6}, 1, 50, 5},
		{"clamped to limit", []int{100}, 1, 20, 20},
		{"factor rounds", []int{5}, 1.5, 50, 8},
		{"does not mutate input order", []int{9, 1, 5}, 1, 50, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpectedCalls(tt.gaps, tt.factor, tt.limit); got != tt.want {
				t.Fatalf("ExpectedCalls = %d, want %d", got, tt.want)
			}
		})
	}
	gaps := []int{9, 1, 5}
	ExpectedCalls(gaps, 1, 50)
	if gaps[0] != 9 || gaps[1] != 1 || gaps[2] != 5 {
		t.Fatalf("input mutated: %v", gaps)
	}
}

func TestGateNudge(t *testing.T) {
	gentle := Decision{Nudge: true, Voice: VoiceGentle}
	firm := Decision{Nudge: true, Voice: VoiceFirm}
	emerg := Decision{Nudge: true, Voice: VoiceEmergency}
	tests := []struct {
		name     string
		d        Decision
		worth    bool
		deferred bool
		idle     int
		ttl      int
		want     GateAction
	}{
		{"no nudge", Decision{}, true, false, 0, 300, GateNone},
		{"worth gentle injects", gentle, true, false, 0, 300, GateInject},
		{"not worth gentle defers", gentle, false, false, 0, 300, GateDefer},
		{"deferred and cache cold injects", gentle, false, true, 301, 300, GateInject},
		{"deferred but cache still warm stays deferred", gentle, false, true, 300, 300, GateDefer},
		{"firm never deferred", firm, false, false, 0, 300, GateInject},
		{"emergency never deferred", emerg, false, false, 0, 300, GateInject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GateNudge(tt.d, tt.worth, tt.deferred, tt.idle, tt.ttl); got != tt.want {
				t.Fatalf("GateNudge = %d, want %d", got, tt.want)
			}
		})
	}
}
