package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSessionWindowAndSetContextLimit(t *testing.T) {
	st := newMemStore(t)
	ctx := context.Background()
	if err := st.EnsureProject(ctx, "p1", "/tmp/p1", "global", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, Session{
		ID: "s1", ProjectID: "p1", Model: "claude-x", ContextLimit: 200_000, Mode: "proxy", Integration: "url",
	}); err != nil {
		t.Fatal(err)
	}
	model, limit, err := st.SessionWindow(ctx, "s1")
	if err != nil || model != "claude-x" || limit != 200_000 {
		t.Fatalf("SessionWindow = %q, %d, %v", model, limit, err)
	}
	if _, _, err := st.SessionWindow(ctx, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing session: err = %v, want sql.ErrNoRows", err)
	}

	if err := st.SetContextLimit(ctx, "s1", 60_000); err != nil {
		t.Fatal(err)
	}
	if _, limit, _ := st.SessionWindow(ctx, "s1"); limit != 60_000 {
		t.Fatalf("limit = %d after SetContextLimit", limit)
	}
	if model, _, _ := st.SessionWindow(ctx, "s1"); model != "claude-x" {
		t.Fatalf("model changed to %q; only the window may change", model)
	}
	if err := st.SetContextLimit(ctx, "s1", 0); err == nil {
		t.Fatal("zero limit must be rejected")
	}
	if err := st.SetContextLimit(ctx, "nope", 1000); err == nil {
		t.Fatal("unknown session must report an error")
	}
	if err := st.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("invariants: %v", err)
	}
}
