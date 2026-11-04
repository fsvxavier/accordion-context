package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cockroachdb/apd/v3"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// withStore returns a session on the store of s, built with opts.
func withStore(t *testing.T, s *Session, opts Options) *Session {
	t.Helper()
	return NewWithOptions(s.st, s.sid, opts)
}

func TestNewKeepsSpecDefaults(t *testing.T) {
	s, _ := newSession(t)
	got := s.Settings()
	if got.Kernel.StepBase != 50_000 || !got.Kernel.TiersEnabled {
		t.Fatalf("kernel defaults not used: %+v", got.Kernel)
	}
	if got.MinCompressRange != 5_000 || got.MinSummaryChars != 50 || got.RecentKeep != recentKeep {
		t.Fatalf("minimums not defaulted: %+v", got)
	}
}

func TestRecentKeepFromOptionsDecidesWhatCompresses(t *testing.T) {
	s, _ := ingested(t)
	// Messages 20..27 end inside the default protected zone (last 10 of 30), so the
	// default session refuses them; a session that keeps 3 accepts them.
	req := CompressArgs{Ranges: []CompressRange{{StartRef: "m00020", EndRef: "m00027", Summary: okSummary}}}
	if _, err := s.HandleCompress(context.Background(), req); err == nil || !strings.Contains(err.Error(), "protected recent zone") {
		t.Fatalf("default session: err %v, want protected recent zone", err)
	}
	keep3 := withStore(t, s, Options{RecentKeep: 3})
	if _, err := keep3.HandleCompress(context.Background(), req); err != nil {
		t.Fatalf("RecentKeep 3: %v", err)
	}
}

func TestMinimumsFromOptionsAreEnforced(t *testing.T) {
	s, _ := ingested(t)
	ctx := context.Background()
	req := CompressArgs{Ranges: []CompressRange{{StartRef: "m00010", EndRef: "m00014", Summary: okSummary}}}
	big := withStore(t, s, Options{MinCompressRange: 100_000})
	if _, err := big.HandleCompress(ctx, req); err == nil || !strings.Contains(err.Error(), "minimum is 100000") {
		t.Fatalf("MinCompressRange: err %v", err)
	}
	long := withStore(t, s, Options{MinSummaryChars: 500})
	if _, err := long.HandleCompress(ctx, req); err == nil || !strings.Contains(err.Error(), "minimum is 500") {
		t.Fatalf("MinSummaryChars: err %v", err)
	}
	if _, err := s.HandleCompress(ctx, req); err != nil {
		t.Fatalf("default session should accept the same range: %v", err)
	}
}

func TestTiersSwitchFromOptions(t *testing.T) {
	s, _ := ingested(t)
	k := kernel.DefaultConfig()
	k.TiersEnabled = false
	off := withStore(t, s, Options{Kernel: k})
	_, err := off.HandleCompress(context.Background(), CompressArgs{Folds: []FoldRange{{Tier: 2, StartBlock: "b1", EndBlock: "b2", Summary: okSummary}}})
	if err == nil || !strings.Contains(err.Error(), "tiers are disabled") {
		t.Fatalf("err %v, want tiers are disabled", err)
	}
}

func TestSetOptionsChangesLiveSession(t *testing.T) {
	s, _ := ingested(t)
	s.SetOptions(Options{RecentKeep: 4})
	if got := s.Settings(); got.RecentKeep != 4 || got.MinCompressRange != 5_000 {
		t.Fatalf("SetOptions: %+v", got)
	}
	req := CompressArgs{Ranges: []CompressRange{{StartRef: "m00020", EndRef: "m00026", Summary: okSummary}}}
	if _, err := s.HandleCompress(context.Background(), req); err != nil {
		t.Fatalf("live session with RecentKeep 4: %v", err)
	}
}

func TestOptionsKernelReachesDecideNudge(t *testing.T) {
	ctx := context.Background()
	s, _ := ingested(t) // ContextLimit 4000, 30 x 1000 tokens of pending T1
	// U = 3900/4000 = 0.975: above the default emergency band (0.95), below 0.99.
	d, err := s.Plan(ctx, 3900)
	if err != nil {
		t.Fatal(err)
	}
	if d.Decision.Row != "row1" {
		t.Fatalf("default kernel: row %s, want row1", d.Decision.Row)
	}

	k := kernel.DefaultConfig()
	k.UEmerg = mustDec(t, "0.99")
	custom := withStore(t, s, Options{Kernel: k})
	// Undo the first nudge so the trigger state is comparable.
	if err := s.DeferNudge(ctx, d); err != nil {
		t.Fatal(err)
	}
	d2, err := custom.Plan(ctx, 3900)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Decision.Row != "row3" {
		t.Fatalf("custom UEmerg: row %s, want row3", d2.Decision.Row)
	}
}

func mustDec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDeferNudgeRestoresTriggerBeforePlan(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t)
	p, err := s.Plan(ctx, 3900)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Decision.Nudge || !p.After.NudgePending {
		t.Fatalf("expected a shown nudge, got %+v", p.Decision)
	}
	if err := s.DeferNudge(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTrigger(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	want := p.Before
	want.LowWater = p.LowWater
	if got != want {
		t.Fatalf("trigger after undo = %+v, want %+v", got, want)
	}
}

func TestDeferNudgeIsSerializedAndRefusesStaleUndo(t *testing.T) {
	ctx := context.Background()
	s, _ := ingested(t)
	p, err := s.Plan(ctx, 3900)
	if err != nil {
		t.Fatal(err)
	}
	var wins, stale, other atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := s.DeferNudge(ctx, p); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrNudgeStale):
				stale.Add(1)
			default:
				other.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || stale.Load() != 7 || other.Load() != 0 {
		t.Fatalf("wins %d stale %d other %d; want exactly one undo", wins.Load(), stale.Load(), other.Load())
	}
}

func TestDeferNudgeDoesNotClobberLaterChange(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t)
	p, err := s.Plan(ctx, 3900)
	if err != nil {
		t.Fatal(err)
	}
	// A later turn wrote a different trigger state before the deferral ran.
	later := p.After
	later.Refusals = 2
	if err := st.SetTrigger(ctx, "s1", later); err != nil {
		t.Fatal(err)
	}
	if err := s.DeferNudge(ctx, p); !errors.Is(err, ErrNudgeStale) {
		t.Fatalf("err %v, want ErrNudgeStale", err)
	}
	got, _ := st.GetTrigger(ctx, "s1")
	if got != later {
		t.Fatalf("later change was overwritten: %+v", got)
	}
}
