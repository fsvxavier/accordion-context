package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/obs"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

var driveCounter atomic.Int64

// driveSession runs a session the way the proxy does: every turn ingests the
// transcript, records a turn event, plans, and then acts on the nudge the way a
// cooperative model would (compress the first candidate span, fold tier 1 blocks
// when a tier 2 nudge comes, decompress a block now and then). It returns the
// store and the session id.
func driveSession(t *testing.T, turns int) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dsn := fmt.Sprintf("file:cli-drive-%d?mode=memory&cache=shared", driveCounter.Add(1))
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	const sid = "s_drive"
	if err := st.EnsureProject(ctx, "proj", "/tmp/proj", "global", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, store.Session{
		ID: sid, ProjectID: "proj", Model: "claude-x", ContextLimit: 8000, Mode: "proxy", Integration: "url",
	}); err != nil {
		t.Fatal(err)
	}
	sess := session.New(st, sid)
	// Non-default options, so replay has to restore them from the trace.
	kcfg := kernel.DefaultConfig()
	kcfg.StepBase, kcfg.StepMin = 20_000, 5_000
	sess.SetOptions(session.Options{Kernel: kcfg, RecentKeep: 4,
		MinCompressRange: session.MinCompressRange, MinSummaryChars: session.MinSummaryChars})

	secret := "SECRET-DRIVE-PHRASE"
	var msgs []kernel.Message
	add := func(role kernel.Role, kind kernel.Kind, text, tool, callID string) {
		msgs = append(msgs, kernel.Message{Role: role, Kind: kind, Text: text,
			ToolName: tool, ToolCallID: callID, Tokens: kernel.EstimateTokens(text)})
	}
	add(kernel.RoleUser, kernel.KindText, "Please fix the parser bug. "+secret, "", "")
	for i := 0; i < turns; i++ {
		body := strings.Repeat(fmt.Sprintf("reading parser file %d and noting details ", i), 40)
		callID := fmt.Sprintf("call-%d", i)
		add(kernel.RoleAssistant, kernel.KindToolCall, `{"path":"parse.go"}`, "read", callID)
		add(kernel.RoleTool, kernel.KindToolResult, body, "read", callID)
		note := fmt.Sprintf("Reviewed chunk %d.", i)
		if i == 7 {
			note = "Still looking at m00003 for the parser."
		}
		add(kernel.RoleAssistant, kernel.KindText, note, "", "")
		add(kernel.RoleUser, kernel.KindText, fmt.Sprintf("Continue with step %d.", i), "", "")

		if _, err := sess.Ingest(ctx, msgs); err != nil {
			t.Fatalf("ingest turn %d: %v", i, err)
		}
		if i == 15 {
			// The window changes mid-session, as when the project config changes.
			if err := st.SetContextLimit(ctx, sid, 6000); err != nil {
				t.Fatal(err)
			}
		}
		snap, err := st.Snapshot(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		tokens := 0
		for _, m := range kernel.Render(snap) {
			tokens += m.Tokens
		}
		opts := sess.Settings()
		if err := st.AppendEvent(ctx, sid, "turn", obs.TurnEvent{
			Tokens: tokens, Messages: len(snap.Messages), ContextLimit: snap.ContextLimit,
			Settings: obs.SettingsOf(opts.Kernel, opts.RecentKeep, opts.MinCompressRange, opts.MinSummaryChars),
		}); err != nil {
			t.Fatal(err)
		}
		plan, err := sess.Plan(ctx, tokens)
		if err != nil {
			t.Fatalf("plan turn %d: %v", i, err)
		}
		if plan.Decision.Nudge && plan.Decision.Tier == 1 {
			for _, sp := range kernel.CompressibleSpans(snap, sess.Settings().RecentKeep) {
				if sp.Tokens < sess.Settings().MinCompressRange {
					continue
				}
				_, _ = sess.HandleCompress(ctx, session.CompressArgs{Ranges: []session.CompressRange{{
					StartRef: sp.Start, EndRef: sp.End, Topic: "parser reads",
					Summary: "Read the parser files and noted the details of each chunk, " + strings.Repeat("kept short. ", 6),
				}}})
				break
			}
		}
		if plan.Decision.Nudge && plan.Decision.Tier >= 2 {
			var first, last string
			for _, b := range snap.Blocks {
				if b.Active && b.Tier == 1 {
					if first == "" {
						first = b.ID
					}
					last = b.ID
				}
			}
			if first != "" && first != last {
				_, _ = sess.HandleCompress(ctx, session.CompressArgs{Folds: []session.FoldRange{{
					Tier: 2, StartBlock: first, EndBlock: last, Topic: "folded reads",
					Summary: "Folded the read blocks into one summary of the parser work so far, " + strings.Repeat("short. ", 8),
				}}})
			}
		}
		if i%6 == 5 {
			for _, b := range snap.Blocks {
				if b.Active {
					_, _ = sess.Decompress(ctx, b.ID)
					break
				}
			}
		}
	}
	return st, sid
}

// TestReplayReproducesEvents exports a driven session, reads the trace back and
// replays it: the replayed decisions must equal the recorded ones.
func TestReplayReproducesEvents(t *testing.T) {
	ctx := context.Background()
	st, sid := driveSession(t, 30)

	tr, err := obs.ExportTrace(ctx, st, sid, false)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := obs.WriteTrace(&buf, tr); err != nil {
		t.Fatal(err)
	}
	back, err := obs.ReadTrace(&buf)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	counts := map[string]int{}
	for _, e := range back.Events {
		counts[e.Type]++
	}
	if counts["turn"] == 0 || counts["compress"] == 0 || counts["decompress"] == 0 {
		t.Fatalf("driven session lacks the events under test: %v", counts)
	}
	if !strings.Contains(string(back.Events[0].Payload), `"recent_keep":4`) {
		t.Fatalf("turn payload lacks the session settings: %s", back.Events[0].Payload)
	}

	res, err := replayTrace(ctx, back)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("replay refused recorded calls: %v", res.Errors)
	}
	if diff := compareEvents(back.Events, res.Events); diff != "" {
		t.Fatalf("replay diverges: %s", diff)
	}
	if len(res.Rendered) == 0 {
		t.Fatal("replayed window is empty")
	}

	// The comparison is not vacuous: a recorded decision that changed is reported.
	tampered := append([]obs.EventLine(nil), back.Events...)
	for i := range tampered {
		if tampered[i].Type == "compress" {
			tampered[i].Payload = json.RawMessage(`{"blocks":[],"tokensAfter":0}`)
			break
		}
	}
	if compareEvents(tampered, res.Events) == "" {
		t.Fatal("compareEvents missed a changed compress event")
	}
}

// TestReplayCommandCheck runs the command on a trace file and expects exit 0.
func TestReplayCommandCheck(t *testing.T) {
	ctx := context.Background()
	st, sid := driveSession(t, 12)
	tr, err := obs.ExportTrace(ctx, st, sid, false)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/trace.jsonl"
	var buf bytes.Buffer
	if err := obs.WriteTrace(&buf, tr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := Run([]string{"replay", "--check", path}, IO{Stdout: &out, Stderr: &errw}); code != ExitOK {
		t.Fatalf("replay --check exit %d: %s%s", code, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "events identical") || !strings.Contains(out.String(), "window:") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	if code := Run([]string{"replay"}, IO{Stdout: &out, Stderr: &errw}); code != ExitUsage {
		t.Fatalf("replay without a file: exit %d, want %d", code, ExitUsage)
	}
}

// TestAnonymizedTraceHasNoText checks that an anonymized export replays (with
// placeholder text of the same length) and carries none of the original text.
func TestAnonymizedTraceHasNoText(t *testing.T) {
	ctx := context.Background()
	st, sid := driveSession(t, 12)
	tr, err := obs.ExportTrace(ctx, st, sid, true)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := obs.WriteTrace(&buf, tr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "SECRET-DRIVE-PHRASE") || strings.Contains(buf.String(), "Reviewed chunk") {
		t.Fatal("anonymized trace carries message text")
	}
	back, err := obs.ReadTrace(&buf)
	if err != nil {
		t.Fatal(err)
	}
	res, err := replayTrace(ctx, back)
	if err != nil {
		t.Fatalf("anonymized replay: %v", err)
	}
	if diff := compareTypes(back.Events, res.Events); diff != "" {
		t.Logf("anonymized replay: %s", diff)
	}
}
