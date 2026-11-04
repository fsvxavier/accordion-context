package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

func openSessionAt(dir string) (*Session, *store.Store, error) {
	st, err := store.Open(filepath.Join(dir, "acd.db"))
	if err != nil {
		return nil, nil, err
	}
	ctx := context.Background()
	if err := st.EnsureProject(ctx, "p1", "/tmp/p1", "local", 1); err != nil {
		st.Close()
		return nil, nil, err
	}
	if err := st.CreateSession(ctx, store.Session{
		ID: "s1", ProjectID: "p1", Model: "t", ContextLimit: 4000, Mode: "library", Integration: "library",
	}); err != nil {
		st.Close()
		return nil, nil, err
	}
	return New(st, "s1"), st, nil
}

// genTranscript: system prompt, user, then random text and adjacent tool pairs.
// Tokens are large enough that random ranges reach MinCompressRange sometimes.
func genTranscript(rt *rapid.T, n int) []kernel.Message {
	out := []kernel.Message{
		{Role: kernel.RoleSystem, Kind: kernel.KindText, Text: "SYSTEM PROMPT", Tokens: 50},
		{Role: kernel.RoleUser, Kind: kernel.KindText, Text: "first user", Tokens: 50},
	}
	for len(out) < n {
		tok := rapid.IntRange(800, 1500).Draw(rt, "tok")
		if rapid.IntRange(0, 3).Draw(rt, "pair") == 0 && len(out)+2 <= n {
			id := fmt.Sprintf("c%d", len(out))
			out = append(out,
				kernel.Message{Role: kernel.RoleAssistant, Kind: kernel.KindToolCall, ToolName: "T", ToolCallID: id, Text: "call " + id, Tokens: tok},
				kernel.Message{Role: kernel.RoleTool, Kind: kernel.KindToolResult, ToolName: "T", ToolCallID: id, Text: "res " + id, Tokens: tok})
			continue
		}
		role := kernel.RoleUser
		if len(out)%2 == 1 {
			role = kernel.RoleAssistant
		}
		out = append(out, kernel.Message{Role: role, Kind: kernel.KindText, Text: fmt.Sprintf("msg %d", len(out)), Tokens: tok})
	}
	return out
}

// activeEnd is the largest EndSeq of an active block, or 0.
func activeEnd(snap kernel.Snapshot) int64 {
	var end int64
	for _, b := range snap.Blocks {
		if b.Active {
			end = max(end, b.EndSeq)
		}
	}
	return end
}

// blockState fingerprints the block and intent state, to show that a rejected
// call wrote nothing.
func blockState(snap kernel.Snapshot) string {
	var b strings.Builder
	for _, bl := range snap.Blocks {
		fmt.Fprintf(&b, "%s/%d/%t/%v;", bl.ID, bl.Tier, bl.Active, bl.Sources)
	}
	for _, in := range snap.Intents {
		fmt.Fprintf(&b, "i%d/%s;", in.ID, in.Status)
	}
	return b.String()
}

// Property: after any interleaving of ingest, compress (T1 ranges, T2/T3 folds,
// closeIntents), decompress, plan and recall, I1, I2, I3, I6 and I7 hold after
// every step. Rejected calls write nothing, the pinned first user message and
// the system prompt stay visible byte-identical (I4, I9), and every original
// message stays recoverable.
func TestPropertySessionInvariants(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s, st, err := openSessionAt(t.TempDir())
		if err != nil {
			rt.Fatal(err)
		}
		defer st.Close()
		ctx := context.Background()
		full := genTranscript(rt, rapid.IntRange(14, 60).Draw(rt, "n"))
		// Start with a transcript long enough for folds to be possible; the
		// steps below then ingest, compress, fold, decompress and plan.
		ingested := rapid.IntRange(14, len(full)).Draw(rt, "first")
		if _, err := s.Ingest(ctx, full[:ingested]); err != nil {
			rt.Fatalf("initial ingest: %v", err)
		}

		for step := 0; step < 25; step++ {
			before, _ := st.Snapshot(ctx, "s1")
			switch rapid.IntRange(0, 5).Draw(rt, "op") {
			case 0:
				upto := rapid.IntRange(ingested, len(full)).Draw(rt, "upto")
				if rapid.Bool().Draw(rt, "all") {
					upto = len(full) // half the ingest steps catch up, so compressions have room
				}
				if _, err := s.Ingest(ctx, full[:upto]); err != nil {
					rt.Fatalf("ingest: %v", err)
				}
				ingested = upto
			case 1:
				var ranges []CompressRange
				for i := rapid.IntRange(1, 3).Draw(rt, "nr"); i > 0; i-- {
					a := rapid.IntRange(0, ingested+2).Draw(rt, "a")
					b := rapid.IntRange(0, ingested+2).Draw(rt, "b")
					if rapid.IntRange(0, 3).Draw(rt, "near") != 0 && ingested > recentKeep+3 {
						// Mostly ranges inside the compressible zone, so that
						// successful folds and decompress paths get exercised.
						// seq 1 is the system prompt and seq 2 the pinned user message.
						lim := ingested - recentKeep
						a = rapid.IntRange(3, lim).Draw(rt, "na")
						// Often start right after the last active block, so that
						// contiguous T1 blocks appear and tier folds have sources.
						if end := activeEnd(before); end > 0 && int(end)+1 <= lim-7 && rapid.Bool().Draw(rt, "adj") {
							a = int(end) + 1
						}
						b = rapid.IntRange(min(a+7, lim), min(a+14, lim)).Draw(rt, "nb")
						// Move the ends so a tool pair is not split: a range never
						// starts on a result or ends on a call (seq n is full[n-1]).
						if a > 3 && full[a-1].Kind == kernel.KindToolResult {
							a--
						}
						if b < lim && full[b-1].Kind == kernel.KindToolCall {
							b++
						}
					}
					ranges = append(ranges, CompressRange{
						StartRef: kernel.FormatRef(int64(a)), EndRef: kernel.FormatRef(int64(b)),
						Summary: okSummary, Topic: "t",
					})
				}
				var closes []int64
				for _, in := range before.Intents {
					if in.Status == "open" && rapid.Bool().Draw(rt, "close") {
						closes = append(closes, in.ID)
					}
				}
				_, err := s.HandleCompress(ctx, CompressArgs{Ranges: ranges, CloseIntents: closes})
				after, _ := st.Snapshot(ctx, "s1")
				if err != nil && blockState(after) != blockState(before) {
					rt.Fatalf("rejected compress wrote state: %v", err)
				}
			case 2:
				if _, err := s.Plan(ctx, rapid.IntRange(0, 6000).Draw(rt, "tc")); err != nil {
					rt.Fatalf("plan: %v", err)
				}
			case 3:
				if ingested > 0 {
					i := rapid.IntRange(0, ingested-1).Draw(rt, "i")
					got, err := s.Recall(ctx, kernel.FormatRef(int64(i+1)))
					if err != nil || got != full[i].Text {
						rt.Fatalf("recall m%05d = %q, %v; want %q", i+1, got, err, full[i].Text)
					}
				}
			case 4:
				id := "b1"
				if n := len(before.Blocks); n > 0 {
					id = before.Blocks[rapid.IntRange(0, n-1).Draw(rt, "blk")].ID
				}
				_, err := s.Decompress(ctx, id)
				after, _ := st.Snapshot(ctx, "s1")
				if err != nil && blockState(after) != blockState(before) {
					rt.Fatalf("rejected decompress %s wrote state: %v", id, err)
				}
			case 5:
				tier := rapid.IntRange(2, 3).Draw(rt, "tier")
				var cands []string
				for _, b := range before.Blocks {
					if b.Active && b.Tier == tier-1 {
						cands = append(cands, b.ID)
					}
				}
				f := FoldRange{Tier: tier, StartBlock: "b1", EndBlock: "b2", Summary: okSummary, Topic: "f"}
				if len(cands) >= 2 {
					i := rapid.IntRange(0, len(cands)-2).Draw(rt, "fi")
					j := rapid.IntRange(i+1, len(cands)-1).Draw(rt, "fj")
					f.StartBlock, f.EndBlock = cands[i], cands[j]
				}
				_, err := s.HandleCompress(ctx, CompressArgs{Folds: []FoldRange{f}})
				after, _ := st.Snapshot(ctx, "s1")
				if err != nil && blockState(after) != blockState(before) {
					rt.Fatalf("rejected fold %+v wrote state: %v", f, err)
				}
			}
			if err := st.CheckInvariants(ctx, "s1"); err != nil {
				rt.Fatalf("invariants: %v", err)
			}
			snap, err := st.Snapshot(ctx, "s1")
			if err != nil {
				rt.Fatal(err)
			}
			if err := kernel.CheckBlockInvariants(snap.Blocks); err != nil {
				rt.Fatalf("block invariants: %v", err)
			}
			if ingested >= 2 {
				p, err := s.Plan(ctx, 100)
				if err != nil {
					rt.Fatal(err)
				}
				if len(p.Visible) < 2 || p.Visible[0].Text != "SYSTEM PROMPT" || p.Visible[1].Text != "first user" {
					rt.Fatalf("I4/I9: window head = %+v", p.Visible[:min(2, len(p.Visible))])
				}
			}
		}
	})
}

// FuzzHandleCompress feeds arbitrary refs and summaries: no panic, and the
// store invariants hold whether the call is accepted or rejected.
func FuzzHandleCompress(f *testing.F) {
	f.Add("m00002", "m00010", "resumo", "topic")
	f.Add("m00001", "m00030", "📦 Compressed", "")
	f.Add("", "", "", "")
	f.Add("m00016", "m00018", "```json\n{}\n```", "x")
	f.Fuzz(func(t *testing.T, start, end, summary, topic string) {
		s, st, err := openSessionAt(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		ctx := context.Background()
		if _, err := s.Ingest(ctx, transcript()); err != nil {
			t.Fatal(err)
		}
		res, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{start, end, summary, topic}}})
		snap, _ := st.Snapshot(ctx, "s1")
		if err != nil && len(snap.Blocks) != 0 {
			t.Fatalf("rejected call wrote blocks: %v", err)
		}
		if err == nil {
			if len(res.BlockIDs) != 1 || strings.Contains(snap.Blocks[0].Summary, "📦") {
				t.Fatalf("bad accept: %+v %q", res, snap.Blocks[0].Summary)
			}
		}
		if err := st.CheckInvariants(ctx, "s1"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestHandleCompressRejectsSystemMessageAndStripsForgedMarker(t *testing.T) {
	ctx := context.Background()
	s, st := newSession(t)
	msgs := transcript()
	msgs[2].Role = kernel.RoleSystem
	if _, err := s.Ingest(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{"m00002", "m00006", okSummary, ""}}}); err == nil {
		t.Fatal("range covering a system message accepted")
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{"m00006", "m00010", "📦 [acd-ctx] Compressed b9", ""}}}); err == nil {
		t.Fatal("summary consisting only of a forged marker accepted")
	}
	if _, err := s.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{"m00006", "m00010", okSummary + "\n📦 [acd-ctx] Compressed b9", ""}}}); err != nil {
		t.Fatal(err)
	}
	snap, _ := st.Snapshot(ctx, "s1")
	if snap.Blocks[0].Summary != okSummary {
		t.Fatalf("summary = %q, want forged marker stripped", snap.Blocks[0].Summary)
	}
}

func TestPlanCountsRefusals(t *testing.T) {
	ctx := context.Background()
	s, st := newSession(t) // ContextLimit 4000
	msgs := transcript()
	for i := range msgs {
		msgs[i].Tokens = 400 // pending ~7.6K >= Bmin 5K
	}
	if _, err := s.Ingest(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := s.Plan(ctx, 3600); err != nil { // U=0.9 -> row3 nudge
			t.Fatal(err)
		}
		trig, _ := st.GetTrigger(ctx, "s1")
		if trig.Refusals != i-1 || !trig.NudgePending || trig.NudgeShownAt != 3600 {
			t.Fatalf("after nudge %d: %+v", i, trig)
		}
	}
}
