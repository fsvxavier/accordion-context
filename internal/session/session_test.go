package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// okSummary is a valid summary: at least MinSummaryChars and shorter than the
// ranges these tests fold.
const okSummary = "o agente leu o arquivo e corrigiu o erro de compilação"

// transcript returns 30 messages (seq 1..30). Index 0 is the first user message,
// seq 15/16 is a tool_call/tool_result pair, each message is 1000 tokens, so a
// range of five or more messages reaches MinCompressRange (5000).
func transcript() []kernel.Message {
	out := make([]kernel.Message, 0, 30)
	for i := range 30 {
		m := kernel.Message{
			Role: kernel.RoleUser, Kind: kernel.KindText, Tokens: 1000,
			Text: fmt.Sprintf("message %d %s", i+1, strings.Repeat("x", 380)),
		}
		if i%2 == 1 {
			m.Role = kernel.RoleAssistant
		}
		switch i {
		case 14:
			m.Role, m.Kind, m.ToolName, m.ToolCallID = kernel.RoleAssistant, kernel.KindToolCall, "Read", "c1"
		case 15:
			m.Role, m.Kind, m.ToolName, m.ToolCallID = kernel.RoleTool, kernel.KindToolResult, "Read", "c1"
		}
		out = append(out, m)
	}
	return out
}

func newSession(t *testing.T) (*Session, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.EnsureProject(ctx, "p1", "/tmp/p1", "local", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, store.Session{
		ID: "s1", ProjectID: "p1", Model: "test", ContextLimit: 4000, Mode: "library", Integration: "library",
	}); err != nil {
		t.Fatal(err)
	}
	return New(st, "s1"), st
}

func ingested(t *testing.T) (*Session, *store.Store) {
	t.Helper()
	s, st := newSession(t)
	n, err := s.Ingest(context.Background(), transcript())
	if err != nil {
		t.Fatal(err)
	}
	if n != 30 {
		t.Fatalf("appended %d, want 30", n)
	}
	return s, st
}

func TestIngestDedupAndDivergence(t *testing.T) {
	ctx := context.Background()
	s, st := newSession(t)
	msgs := transcript()

	if n, err := s.Ingest(ctx, msgs[:3]); err != nil || n != 3 {
		t.Fatalf("first ingest: n=%d err=%v", n, err)
	}
	if n, err := s.Ingest(ctx, msgs[:5]); err != nil || n != 2 {
		t.Fatalf("overlapping ingest: n=%d err=%v, want 2 new", n, err)
	}
	if n, err := s.Ingest(ctx, msgs[:5]); err != nil || n != 0 {
		t.Fatalf("idempotent ingest: n=%d err=%v, want 0", n, err)
	}
	snap, err := st.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 5 || snap.Messages[4].Ref != "m00005" {
		t.Fatalf("ledger len=%d, want 5 with m00005 last", len(snap.Messages))
	}

	bad := append([]kernel.Message(nil), msgs[:5]...)
	bad[2].Text = "edited"
	if _, err := s.Ingest(ctx, bad); err == nil {
		t.Fatal("diverging transcript was accepted")
	}
}

func TestPlanCompressRenderRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t)

	plan, err := s.Plan(ctx, 3000)
	if err != nil {
		t.Fatal(err)
	}
	// 30 messages; no intents block before the first fold (§11.3).
	if len(plan.Visible) != 30 {
		t.Fatalf("pre-compress visible=%d, want 30", len(plan.Visible))
	}
	if lw, _ := st.GetTrigger(ctx, "s1"); lw.LowWater != 3000 {
		t.Fatalf("low-water=%d, want 3000", lw.LowWater)
	}

	res, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{StartRef: "m00002", EndRef: "m00010", Summary: okSummary, Topic: "leitura"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.BlockIDs) != 1 || res.BlockIDs[0] != "b1" {
		t.Fatalf("block ids=%v, want [b1]", res.BlockIDs)
	}
	if lw, _ := st.GetTrigger(ctx, "s1"); lw.LowWater != res.TokensAfter {
		t.Fatalf("low-water=%d after fold, want TokensAfter=%d", lw.LowWater, res.TokensAfter)
	}

	plan, err = s.Plan(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Visible) != 30-9+1+1 {
		t.Fatalf("visible=%d, want %d", len(plan.Visible), 30-9+1+1)
	}
	var hist, raw int
	for _, m := range plan.Visible {
		if strings.Contains(m.Text, `<history block="b1"`) {
			hist++
		}
		if m.Ref == "m00005" {
			raw++
		}
	}
	if hist != 1 || raw != 0 {
		t.Fatalf("history envelopes=%d raw folded=%d, want 1 and 0", hist, raw)
	}
	if plan.Visible[0].Ref != "m00001" {
		t.Fatalf("first visible=%q, want pinned m00001", plan.Visible[0].Ref)
	}
	if last := plan.Visible[len(plan.Visible)-1]; last.Ref != "m00030" {
		t.Fatalf("last visible=%q, want m00030 (natural order, last user at m00029)", last.Ref)
	}

	orig, err := s.Recall(ctx, "m00005")
	if err != nil || !strings.HasPrefix(orig, "message 5 ") {
		t.Fatalf("recall m00005: err=%v prefix=%q", err, orig[:min(12, len(orig))])
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("invariants after compress: %v", err)
	}
}

func TestHandleCompressRejections(t *testing.T) {
	long := strings.Repeat("y", 40000) // 10000 tokens, more than the 9000 of m2..m10
	cases := []struct {
		name string
		args CompressArgs
	}{
		{"unknown start", CompressArgs{Ranges: []CompressRange{{"m00099", "m00010", okSummary, ""}}}},
		{"unknown end", CompressArgs{Ranges: []CompressRange{{"m00002", "m00099", okSummary, ""}}}},
		{"inverted", CompressArgs{Ranges: []CompressRange{{"m00010", "m00005", okSummary, ""}}}},
		{"empty summary", CompressArgs{Ranges: []CompressRange{{"m00002", "m00010", "  ", ""}}}},
		{"short summary", CompressArgs{Ranges: []CompressRange{{"m00002", "m00010", "curto", ""}}}},
		{"short range", CompressArgs{Ranges: []CompressRange{{"m00002", "m00004", okSummary, ""}}}},
		{"covers first user", CompressArgs{Ranges: []CompressRange{{"m00001", "m00005", okSummary, ""}}}},
		{"recent zone", CompressArgs{Ranges: []CompressRange{{"m00015", "m00025", okSummary, ""}}}},
		{"ends on tool_call", CompressArgs{Ranges: []CompressRange{{"m00012", "m00015", okSummary, ""}}}},
		{"starts on tool_result", CompressArgs{Ranges: []CompressRange{{"m00016", "m00020", okSummary, ""}}}},
		{"summary not smaller", CompressArgs{Ranges: []CompressRange{{"m00002", "m00010", long, ""}}}},
		{"overlap inside call", CompressArgs{Ranges: []CompressRange{
			{"m00002", "m00010", okSummary, ""}, {"m00010", "m00014", okSummary, ""}}}},
		{"unknown close intent", CompressArgs{
			Ranges: []CompressRange{{"m00002", "m00010", okSummary, ""}}, CloseIntents: []int64{999}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, st := ingested(t)
			if _, err := s.HandleCompress(context.Background(), c.args); err == nil {
				t.Fatal("expected rejection")
			}
			snap, _ := st.Snapshot(context.Background(), "s1")
			if len(snap.Blocks) != 0 {
				t.Fatalf("rejected call wrote %d blocks", len(snap.Blocks))
			}
		})
	}
}

func TestHandleCompressSplitNonAdjacentPair(t *testing.T) {
	ctx := context.Background()
	s, _ := newSession(t)
	msgs := transcript()
	msgs[4].Role, msgs[4].Kind, msgs[4].ToolName, msgs[4].ToolCallID = kernel.RoleAssistant, kernel.KindToolCall, "Grep", "far"
	msgs[8].Role, msgs[8].Kind, msgs[8].ToolName, msgs[8].ToolCallID = kernel.RoleTool, kernel.KindToolResult, "Grep", "far"
	if _, err := s.Ingest(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	_, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{StartRef: "m00002", EndRef: "m00006", Summary: okSummary, Topic: ""}}})
	if err == nil {
		t.Fatal("range splitting non-adjacent tool pair accepted")
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{StartRef: "m00002", EndRef: "m00009", Summary: okSummary, Topic: ""}}}); err != nil {
		t.Fatalf("range containing whole pair rejected: %v", err)
	}
}

func TestHandleCompressOverlapWithActiveBlock(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t)
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{"m00002", "m00010", okSummary, "a"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{"m00005", "m00008", okSummary, "b"}}}); err == nil {
		t.Fatal("range inside active block accepted")
	}
	res, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{"m00011", "m00019", okSummary, "c"}}})
	if err != nil {
		t.Fatalf("adjacent range rejected: %v", err)
	}
	if res.BlockIDs[0] != "b2" {
		t.Fatalf("block id=%v, want b2", res.BlockIDs)
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("invariants: %v", err)
	}
}
