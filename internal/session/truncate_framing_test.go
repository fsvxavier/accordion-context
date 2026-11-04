package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

func TestPlanTruncatesOverLimitAndLogsEvent(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t) // ContextLimit 4000; 30 messages of 1000 tokens

	// Calibrated 3000 is within the limit: nothing is truncated, no event.
	plan, err := s.Plan(ctx, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Visible) != 30 {
		t.Fatalf("within limit: visible=%d, want 30", len(plan.Visible))
	}
	if n := countEvents(t, ctx, st, "truncate"); n != 0 {
		t.Fatalf("within limit: %d truncate events", n)
	}

	// 9000 calibrated tokens over 30000 raw: each message counts 300 tokens.
	plan, err = s.Plan(ctx, 9000)
	if err != nil {
		t.Fatal(err)
	}
	if got := scaledSum(plan.Visible, 9000, 30000); got > 4000 {
		t.Fatalf("visible window %d tokens exceeds limit 4000", got)
	}
	if !hasMarker(plan.Visible) {
		t.Fatal("over limit: marker missing from the window")
	}
	if len(plan.Visible) >= 30 {
		t.Fatalf("over limit: nothing dropped (%d visible)", len(plan.Visible))
	}
	evs := eventPayloads(t, ctx, st, "truncate")
	if len(evs) != 1 {
		t.Fatalf("want one truncate event, got %d", len(evs))
	}
	if evs[0]["marker"] != true || evs[0]["overflow"] != false || evs[0]["from"] != "m00002" {
		t.Fatalf("truncate payload = %v", evs[0])
	}

	// Truncation does not feed the trigger: the low-water follows the kernel rule
	// on the calibrated count, as it would without the window bound.
	if lw, _ := st.GetTrigger(ctx, "s1"); lw.LowWater != kernel.UpdateLowWater(3000, 9000) {
		t.Fatalf("low-water = %d, want %d", lw.LowWater, kernel.UpdateLowWater(3000, 9000))
	}
}

func TestPlanOverflowKeepsProtectedOnlyAndLogsIt(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t)

	// 12000 calibrated: each message counts 400, and the protected pinned message
	// plus the 10 recent messages alone take 4400 > 4000.
	plan, err := s.Plan(ctx, 12000)
	if err != nil {
		t.Fatal(err)
	}
	if hasMarker(plan.Visible) {
		t.Fatal("overflow must not add a marker")
	}
	if len(plan.Visible) != 11 {
		t.Fatalf("overflow: visible=%d, want the pinned message and the 10 recent ones", len(plan.Visible))
	}
	evs := eventPayloads(t, ctx, st, "truncate")
	if len(evs) != 1 || evs[0]["overflow"] != true {
		t.Fatalf("overflow event = %v", evs)
	}
}

func TestHandleCompressFramingCheckIsOptIn(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t)
	imperative := "Leia o arquivo. Corrija o erro de compilação. Rode os testes."

	strict := NewWithOptions(st, "s1", Options{FramingCheck: true})
	_, err := strict.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{StartRef: "m00002", EndRef: "m00010", Summary: imperative, Topic: "leitura"},
	}})
	if err == nil || !strings.Contains(err.Error(), "past tense") {
		t.Fatalf("framing check on: want rejection, got %v", err)
	}
	if n := countEvents(t, ctx, st, "compress"); n != 0 {
		t.Fatal("a rejected compress must write nothing")
	}

	// Off by default: the same summary is accepted.
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{StartRef: "m00002", EndRef: "m00010", Summary: imperative, Topic: "leitura"},
	}}); err != nil {
		t.Fatalf("framing check off: %v", err)
	}
}

func TestHandleCompressFramingCheckAcceptsPastTense(t *testing.T) {
	ctx := context.Background()
	_, st := ingested(t)
	strict := NewWithOptions(st, "s1", Options{FramingCheck: true})
	if _, err := strict.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{StartRef: "m00002", EndRef: "m00010", Summary: okSummary, Topic: "leitura"},
	}}); err != nil {
		t.Fatalf("past-tense summary must pass: %v", err)
	}
}

// scaledSum mirrors the session's calibrated share: ceil(tokens*count/raw).
func scaledSum(ms []kernel.Message, count, raw int) int {
	total := 0
	for _, m := range ms {
		total += (m.Tokens*count + raw - 1) / raw
	}
	return total
}

func hasMarker(ms []kernel.Message) bool {
	for _, m := range ms {
		if strings.HasPrefix(m.Text, `<history truncated="true"`) {
			return true
		}
	}
	return false
}

func eventPayloads(t *testing.T, ctx context.Context, st *store.Store, typ string) []map[string]any {
	t.Helper()
	rows, err := st.ListEvents(ctx, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, r := range rows {
		if r.Type != typ {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func countEvents(t *testing.T, ctx context.Context, st *store.Store, typ string) int {
	t.Helper()
	return len(eventPayloads(t, ctx, st, typ))
}
