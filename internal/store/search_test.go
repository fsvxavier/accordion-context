package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

func TestFtsMatchQuotesEveryWord(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", false},
		{"   ", "", false},
		{`"`, "", false},
		{"*", "", false},
		{"-", "", false},
		{"-x", `"-x"`, true},
		{"a:b", `"a:b"`, true},
		{"NEAR(a b)", `"NEAR(a" "b)"`, true},
		{"AND", `"AND"`, true},
		{`say "hi"`, `"say" """hi"""`, true},
		{"x^ OR", `"x^" "OR"`, true},
		{"caf\x00é", `"café"`, true},
	}
	for _, c := range cases {
		got, ok := ftsMatch(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ftsMatch(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSearchNeverFailsOnFtsSyntax(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	if _, err := s.AppendMessage(ctx, "s1", NewMessage{
		Role: kernel.RoleUser, Kind: kernel.KindText, Text: "the retry timeout is 30s", Tokens: 10,
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`retry "`, `retry OR (`, `NEAR(retry timeout, 2)`, `retry*`, `col:retry`, `^retry`, `-retry`} {
		if _, err := s.Search(ctx, "s1", q, 5); err != nil {
			t.Errorf("Search(%q) failed: %v", q, err)
		}
	}
	for _, q := range []string{`"`, " * ", "()"} {
		if _, err := s.Search(ctx, "s1", q, 5); !errors.Is(err, ErrEmptyQuery) {
			t.Errorf("Search(%q): err = %v, want ErrEmptyQuery", q, err)
		}
	}
}

func TestSearchScopesToSessionAndProject(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	ctx := context.Background()
	if err := s.CreateSession(ctx, Session{ID: "s2", ProjectID: "p1", Model: "m",
		ContextLimit: 200000, Mode: "proxy", Integration: "url"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureProject(ctx, "p2", "/work/p2", "global", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, Session{ID: "s3", ProjectID: "p2", Model: "m",
		ContextLimit: 200000, Mode: "proxy", Integration: "url"}); err != nil {
		t.Fatal(err)
	}
	for sid, text := range map[string]string{
		"s1": "the retry timeout is 30s",
		"s2": "retry timeout again, later",
		"s3": "retry timeout in another project",
	} {
		if _, err := s.AppendMessage(ctx, sid, NewMessage{
			Role: kernel.RoleUser, Kind: kernel.KindText, Text: text, Tokens: 10,
		}); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := s.Search(ctx, "s1", "retry timeout", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "s1" || hits[0].Key != "m00001" || hits[0].Kind != indexKindMessage {
		t.Fatalf("session search = %+v, want only m00001 of s1", hits)
	}
	if want := "the [retry] [timeout] is 30s"; hits[0].Snippet != want {
		t.Errorf("snippet = %q, want %q", hits[0].Snippet, want)
	}

	hits, err = s.SearchProject(ctx, "p1", "retry timeout", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("project search found %d hits, want 2 (s1 and s2, not s3): %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.SessionID == "s3" {
			t.Fatalf("project search leaked another project: %+v", h)
		}
	}

	hits, err = s.Search(ctx, "s1", "retry", 1)
	if err != nil || len(hits) != 1 {
		t.Fatalf("limit 1 gave %d hits, err %v", len(hits), err)
	}
}

// A block is indexed once, when it is created. Folding and unfolding flip its
// active flag but leave its row alone, so its summary stays searchable.
func TestBlockIndexSurvivesFoldAndUnfold(t *testing.T) {
	s := foldedFixture(t)
	ctx := context.Background()

	if n := countIndexRows(t, s, indexKindBlock, "b1"); n != 1 {
		t.Fatalf("b1 indexed %d times after fold, want 1", n)
	}
	if n := countIndexRows(t, s, indexKindMessage, "m00001"); n != 1 {
		t.Fatalf("m00001 indexed %d times, want 1", n)
	}
	hits, err := s.Search(ctx, "s1", "t", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !hasHit(hits, indexKindBlock, "b3") {
		t.Fatalf("deactivated-parent b3 is not searchable: %+v", hits)
	}

	if err := s.ApplyDecompress(ctx, "s1", kernel.DecompressPlan{
		Requested: "b3", Target: "b3", Reactivate: []string{"b1", "b2"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b1", "b2", "b3"} {
		if n := countIndexRows(t, s, indexKindBlock, id); n != 1 {
			t.Fatalf("%s indexed %d times after unfold, want 1", id, n)
		}
	}
	hits, err = s.Search(ctx, "s1", "s", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !hasHit(hits, indexKindBlock, "b1") || !hasHit(hits, indexKindBlock, "b2") {
		t.Fatalf("reactivated blocks are not searchable: %+v", hits)
	}
}

func TestMessagesAreIndexedOnAppend(t *testing.T) {
	s := newMemStore(t)
	setupSession(t, s, "s1")
	appendN(t, s, "s1", 3)
	if n := countIndexRows(t, s, indexKindMessage, "m00002"); n != 1 {
		t.Fatalf("m00002 indexed %d times, want 1", n)
	}
	hits, err := s.Search(context.Background(), "s1", "message", 10)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, h := range hits {
		keys = append(keys, h.Key)
	}
	if want := []string{"m00001", "m00002", "m00003"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func countIndexRows(t *testing.T, s *Store, kind, key string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM search_idx WHERE kind = ? AND key = ?`, kind, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func hasHit(hits []SearchHit, kind, key string) bool {
	for _, h := range hits {
		if h.Kind == kind && h.Key == key {
			return true
		}
	}
	return false
}
