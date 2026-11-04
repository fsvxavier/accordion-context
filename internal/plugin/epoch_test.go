package plugin

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// A purge through a second handle must not leave the plugin writing into the
// purged conversation from its cache. The next turn creates the conversation
// again, and its ledger holds only the new transcript.
func TestPurgeFromSecondHandleForgetsConversation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	conv := "conv-epoch"
	msgs := transcript(12)
	rtBody := map[string]any{"conversationId": conv, "model": "m-test", "contextWindow": 200000, "cwd": e.dir}

	if rec := e.do(t, "POST", pathRuntime, rtBody, nil); rec.Code != http.StatusOK {
		t.Fatalf("runtime-info: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": msgs}, nil); rec.Code != http.StatusOK {
		t.Fatalf("turn: %d %s", rec.Code, rec.Body.String())
	}
	sid := sessionID(e.proj.ID, conv)
	snap, err := e.st.Snapshot(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	first := len(snap.Messages)

	second, err := store.Open(e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.PurgeProject(ctx, e.proj.ID); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, e.dbPath, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("sessions after purge = %d", n)
	}
	if rec := e.do(t, "POST", pathTurn, map[string]any{"conversationId": conv, "messages": msgs}, nil); rec.Code != http.StatusOK {
		t.Fatalf("turn after purge: %d %s", rec.Code, rec.Body.String())
	}
	snap, err = e.st.Snapshot(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != first {
		t.Fatalf("ledger after recreate has %d messages, want %d", len(snap.Messages), first)
	}
}

func countRows(t *testing.T, dbPath, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
