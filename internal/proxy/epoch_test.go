package proxy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// A purge through a second handle, while the proxy serves, must not leave the
// proxy holding the purged session: the next request creates it again, and its
// ledger holds only the new transcript.
func TestPurgeFromSecondHandleRecreatesSessionCleanly(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, dbPath := newProxy(t, up)
	srv := ps.Config.Handler.(*Server)
	body := `{"model":"claude-x","max_tokens":64,"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"again"}]}`

	if resp, got := postMessages(t, ps.URL, body, nil); resp.StatusCode != 200 {
		t.Fatalf("first turn: %d %s", resp.StatusCode, got)
	}
	first := countRows(t, dbPath, `SELECT COUNT(*) FROM messages`)
	if first == 0 {
		t.Fatal("first turn wrote no messages")
	}
	var oldSID string
	var oldSess *session.Session
	srv.mu.Lock()
	for sid, s := range srv.sessions {
		oldSID, oldSess = sid, s
	}
	srv.mu.Unlock()
	if oldSess == nil {
		t.Fatal("proxy cached no session after the first turn")
	}

	ctx := context.Background()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	projects, err := st.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects = %v, %v", projects, err)
	}
	if _, err := st.PurgeProject(ctx, projects[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("sessions after purge = %d", n)
	}

	if resp, got := postMessages(t, ps.URL, body, nil); resp.StatusCode != 200 {
		t.Fatalf("turn after purge: %d %s", resp.StatusCode, got)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions after recreate = %d, want 1", n)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM messages`); n != first {
		t.Fatalf("messages after recreate = %d, want the clean ledger of %d", n, first)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if cur := srv.sessions[oldSID]; cur == nil || cur == oldSess {
		t.Fatalf("proxy still holds the purged session object")
	}
	if len(srv.tunes[oldSID].held) != 0 {
		t.Fatal("held spans survived the purge")
	}
	if ep, _ := st.Epoch(ctx); srv.epochs[srv.cfg.Store] != ep {
		t.Fatalf("proxy epoch %d, store epoch %d", srv.epochs[srv.cfg.Store], ep)
	}
}

// Without a purge the proxy keeps its cache: the epoch check is only a read.
func TestNoPurgeKeepsProxyCache(t *testing.T) {
	up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
		return 200, "application/json", plainAnswer
	})
	ps, _, _ := newProxy(t, up)
	srv := ps.Config.Handler.(*Server)
	body := `{"model":"claude-x","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
	postMessages(t, ps.URL, body, nil)
	srv.mu.Lock()
	var before *session.Session
	for _, s := range srv.sessions {
		before = s
	}
	srv.mu.Unlock()
	postMessages(t, ps.URL, body, nil)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, s := range srv.sessions {
		if s != before {
			t.Fatal("cache dropped without a purge")
		}
	}
}
