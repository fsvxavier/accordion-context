package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// twoProjects opens one store with project p1 holding session s1 and project p2
// holding session s2, each ingested with its own messages.
func twoProjects(t *testing.T, m1, m2 []kernel.Message) (*store.Store, *Session, *Session) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	for i, msgs := range [][]kernel.Message{m1, m2} {
		pid := []string{"p1", "p2"}[i]
		sid := []string{"s1", "s2"}[i]
		if err := st.EnsureProject(ctx, pid, "/work/"+pid, "local", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateSession(ctx, store.Session{
			ID: sid, ProjectID: pid, Model: "test", ContextLimit: 200000, Mode: "library", Integration: "library",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := New(st, sid).Ingest(ctx, msgs); err != nil {
			t.Fatal(err)
		}
	}
	return st, New(st, "s1"), New(st, "s2")
}

func textMsgs(texts ...string) []kernel.Message {
	out := make([]kernel.Message, 0, len(texts))
	for i, tx := range texts {
		role := kernel.RoleUser
		if i%2 == 1 {
			role = kernel.RoleAssistant
		}
		out = append(out, kernel.Message{Role: role, Kind: kernel.KindText, Text: tx, Tokens: kernel.EstimateTokens(tx)})
	}
	return out
}

func TestSearchCapsHitsAndSnippets(t *testing.T) {
	var texts []string
	for i := 0; i < 30; i++ {
		texts = append(texts, "needle "+strings.Repeat("filler words around the needle ", 40))
	}
	_, s1, _ := twoProjects(t, textMsgs(texts...), nil)
	ctx := context.Background()

	hits, err := s1.Search(ctx, "needle", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != MaxSearchHits {
		t.Fatalf("hits = %d, want the cap %d", len(hits), MaxSearchHits)
	}
	for _, h := range hits {
		if n := utf8.RuneCountInString(h.Snippet); n > MaxSnippetRunes+1 {
			t.Fatalf("snippet has %d runes, cap is %d", n, MaxSnippetRunes)
		}
		if strings.ContainsAny(h.Snippet, "\n\r") {
			t.Fatalf("snippet is not one line: %q", h.Snippet)
		}
	}
	if hits, _ = s1.Search(ctx, "needle", 0); len(hits) != DefaultSearchHits {
		t.Fatalf("default limit gave %d hits, want %d", len(hits), DefaultSearchHits)
	}
}

func TestSearchProjectScopeStaysInProject(t *testing.T) {
	_, s1, s2 := twoProjects(t,
		textMsgs("the zebrafish tank needs cleaning"),
		textMsgs("a marmot was seen near the zebrafish pond"))
	ctx := context.Background()

	if hits, _ := s1.Search(ctx, "marmot", 5); len(hits) != 0 {
		t.Fatalf("session scope saw another session: %+v", hits)
	}
	// The caller passes the project of its request: a p1 request never reads p2.
	if hits, _ := s1.SearchProject(ctx, "p1", "marmot", 5); len(hits) != 0 {
		t.Fatalf("project scope of p1 leaked p2 data: %+v", hits)
	}
	hits, err := s1.SearchProject(ctx, "p1", "zebrafish", 5)
	if err != nil || len(hits) != 1 || hits[0].SessionID != "s1" {
		t.Fatalf("project scope of p1 = %+v, %v", hits, err)
	}
	if hits, _ := s2.SearchProject(ctx, "p2", "zebrafish", 5); len(hits) != 1 || hits[0].SessionID != "s2" {
		t.Fatalf("p2 project search = %+v", hits)
	}
}

// bigMsgs makes three messages of about 3000 tokens each, enough for one fold
// (the compress minimum is 5000 tokens over the range).
func bigMsgs() []kernel.Message {
	var texts []string
	for i := 1; i <= 16; i++ {
		texts = append(texts, strings.Repeat("step "+string(rune('0'+i))+" reading the lexer source and notes. ", 300))
	}
	return textMsgs(texts...)
}

func TestRetrieveFoldedRefAndBlock(t *testing.T) {
	st, s1, _ := twoProjects(t, bigMsgs(), nil)
	ctx := context.Background()
	if _, err := s1.HandleCompress(ctx, CompressArgs{Ranges: []CompressRange{{
		StartRef: "m00002", EndRef: "m00004",
		Summary: "Read the lexer source in detail across three steps; the bug is not in the lexer.",
	}}}); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Snapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(snap.Blocks))
	}
	blk := snap.Blocks[0].ID

	res, err := s1.Retrieve(ctx, "m00003", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Messages != 1 || !strings.HasPrefix(res.Text, "[m00003] step 3 reading the lexer") {
		t.Fatalf("ref retrieve = %d messages, %.60q", res.Messages, res.Text)
	}
	res, err = s1.Retrieve(ctx, blk, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Messages != 3 || !strings.Contains(res.Text, "[m00004] step 4") {
		t.Fatalf("block retrieve = %d messages", res.Messages)
	}
	res, err = s1.Retrieve(ctx, blk, "m00003..m00003")
	if err != nil || res.Messages != 1 || strings.Contains(res.Text, "[m00002]") {
		t.Fatalf("block range = %+v, %v", res, err)
	}
	if _, err := s1.Retrieve(ctx, blk, "m00010..m00012"); err == nil {
		t.Error("range outside the block succeeded")
	}
	if _, err := s1.Retrieve(ctx, "b99", ""); err == nil {
		t.Error("unknown block succeeded")
	}
	if _, err := s1.Retrieve(ctx, "m99999", ""); err == nil {
		t.Error("unknown ref succeeded")
	}
	if _, err := s1.Retrieve(ctx, "m00001", "m00005"); err == nil {
		t.Error("malformed range succeeded")
	}
}

func TestRetrieveTextIsCapped(t *testing.T) {
	huge := strings.Repeat("é", 200000)
	_, s1, _ := twoProjects(t, textMsgs(huge), nil)
	res, err := s1.Retrieve(context.Background(), "m00001", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("oversized message not truncated")
	}
	if n := utf8.RuneCountInString(res.Text); n > MaxRetrieveTokens*charsPerToken {
		t.Fatalf("text has %d runes, cap is %d", n, MaxRetrieveTokens*charsPerToken)
	}
	if !utf8.ValidString(res.Text) {
		t.Fatal("cut split a rune")
	}
}
