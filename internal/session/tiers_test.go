package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

func snapOf(t *testing.T, st *store.Store) kernel.Snapshot {
	t.Helper()
	snap, err := st.Snapshot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func blocksOf(snap kernel.Snapshot) map[string]kernel.Block {
	m := make(map[string]kernel.Block, len(snap.Blocks))
	for _, b := range snap.Blocks {
		m[b.ID] = b
	}
	return m
}

// twoBlocks ingests the transcript and folds m2..m10 into b1 and m11..m19 into b2.
func twoBlocks(t *testing.T) (*Session, *store.Store) {
	t.Helper()
	s, st := ingested(t)
	if _, err := s.HandleCompress(context.Background(), CompressArgs{Ranges: []CompressRange{
		{"m00002", "m00010", okSummary, "a"},
		{"m00011", "m00019", okSummary, "b"},
	}}); err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestTierFoldAndDecompressRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, st := twoBlocks(t)

	if _, err := s.HandleCompress(ctx, CompressArgs{Folds: []FoldRange{
		{Tier: 2, StartBlock: "b1", EndBlock: "b2", Summary: okSummary, Topic: "fold"},
	}}); err != nil {
		t.Fatal(err)
	}
	blocks := blocksOf(snapOf(t, st))
	if !blocks["b3"].Active || blocks["b3"].Tier != 2 || len(blocks["b3"].Sources) != 2 {
		t.Fatalf("b3 = %+v, want active tier-2 with sources b1,b2", blocks["b3"])
	}
	if blocks["b1"].Active || blocks["b2"].Active {
		t.Fatal("fold left its sources active")
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("after fold: %v", err)
	}
	p, err := s.Plan(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasHistory(p.Visible, "b3") || hasHistory(p.Visible, "b1") {
		t.Fatal("window does not show only the tier-2 envelope")
	}

	// m00005 is folded under b1, which is folded under b3: decompress expands
	// one level, so b3 opens and b1 and b2 come back.
	res, err := s.Decompress(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != "b3" || !res.Redirected || len(res.Reactivated) != 2 {
		t.Fatalf("decompress b1 = %+v, want redirect to b3 expanding b1,b2", res)
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("after redirected decompress: %v", err)
	}

	// b1 is now a T1 block again. Expanding it leaves its messages raw in the
	// window: an inactive block with no parent, which I7 accepts.
	res, err = s.Decompress(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != "b1" || res.Redirected || len(res.Reactivated) != 0 {
		t.Fatalf("decompress raw b1 = %+v", res)
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("after raw T1 decompress: %v", err)
	}
	p, err = s.Plan(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(p.Visible, "m00005") || hasHistory(p.Visible, "b1") {
		t.Fatal("expanded T1 messages are not raw in the window")
	}

	// Nothing is left to expand: b1 has no active ancestor, and b3 has no parent.
	if _, err := s.Decompress(ctx, "b1"); !errors.Is(err, kernel.ErrNotReachable) {
		t.Fatalf("decompress raw b1 again: %v, want ErrNotReachable", err)
	}
	if _, err := s.Decompress(ctx, "b3"); !errors.Is(err, kernel.ErrNotReachable) {
		t.Fatalf("decompress unfolded b3: %v, want ErrNotReachable", err)
	}
	if _, err := s.Decompress(ctx, "b99"); !errors.Is(err, kernel.ErrNoSuchBlock) {
		t.Fatalf("decompress unknown: %v, want ErrNoSuchBlock", err)
	}
}

func TestFoldRejectionsWriteNothing(t *testing.T) {
	long := strings.Repeat("y", 40000)
	cases := []struct {
		name string
		f    FoldRange
	}{
		{"tier 4", FoldRange{Tier: 4, StartBlock: "b1", EndBlock: "b2", Summary: okSummary}},
		{"single source", FoldRange{Tier: 2, StartBlock: "b1", EndBlock: "b1", Summary: okSummary}},
		{"unknown block", FoldRange{Tier: 2, StartBlock: "b1", EndBlock: "b9", Summary: okSummary}},
		{"reversed", FoldRange{Tier: 2, StartBlock: "b2", EndBlock: "b1", Summary: okSummary}},
		{"no tier-2 blocks", FoldRange{Tier: 3, StartBlock: "b1", EndBlock: "b2", Summary: okSummary}},
		{"short summary", FoldRange{Tier: 2, StartBlock: "b1", EndBlock: "b2", Summary: "curto"}},
		{"summary not smaller", FoldRange{Tier: 2, StartBlock: "b1", EndBlock: "b2", Summary: long}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, st := twoBlocks(t)
			before := len(snapOf(t, st).Blocks)
			if _, err := s.HandleCompress(context.Background(), CompressArgs{Folds: []FoldRange{c.f}}); err == nil {
				t.Fatal("expected rejection")
			}
			if after := snapOf(t, st); len(after.Blocks) != before || !after.Blocks[0].Active {
				t.Fatalf("rejected fold wrote blocks: %d -> %d", before, len(after.Blocks))
			}
		})
	}
}

func TestIntentsOpenAtIngestAndCloseAtCompress(t *testing.T) {
	ctx := context.Background()
	s, st := ingested(t)

	userTexts := 0
	for _, m := range transcript() {
		if m.Role == kernel.RoleUser && m.Kind == kernel.KindText {
			userTexts++
		}
	}
	snap := snapOf(t, st)
	if len(snap.Intents) != userTexts {
		t.Fatalf("intents = %d, want one per user text message (%d)", len(snap.Intents), userTexts)
	}
	var id int64
	for _, in := range snap.Intents {
		if in.Status != "open" {
			t.Fatalf("intent %d status %q, want open", in.ID, in.Status)
		}
		if in.OriginSeq == 3 {
			id = in.ID
		}
	}
	if id == 0 {
		t.Fatal("no intent originates at m00003")
	}

	if _, err := s.HandleCompress(ctx, CompressArgs{
		Ranges:       []CompressRange{{"m00002", "m00010", okSummary, "a"}},
		CloseIntents: []int64{id},
	}); err != nil {
		t.Fatal(err)
	}
	for _, in := range snapOf(t, st).Intents {
		if in.ID == id && in.Status != "done" {
			t.Fatalf("intent %d status %q after compress, want done", id, in.Status)
		}
	}

	// Closing an intent that is no longer open is rejected, and nothing is written.
	if _, err := s.HandleCompress(ctx, CompressArgs{
		Ranges:       []CompressRange{{"m00011", "m00019", okSummary, "b"}},
		CloseIntents: []int64{id},
	}); err == nil {
		t.Fatal("closing an intent twice accepted")
	}
	if n := len(snapOf(t, st).Blocks); n != 1 {
		t.Fatalf("rejected close wrote blocks: have %d, want 1", n)
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
}

func TestPlanReturnsRereadHint(t *testing.T) {
	ctx := context.Background()
	s, _ := newSession(t)
	msgs := transcript()
	msgs[14].Text = `{"file_path":"a.go"}` // the tool_call at m00015
	if _, err := s.Ingest(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{"m00002", "m00016", okSummary, "leitura"},
	}}); err != nil {
		t.Fatal(err)
	}
	// The model reads a.go again after the fold, in a new turn.
	again := append(append([]kernel.Message(nil), msgs...),
		kernel.Message{Role: kernel.RoleUser, Kind: kernel.KindText, Text: "veja de novo", Tokens: 10},
		kernel.Message{Role: kernel.RoleAssistant, Kind: kernel.KindToolCall, ToolName: "Read",
			ToolCallID: "c9", Text: `{"file_path":"a.go"}`, Tokens: 10})
	if _, err := s.Ingest(ctx, again); err != nil {
		t.Fatal(err)
	}
	p, err := s.Plan(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range p.Hints {
		if h.Kind == kernel.RecallReread && h.BlockID == "b1" && h.Path == "a.go" {
			return
		}
	}
	t.Fatalf("hints = %+v, want a re-read hint for b1 and a.go", p.Hints)
}

func hasHistory(msgs []kernel.Message, block string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Text, `<history block="`+block+`"`) {
			return true
		}
	}
	return false
}

func hasRef(msgs []kernel.Message, ref string) bool {
	for _, m := range msgs {
		if m.Ref == ref {
			return true
		}
	}
	return false
}

// longTranscript returns n plain text messages of 1000 tokens each, alternating
// user and assistant, with no tool pairs.
func longTranscript(n int) []kernel.Message {
	out := make([]kernel.Message, 0, n)
	for i := range n {
		role := kernel.RoleUser
		if i%2 == 1 {
			role = kernel.RoleAssistant
		}
		out = append(out, kernel.Message{Role: role, Kind: kernel.KindText,
			Text: fmt.Sprintf("message %d", i+1), Tokens: 1000})
	}
	return out
}

// Four T1 blocks, two T2 folds and one T3 fold over them. The T3 covers 36000
// raw tokens, while its sources carry only 28 summary tokens, so the raw measure
// is what lets it pass MinCompressRange.
func TestTierThreeFoldDeterministic(t *testing.T) {
	ctx := context.Background()
	s, st := newSession(t)
	if _, err := s.Ingest(ctx, longTranscript(60)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{
		{"m00002", "m00010", okSummary, "a"},
		{"m00011", "m00019", okSummary, "b"},
		{"m00020", "m00028", okSummary, "c"},
		{"m00029", "m00037", okSummary, "d"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Folds: []FoldRange{
		{Tier: 2, StartBlock: "b1", EndBlock: "b2", Summary: okSummary, Topic: "t2a"},
		{Tier: 2, StartBlock: "b3", EndBlock: "b4", Summary: okSummary, Topic: "t2b"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Folds: []FoldRange{
		{Tier: 3, StartBlock: "b5", EndBlock: "b6", Summary: okSummary, Topic: "t3"},
	}}); err != nil {
		t.Fatal(err)
	}
	blocks := blocksOf(snapOf(t, st))
	if !blocks["b7"].Active || blocks["b7"].Tier != 3 || len(blocks["b7"].Sources) != 2 {
		t.Fatalf("b7 = %+v, want active tier-3 with sources b5,b6", blocks["b7"])
	}
	for _, id := range []string{"b1", "b2", "b3", "b4", "b5", "b6"} {
		if blocks[id].Active {
			t.Fatalf("%s still active after the T3 fold", id)
		}
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("after T3 fold: %v", err)
	}

	// Expanding the T3 restores its two T2 sources (their parent_block is b7).
	res, err := s.Decompress(ctx, "b7")
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != "b7" || res.Redirected || len(res.Reactivated) != 2 {
		t.Fatalf("decompress b7 = %+v", res)
	}
	blocks = blocksOf(snapOf(t, st))
	if !blocks["b5"].Active || !blocks["b6"].Active || blocks["b7"].Active {
		t.Fatalf("after decompress b7: b5=%v b6=%v b7=%v", blocks["b5"].Active, blocks["b6"].Active, blocks["b7"].Active)
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("after T3 decompress: %v", err)
	}

	// Then one level more: b5 restores b1 and b2.
	res, err = s.Decompress(ctx, "b5")
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != "b5" || len(res.Reactivated) != 2 {
		t.Fatalf("decompress b5 = %+v", res)
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("after T2 decompress: %v", err)
	}
}
