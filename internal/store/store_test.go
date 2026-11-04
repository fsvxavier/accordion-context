package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"pgregory.net/rapid"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

var memCounter atomic.Int64

// testHelper is the subset of testing.TB also provided by *rapid.T.
type testHelper interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// newMemStore opens an isolated shared-cache in-memory database.
func newMemStore(t testHelper) *Store {
	t.Helper()
	dsn := fmt.Sprintf("file:store-test-%d?mode=memory&cache=shared", memCounter.Add(1))
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func setupSession(t testHelper, s *Store, sid string) {
	t.Helper()
	ctx := context.Background()
	if err := s.EnsureProject(ctx, "p1", "/work/p1", "global", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, Session{
		ID: sid, ProjectID: "p1", Model: "m", ContextLimit: 200000,
		Mode: "proxy", Integration: "url",
	}); err != nil {
		t.Fatal(err)
	}
}

func appendN(t testHelper, s *Store, sid string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		role := kernel.RoleUser
		if i%2 == 1 {
			role = kernel.RoleAssistant
		}
		if _, err := s.AppendMessage(context.Background(), sid, NewMessage{
			Role: role, Kind: kernel.KindText,
			Text: fmt.Sprintf("message %d", i+1), Tokens: 10,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenMigratesOnceAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acd.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
	setupSession(t, s, "s1")
	appendN(t, s, "s1", 2)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	snap, err := s2.Snapshot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("messages after reopen = %d, want 2", len(snap.Messages))
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acd.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Open with v99 err = %v, want 'newer' error", err)
	}
}

func TestAppendMessageSeqRefAndContentDedup(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()

	m1, err := s.AppendMessage(ctx, "s1", NewMessage{Role: kernel.RoleUser, Kind: kernel.KindText, Text: "same", Tokens: 3})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := s.AppendMessage(ctx, "s1", NewMessage{Role: kernel.RoleAssistant, Kind: kernel.KindText, Text: "same", Tokens: 3})
	if err != nil {
		t.Fatal(err)
	}
	if m1.Seq != 1 || m1.Ref != "m00001" || m2.Seq != 2 || m2.Ref != "m00002" {
		t.Fatalf("seq/ref = (%d,%s),(%d,%s)", m1.Seq, m1.Ref, m2.Seq, m2.Ref)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM contents`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("contents rows = %d, want 1 (dedup by sha256)", rows)
	}
}

func TestSnapshotRoundTripsMessagesAndTrigger(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	if _, err := s.AppendMessage(ctx, "s1", NewMessage{
		Role: kernel.RoleTool, Kind: kernel.KindToolResult,
		ToolName: "bash", ToolCallID: "call_1", Text: "ok ✓", Tokens: 4,
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.ContextLimit != 200000 || len(snap.Messages) != 1 {
		t.Fatalf("snapshot = limit %d, %d msgs", snap.ContextLimit, len(snap.Messages))
	}
	m := snap.Messages[0]
	if m.Text != "ok ✓" || m.ToolName != "bash" || m.ToolCallID != "call_1" || m.Ref != "m00001" {
		t.Fatalf("message round trip = %+v", m)
	}
}

func TestTriggerGetSet(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	want := kernel.TriggerState{LowWater: 120, NudgePending: true, NudgeShownAt: 150, LastShownT2: 1, LastShownT3: 2, Refusals: 3}
	if err := s.SetTrigger(ctx, "s1", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTrigger(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("trigger = %+v, want %+v", got, want)
	}
	if err := s.SetTrigger(ctx, "missing", want); err == nil {
		t.Fatal("SetTrigger on unknown session: want error")
	}
}

func TestIntentLifecycle(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	id, err := s.AddIntent(ctx, "s1", 1, "fix the parser")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CloseIntent(ctx, "s1", id, "bogus", 5); err == nil {
		t.Fatal("invalid status accepted")
	}
	if err := s.CloseIntent(ctx, "s1", id, "done", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseIntent(ctx, "s1", id, "superseded", 6); err == nil {
		t.Fatal("closing an already closed intent accepted")
	}
	snap, err := s.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Intents) != 1 || snap.Intents[0].Status != "done" || snap.Intents[0].Text != "fix the parser" {
		t.Fatalf("intents = %+v", snap.Intents)
	}
}

func TestRecordCacheAndEventsAreAppendOnly(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.RecordCache(ctx, "s1", CacheCall{InputTokens: 100, CachedTokens: 80, EstTokens: 98}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent(ctx, "s1", "turn", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	var maxCall, maxEvent int
	if err := s.db.QueryRow(`SELECT MAX(call_seq) FROM cache_ledger WHERE session_id='s1'`).Scan(&maxCall); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT MAX(event_seq) FROM events WHERE session_id='s1'`).Scan(&maxEvent); err != nil {
		t.Fatal(err)
	}
	if maxCall != 3 || maxEvent != 3 {
		t.Fatalf("max call_seq=%d event_seq=%d, want 3 and 3", maxCall, maxEvent)
	}
}

func TestApplyCompressDeactivatesWithoutDeleting(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	appendN(t, s, "s1", 10)

	if err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks: []NewBlock{{ID: "b1", Tier: 1, Topic: "first", Summary: "sum", StartSeq: 1, EndSeq: 5, SrcTokens: 50, SumTokens: 5}},
		Now:       2, TokensAfter: 60,
	}); err != nil {
		t.Fatalf("ApplyCompress: %v", err)
	}
	if err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks:   []NewBlock{{ID: "b2", Tier: 2, Summary: "tier2", StartSeq: 1, EndSeq: 5, SrcTokens: 5, SumTokens: 1, Sources: []string{"b1"}}},
		Deactivate:  []string{"b1"},
		ParentOf:    map[string]string{"b1": "b2"},
		Now:         3,
		TokensAfter: 40,
	}); err != nil {
		t.Fatalf("second ApplyCompress: %v", err)
	}

	snap, err := s.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 10 {
		t.Fatalf("messages = %d, want 10 (nothing deleted)", len(snap.Messages))
	}
	active := map[string]bool{}
	for _, b := range snap.Blocks {
		active[b.ID] = b.Active
	}
	if active["b1"] || !active["b2"] {
		t.Fatalf("active = %v, want b1 inactive, b2 active", active)
	}
	if snap.Trigger.LowWater != 40 {
		t.Fatalf("low water = %d, want 40", snap.Trigger.LowWater)
	}
	if got := snap.Blocks[0]; got.ID != "b1" || len(got.Sources) != 0 {
		t.Fatalf("b1 = %+v", got)
	}
	var sources int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM block_sources WHERE block_id='b2' AND source_id='b1'`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 1 {
		t.Fatalf("block_sources rows = %d, want 1", sources)
	}
	if err := s.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("CheckInvariants after valid folds: %v", err)
	}
}

func TestApplyCompressRollsBackOnInvariantViolation(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	appendN(t, s, "s1", 10)
	if err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks: []NewBlock{{ID: "b1", Tier: 1, Summary: "x", StartSeq: 1, EndSeq: 6, SrcTokens: 60, SumTokens: 5}},
		Now:       1, TokensAfter: 5,
	}); err != nil {
		t.Fatal(err)
	}

	// Overlaps b1 while b1 is still active: I1 and I2 must trip and roll back.
	err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks: []NewBlock{{ID: "b2", Tier: 1, Summary: "y", StartSeq: 4, EndSeq: 8, SrcTokens: 50, SumTokens: 5}},
		Now:       2, TokensAfter: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "invariant") {
		t.Fatalf("overlapping plan err = %v, want invariant error", err)
	}
	snap, err := s.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Blocks) != 1 || snap.Blocks[0].ID != "b1" {
		t.Fatalf("rollback failed, blocks = %+v", snap.Blocks)
	}
	var events int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE session_id='s1' AND type='compress'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("compress events = %d, want 1 (rolled-back event must not persist)", events)
	}
}

func TestApplyCompressRejectsEmptyPlanAndBadSummary(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	appendN(t, s, "s1", 3)

	if err := s.ApplyCompress(ctx, "s1", CompressPlan{}); err == nil {
		t.Fatal("empty plan accepted")
	}
	// CHECK sum_tokens < src_tokens from schema must reject a non-shrinking fold.
	err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks: []NewBlock{{ID: "b1", Tier: 1, Summary: "big", StartSeq: 1, EndSeq: 2, SrcTokens: 10, SumTokens: 10}},
		Now:       1,
	})
	if err == nil {
		t.Fatal("non-shrinking block accepted")
	}
}

func TestMissingSessionErrors(t *testing.T) {
	s := newMemStore(t)
	if _, err := s.Snapshot(context.Background(), "nope"); err == nil {
		t.Fatal("Snapshot of unknown session: want error")
	}
}

// Property: any partition of the ledger into consecutive T1 folds keeps every
// message covered by exactly one active block, keeps all messages, and passes
// CheckInvariants.
func TestPropertyConsecutiveFoldsKeepInvariants(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := newMemStore(rt)
		setupSession(rt, s, "s1")
		ctx := context.Background()

		n := rapid.IntRange(2, 40).Draw(rt, "n")
		appendN(rt, s, "s1", n)

		start := 1
		id := 0
		for start <= n {
			size := rapid.IntRange(1, n-start+1).Draw(rt, "size")
			end := start + size - 1
			id++
			if err := s.ApplyCompress(ctx, "s1", CompressPlan{
				NewBlocks: []NewBlock{{
					ID: fmt.Sprintf("b%d", id), Tier: 1, Summary: "s",
					StartSeq: int64(start), EndSeq: int64(end), SrcTokens: 10 * size, SumTokens: 1,
				}},
				Now: int64(id), TokensAfter: 1,
			}); err != nil {
				rt.Fatalf("fold %d [%d,%d]: %v", id, start, end, err)
			}
			start = end + 1
		}

		if err := s.CheckInvariants(ctx, "s1"); err != nil {
			rt.Fatalf("invariants: %v", err)
		}
		snap, err := s.Snapshot(ctx, "s1")
		if err != nil {
			rt.Fatal(err)
		}
		if len(snap.Messages) != n {
			rt.Fatalf("messages = %d, want %d", len(snap.Messages), n)
		}
		covered := make([]int, n+1)
		for _, b := range snap.Blocks {
			if !b.Active {
				continue
			}
			for seq := b.StartSeq; seq <= b.EndSeq; seq++ {
				covered[seq]++
			}
		}
		for seq := 1; seq <= n; seq++ {
			if covered[seq] != 1 {
				rt.Fatalf("seq %d covered by %d active blocks, want 1", seq, covered[seq])
			}
		}
	})
}

// I7 accepts an inactive block with no parent (a decompressed T1 whose messages
// are raw again), and rejects a message hidden under a parent with no active
// covering block.
func TestI7DecompressedRawBlockIsNotViolation(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	appendN(t, s, "s1", 10)
	if err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks: []NewBlock{{ID: "b1", Tier: 1, Summary: "sum", StartSeq: 1, EndSeq: 5, SrcTokens: 50, SumTokens: 5}},
		Now:       1, TokensAfter: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE blocks SET active = 0 WHERE block_id = 'b1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("raw decompressed block flagged: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE blocks SET parent_block = 'b9' WHERE block_id = 'b1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckInvariants(ctx, "s1"); err == nil || !strings.Contains(err.Error(), "I7") {
		t.Fatalf("hidden message with no active cover: err = %v, want I7", err)
	}
}

// ApplyDecompress restores the folded sources in one transaction and rejects a
// plan whose target or sources no longer match the stored state.
func TestApplyDecompressRestoresSourcesAndRejectsStalePlan(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	appendN(t, s, "s1", 10)
	if err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks: []NewBlock{{ID: "b1", Tier: 1, Summary: "sum", StartSeq: 1, EndSeq: 5, SrcTokens: 50, SumTokens: 5}},
		Now:       1, TokensAfter: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyCompress(ctx, "s1", CompressPlan{
		NewBlocks:  []NewBlock{{ID: "b2", Tier: 2, Summary: "t2", StartSeq: 1, EndSeq: 5, SrcTokens: 5, SumTokens: 1, Sources: []string{"b1"}}},
		Deactivate: []string{"b1"}, ParentOf: map[string]string{"b1": "b2"},
		Now: 2, TokensAfter: 5,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ApplyDecompress(ctx, "s1", kernel.DecompressPlan{Target: "b2", Reactivate: []string{"b9"}}); err == nil {
		t.Fatal("plan with unknown source accepted")
	}
	if err := s.ApplyDecompress(ctx, "s1", kernel.DecompressPlan{Target: "b1"}); err == nil {
		t.Fatal("plan targeting an inactive block accepted")
	}
	snap, _ := s.Snapshot(ctx, "s1")
	active := map[string]bool{}
	for _, b := range snap.Blocks {
		active[b.ID] = b.Active
	}
	if active["b1"] || !active["b2"] {
		t.Fatalf("rejected plans changed state: %v", active)
	}

	if err := s.ApplyDecompress(ctx, "s1", kernel.DecompressPlan{
		Requested: "b2", Target: "b2", Reactivate: []string{"b1"},
	}); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Snapshot(ctx, "s1")
	active = map[string]bool{}
	for _, b := range snap.Blocks {
		active[b.ID] = b.Active
	}
	if !active["b1"] || active["b2"] || len(snap.Messages) != 10 {
		t.Fatalf("after decompress active=%v messages=%d", active, len(snap.Messages))
	}
	if err := s.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("invariants after decompress: %v", err)
	}
}
