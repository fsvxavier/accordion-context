package acdctx_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/pkg/acdctx"
)

// transcript returns n alternating user and assistant messages, the first one a user
// message. Message i (1-based) has Ref m0000i and the marker <<user-ii>> or <<assistant-ii>>.
func transcript(n int) []acdctx.Message {
	out := make([]acdctx.Message, 0, n)
	for i := 1; i <= n; i++ {
		role, roleName := acdctx.RoleUser, "user"
		if i%2 == 0 {
			role, roleName = acdctx.RoleAssistant, "assistant"
		}
		out = append(out, acdctx.Message{Role: role, Kind: acdctx.KindText, Text: bigText(i, roleName)})
	}
	return out
}

func openClient(t *testing.T) *acdctx.Client {
	t.Helper()
	dir := t.TempDir()
	c, err := acdctx.Open(context.Background(), acdctx.Options{
		DBPath:     dir + "/db/acd-ctx.db",
		Cwd:        dir,
		ConfigPath: dir + "/absent/config.yaml",
		Model:      "claude-test",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestOpenRequiresDBPath(t *testing.T) {
	if _, err := acdctx.Open(context.Background(), acdctx.Options{}); err == nil {
		t.Fatal("Open without DBPath succeeded")
	}
}

func TestLibraryFlowIngestCompressDecompress(t *testing.T) {
	ctx := context.Background()
	c := openClient(t)

	if _, err := c.Compress(ctx, acdctx.CompressArgs{Ranges: []acdctx.CompressRange{{StartRef: "m00001", EndRef: "m00002", Summary: strings.Repeat("s", 80)}}}); !errors.Is(err, acdctx.ErrNoConversation) {
		t.Fatalf("Compress before Turn: got %v, want ErrNoConversation", err)
	}

	msgs := transcript(30)
	turn, err := c.Turn(ctx, msgs, 20_000)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	full := len(turn.Visible)
	if full < 30 {
		t.Fatalf("first turn shows %d messages, want at least 30", full)
	}
	if turn.Visible[0].Ref != "m00001" || turn.Visible[0].Tokens == 0 {
		t.Fatalf("first message = %+v, want ref m00001 with tokens", turn.Visible[0])
	}

	summary := "SUMMARY-MARK: the first twelve messages set up the lorem task."
	res, err := c.Compress(ctx, acdctx.CompressArgs{Ranges: []acdctx.CompressRange{
		{StartRef: "m00002", EndRef: "m00013", Summary: summary, Topic: "setup"},
	}})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(res.BlockIDs) != 1 {
		t.Fatalf("BlockIDs = %v, want one block", res.BlockIDs)
	}

	// The same transcript again: ingest is idempotent and the window shows the summary.
	turn, err = c.Turn(ctx, msgs, 12_000)
	if err != nil {
		t.Fatalf("Turn after compress: %v", err)
	}
	if len(turn.Visible) >= full {
		t.Fatalf("window not reduced: %d messages, was %d", len(turn.Visible), full)
	}
	if !containsText(turn.Visible, "SUMMARY-MARK") {
		t.Fatal("window does not show the summary")
	}
	if containsText(turn.Visible, "<<assistant-02>>") {
		t.Fatal("compressed original still visible")
	}
	if !containsText(turn.Visible, "<<user-01>>") {
		t.Fatal("pinned first user message is missing")
	}

	// Recall returns the original text, and Decompress brings the messages back.
	orig, err := c.Recall(ctx, "m00002")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !strings.Contains(orig, "<<assistant-02>>") {
		t.Fatalf("Recall(m00002) = %.60q, want the original message", orig)
	}
	dec, err := c.Decompress(ctx, res.BlockIDs[0])
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if dec.Target != res.BlockIDs[0] {
		t.Fatalf("Decompress = %+v", dec)
	}
	turn, err = c.Turn(ctx, msgs, 20_000)
	if err != nil {
		t.Fatalf("Turn after decompress: %v", err)
	}
	if !containsText(turn.Visible, "<<assistant-02>>") || containsText(turn.Visible, "SUMMARY-MARK") {
		t.Fatal("decompress did not restore the original messages")
	}
}

func TestCompressRejectsInvalidRangeAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	c := openClient(t)
	if _, err := c.Turn(ctx, transcript(30), 20_000); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	_, err := c.Compress(ctx, acdctx.CompressArgs{Ranges: []acdctx.CompressRange{
		{StartRef: "m00001", EndRef: "m00029", Summary: "too wide, touches the recent zone"},
	}})
	if err == nil {
		t.Fatal("Compress into the recent zone succeeded")
	}
	before, err := c.Turn(ctx, transcript(30), 20_000)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if _, err := c.Compress(ctx, acdctx.CompressArgs{}); err == nil {
		t.Fatal("empty Compress succeeded")
	}
	after, err := c.Turn(ctx, transcript(30), 20_000)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if len(after.Visible) != len(before.Visible) || containsText(after.Visible, "SUMMARY") {
		t.Fatalf("rejected compress changed the window: %d -> %d messages", len(before.Visible), len(after.Visible))
	}
}

func TestNudgeIsReturnedWhenContextIsFull(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, err := acdctx.Open(ctx, acdctx.Options{
		DBPath: dir + "/acd-ctx.db", Cwd: dir, ConfigPath: dir + "/absent.yaml", ContextLimit: 40_000,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	turn, err := c.Turn(ctx, transcript(30), 38_000)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if turn.Nudge == "" || !strings.Contains(turn.Nudge, "<context_nudge>") {
		t.Fatalf("nudge = %q, want a context_nudge note (row %s)", turn.Nudge, turn.Row)
	}
}

func TestClosedClientRefusesWork(t *testing.T) {
	ctx := context.Background()
	c := openClient(t)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := c.Turn(ctx, transcript(2), 10); !errors.Is(err, acdctx.ErrClosed) {
		t.Fatalf("Turn after Close: %v, want ErrClosed", err)
	}
}

func TestTurnRejectsUnknownRole(t *testing.T) {
	c := openClient(t)
	_, err := c.Turn(context.Background(), []acdctx.Message{{Role: "narrator", Kind: acdctx.KindText, Text: "x"}}, 1)
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("Turn with bad role: %v", err)
	}
}

func containsText(msgs []acdctx.Message, s string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Text, s) {
			return true
		}
	}
	return false
}
