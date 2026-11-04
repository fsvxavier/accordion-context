package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// roomySession is ingested with the 30-message transcript in a window large
// enough that the usage bands stay quiet: only a forced nudge can fire.
func roomySession(t *testing.T, msgs []kernel.Message) (*Session, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.EnsureProject(ctx, "p1", "/work/p1", "local", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, store.Session{
		ID: "s1", ProjectID: "p1", Model: "test", ContextLimit: 200000, Mode: "library", Integration: "library",
	}); err != nil {
		t.Fatal(err)
	}
	s := New(st, "s1")
	if _, err := s.Ingest(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestCompactOffersSpansAndNudgesOnNextPlan(t *testing.T) {
	s, st := roomySession(t, transcript())
	ctx := context.Background()

	if _, err := s.Plan(ctx, 3000); err != nil {
		t.Fatal(err)
	}
	res, err := s.Compact(ctx, CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.NudgePending || len(res.Spans) != 1 {
		t.Fatalf("Compact = %+v, want one span and a pending nudge", res)
	}
	if sp := res.Spans[0]; sp.Start != "m00002" || sp.End != "m00020" || sp.Tokens < MinCompressRange {
		t.Fatalf("span = %+v, want m00002..m00020 with at least %d tokens", sp, MinCompressRange)
	}
	if trig, _ := st.GetTrigger(ctx, "s1"); !trig.NudgePending {
		t.Fatal("trigger NudgePending not set by Compact")
	}

	plan, err := s.Plan(ctx, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Decision.Nudge || plan.Decision.Tier != 1 || plan.Decision.Row != "forced" {
		t.Fatalf("decision after Compact = %+v, want the forced tier-1 nudge", plan.Decision)
	}
	if plan.After.Refusals != 0 {
		t.Fatalf("a forced nudge counted %d refusals, want 0", plan.After.Refusals)
	}
	if forced, _ := st.ForcedNudge(ctx, "s1"); forced {
		t.Fatal("forced flag survived the Plan that nudged")
	}
}

func TestCompactWithoutSpansArmsNothing(t *testing.T) {
	s, st := roomySession(t, transcript()[:5])
	ctx := context.Background()
	res, err := s.Compact(ctx, CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.NudgePending || len(res.Spans) != 0 || res.Spans == nil {
		t.Fatalf("Compact on a short session = %+v, want no spans, empty list and no nudge", res)
	}
	if trig, _ := st.GetTrigger(ctx, "s1"); trig.NudgePending {
		t.Fatal("nudge armed without spans")
	}
}

func TestCompactMinTokensFiltersSpans(t *testing.T) {
	s, _ := roomySession(t, transcript())
	res, err := s.Compact(context.Background(), CompactOptions{MinTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Spans) != 0 || res.NudgePending {
		t.Fatalf("Compact with a huge minimum = %+v, want nothing offered", res)
	}
}

// exportFixture is a session whose block b1 covers seq 2..4 of the transcript.
func exportFixture(t *testing.T, msgs []kernel.Message) (*Session, *store.Store) {
	t.Helper()
	s, st := roomySession(t, msgs)
	err := st.ApplyCompress(context.Background(), "s1", store.CompressPlan{
		NewBlocks: []store.NewBlock{{ID: "b1", Tier: 1, Topic: "t", Summary: "a summary of the exchange",
			StartSeq: 2, EndSeq: 4, SrcTokens: 3000, SumTokens: 10}},
		Now: 1, TokensAfter: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestExportBlockWritesOwnerOnlyFile(t *testing.T) {
	s, _ := exportFixture(t, transcript())
	dir := t.TempDir()
	res, err := s.ExportBlock(context.Background(), "b1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(res.Path) != dir || !strings.HasPrefix(filepath.Base(res.Path), "b1-") || !strings.HasSuffix(res.Path, ".txt") {
		t.Fatalf("path = %q, want b1-<hash>.txt directly in %s", res.Path, dir)
	}
	if res.Messages != 3 || res.Truncated {
		t.Fatalf("result = %+v, want 3 messages, not truncated", res)
	}
	info, err := os.Stat(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "== m00002 assistant text\nmessage 2 ") || !strings.Contains(string(raw), "== m00004 ") {
		t.Fatalf("export lacks the original messages: %.120q", raw)
	}
	if strings.Contains(string(raw), "== m00005 ") {
		t.Fatal("export holds a message outside the block")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %d entries", len(entries))
	}
}

func TestExportBlockRejectsBadIDsAndSymlinks(t *testing.T) {
	s, _ := exportFixture(t, transcript())
	ctx := context.Background()
	dir := t.TempDir()
	for _, id := range []string{"", "../b1", "b1/../../x", "B1", "b1.txt", "b1\x00", "b99"} {
		if _, err := s.ExportBlock(ctx, id, dir); err == nil {
			t.Errorf("ExportBlock(%q) succeeded, want an error", id)
		}
	}
	if _, err := s.ExportBlock(ctx, "b1", ""); err == nil {
		t.Error("ExportBlock with no directory succeeded")
	}

	// A link planted at the target name is replaced, never written through.
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("s1"))
	planted := filepath.Join(dir, "b1-"+hex.EncodeToString(sum[:6])+".txt")
	if err := os.Symlink(victim, planted); err != nil {
		t.Fatal(err)
	}
	res, err := s.ExportBlock(ctx, "b1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != planted {
		t.Fatalf("path = %q, want %q", res.Path, planted)
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Fatalf("the file behind the planted link was overwritten: %q", raw)
	}
	if info, _ := os.Lstat(planted); info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the planted link was kept in place of the export")
	}
}

func TestExportBlockTruncatesAtCap(t *testing.T) {
	// Two-byte runes, so the cap can fall inside one and must back off to a boundary.
	big := strings.Repeat("é", MaxExportBytes/2+10)
	msgs := []kernel.Message{
		{Role: kernel.RoleUser, Kind: kernel.KindText, Text: big, Tokens: 10},
		{Role: kernel.RoleAssistant, Kind: kernel.KindText, Text: "ok", Tokens: 10},
	}
	s, st := roomySession(t, msgs)
	if err := st.ApplyCompress(context.Background(), "s1", store.CompressPlan{
		NewBlocks: []store.NewBlock{{ID: "b1", Tier: 1, Summary: "x", StartSeq: 1, EndSeq: 2,
			SrcTokens: 100, SumTokens: 10}},
		Now: 1, TokensAfter: 10,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.ExportBlock(context.Background(), "b1", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Bytes > MaxExportBytes || res.Bytes < MaxExportBytes-3 {
		t.Fatalf("result = %+v, want a truncated export of about %d bytes", res, MaxExportBytes)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if int(len(raw)) != res.Bytes || !utf8.Valid(raw) {
		t.Fatalf("file is %d bytes, valid UTF-8 %v; want %d valid bytes", len(raw), utf8.Valid(raw), res.Bytes)
	}
}
