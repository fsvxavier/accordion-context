package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/obs"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// replayable are the event types that the kernel and the session produce from the
// trace's inputs. Replay compares these and nothing else: the others (for example
// nudge_deferred, which depends on the cache gate) are not reproduced.
var replayable = map[string]bool{
	"turn": true, "compress": true, "decompress": true, "recall_hint": true, "refuse": true,
}

// replayCounter names each in-memory database, so replays in one process do not share state.
var replayCounter atomic.Int64

// replayResult is what a replay leaves behind: the events of the replayed session
// and its final window.
type replayResult struct {
	Session   string
	Events    []store.EventRow
	Skipped   int      // trace events that replay does not reproduce
	Errors    []string // recorded calls that the replayed session refused
	Window    kernel.Snapshot
	Rendered  []kernel.Message
	BlockTier map[int]int // active blocks per tier
}

// runReplay handles "replay [--check] <trace.jsonl>". It re-feeds the messages
// and the recorded turns, compressions and decompressions through an in-memory
// store and the kernel, then prints the final window and the events. With --check
// it exits 1 unless the replayed events equal the trace's replayable events.
func runReplay(args []string, s IO) int {
	fset := newFlagSet("replay", s)
	check := fset.Bool("check", false, "exit 1 unless the replayed events equal the trace's events")
	pos, err := parseFlags(fset, args)
	if err != nil || len(pos) != 1 {
		return usageError(s, "usage: replay [--check] <trace.jsonl>")
	}
	f, err := os.Open(pos[0])
	if err != nil {
		return fail(s, err)
	}
	defer f.Close()
	tr, err := obs.ReadTrace(f)
	if err != nil {
		return fail(s, fmt.Errorf("read trace: %w", err))
	}
	res, err := replayTrace(s.Ctx, tr)
	if err != nil {
		return fail(s, fmt.Errorf("replay: %w", err))
	}
	writeReplay(s.Stdout, res)
	if *check {
		if tr.Session.Anonymized {
			// Placeholder text changes token estimates and recall hints, so only the
			// sequence of event types is compared.
			if diff := compareTypes(tr.Events, res.Events); diff != "" {
				fmt.Fprintf(s.Stderr, "acd-ctx: replay differs from the trace: %s\n", diff)
				return ExitFail
			}
			fmt.Fprintln(s.Stdout, "replay check: event types identical (anonymized trace, payloads not compared)")
			return ExitOK
		}
		if len(res.Errors) > 0 {
			fmt.Fprintf(s.Stderr, "acd-ctx: replay refused recorded calls: %s\n", strings.Join(res.Errors, "; "))
			return ExitFail
		}
		if diff := compareEvents(tr.Events, res.Events); diff != "" {
			fmt.Fprintf(s.Stderr, "acd-ctx: replay differs from the trace: %s\n", diff)
			return ExitFail
		}
		fmt.Fprintln(s.Stdout, "replay check: events identical")
	}
	return ExitOK
}

// replayTrace runs tr through a fresh in-memory store. Messages are ingested up to
// the count each recorded turn saw, so every Plan runs on the ledger it ran on.
func replayTrace(ctx context.Context, tr obs.Trace) (replayResult, error) {
	var out replayResult
	dsn := fmt.Sprintf("file:acd-replay-%d?mode=memory&cache=shared", replayCounter.Add(1))
	st, err := store.Open(dsn)
	if err != nil {
		return out, err
	}
	defer st.Close()

	sid := tr.Session.ID
	out.Session = sid
	pid := tr.Session.ProjectID
	if err := st.EnsureProject(ctx, pid, "replay:"+pid, "global", time.Now().Unix()); err != nil {
		return out, err
	}
	if err := st.CreateSession(ctx, store.Session{
		ID: sid, ProjectID: pid, Model: tr.Session.Model, ContextLimit: tr.Session.ContextLimit,
		Mode: tr.Session.Mode, Integration: tr.Session.Integration,
	}); err != nil {
		return out, err
	}
	sess := session.New(st, sid)

	msgs := make([]kernel.Message, 0, len(tr.Messages))
	refs := make(map[int64]string, len(tr.Messages))
	for _, m := range tr.Messages {
		refs[m.Seq] = m.Ref
		msgs = append(msgs, kernel.Message{
			Role: kernel.Role(m.Role), Kind: kernel.Kind(m.Kind),
			ToolName: m.ToolName, ToolCallID: m.ToolCallID,
			Text: bodyText(m.Text), Tokens: m.Tokens,
		})
	}
	blocks := make(map[string]obs.BlockLine, len(tr.Blocks))
	for _, b := range tr.Blocks {
		blocks[b.ID] = b
	}

	ingested := 0
	ingest := func(n int) error {
		if n < ingested || n > len(msgs) {
			return fmt.Errorf("ingest %d messages after %d (trace has %d)", n, ingested, len(msgs))
		}
		if n == ingested {
			return nil
		}
		if _, err := sess.Ingest(ctx, msgs[:n]); err != nil {
			return err
		}
		ingested = n
		return nil
	}

	for _, e := range tr.Events {
		switch e.Type {
		case "turn":
			var p obs.TurnEvent
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return out, fmt.Errorf("event %d: %w", e.Seq, err)
			}
			if err := ingest(p.Messages); err != nil {
				return out, fmt.Errorf("event %d: %w", e.Seq, err)
			}
			if p.ContextLimit > 0 {
				if err := st.SetContextLimit(ctx, sid, p.ContextLimit); err != nil {
					return out, fmt.Errorf("event %d: %w", e.Seq, err)
				}
			}
			if p.Settings.Kernel.StepBase != 0 {
				kcfg, err := p.Settings.Kernel.Config()
				if err != nil {
					return out, fmt.Errorf("event %d: %w", e.Seq, err)
				}
				sess.SetOptions(session.Options{
					Kernel: kcfg, RecentKeep: p.Settings.RecentKeep,
					MinCompressRange: p.Settings.MinCompressRange, MinSummaryChars: p.Settings.MinSummaryChars,
				})
			}
			if err := st.AppendEvent(ctx, sid, "turn", e.Payload); err != nil {
				return out, err
			}
			if _, err := sess.Plan(ctx, p.Tokens); err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("event %d: plan: %v", e.Seq, err))
			}
		case "compress":
			var p struct {
				Blocks       []string `json:"blocks"`
				CloseIntents []int64  `json:"closedIntents"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return out, fmt.Errorf("event %d: %w", e.Seq, err)
			}
			if err := ingest(ingested); err != nil {
				return out, err
			}
			args, err := compressArgs(p.Blocks, blocks, refs)
			if err != nil {
				return out, fmt.Errorf("event %d: %w", e.Seq, err)
			}
			args.CloseIntents = p.CloseIntents
			if _, err := sess.HandleCompress(ctx, args); err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("event %d: compress: %v", e.Seq, err))
			}
		case "decompress":
			var p struct {
				Requested string `json:"requested"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return out, fmt.Errorf("event %d: %w", e.Seq, err)
			}
			if _, err := sess.Decompress(ctx, p.Requested); err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("event %d: decompress: %v", e.Seq, err))
			}
		case "recall_hint", "refuse":
			// Written by the Plan above, so they are not replayed on their own.
		default:
			out.Skipped++
		}
	}
	if err := ingest(len(msgs)); err != nil {
		return out, err
	}

	if out.Events, err = st.ListEvents(ctx, sid, 0); err != nil {
		return out, err
	}
	snap, err := st.Snapshot(ctx, sid)
	if err != nil {
		return out, err
	}
	out.Window = snap
	out.Rendered = kernel.Render(snap)
	out.BlockTier = map[int]int{}
	for _, b := range snap.Blocks {
		if b.Active {
			out.BlockTier[b.Tier]++
		}
	}
	return out, nil
}

// compressArgs rebuilds the compress call that produced the blocks, in the order
// the event lists them. A tier 1 block is a range over its messages; a tier 2 or 3
// block is a fold over its sources.
func compressArgs(ids []string, blocks map[string]obs.BlockLine, refs map[int64]string) (session.CompressArgs, error) {
	var args session.CompressArgs
	for _, id := range ids {
		b, ok := blocks[id]
		if !ok {
			return args, fmt.Errorf("block %s is not in the trace", id)
		}
		summary, topic := bodyText(b.Summary), bodyText(b.Topic)
		if b.Tier == 1 {
			start, end := refOf(refs, b.StartSeq), refOf(refs, b.EndSeq)
			args.Ranges = append(args.Ranges, session.CompressRange{
				StartRef: start, EndRef: end, Summary: summary, Topic: topic,
			})
			continue
		}
		if len(b.Sources) == 0 {
			return args, fmt.Errorf("block %s (tier %d) has no sources", id, b.Tier)
		}
		args.Folds = append(args.Folds, session.FoldRange{
			Tier: b.Tier, StartBlock: b.Sources[0], EndBlock: b.Sources[len(b.Sources)-1],
			Summary: summary, Topic: topic,
		})
	}
	return args, nil
}

func refOf(refs map[int64]string, seq int64) string {
	if r, ok := refs[seq]; ok {
		return r
	}
	return kernel.FormatRef(seq)
}

// bodyText is the text of a trace body. An anonymized body has no value, so a
// placeholder of the recorded length stands in: sizes and decisions are kept.
func bodyText(b obs.Body) string {
	if b.Value != nil {
		return *b.Value
	}
	return strings.Repeat("x", b.Len)
}

// compareTypes reports the first difference between the sequences of replayable
// event types, or "" when they match.
func compareTypes(want []obs.EventLine, got []store.EventRow) string {
	var w, g []string
	for _, e := range want {
		if replayable[e.Type] {
			w = append(w, e.Type)
		}
	}
	for _, e := range got {
		if replayable[e.Type] {
			g = append(g, e.Type)
		}
	}
	for i := 0; i < len(w) || i < len(g); i++ {
		switch {
		case i >= len(w):
			return fmt.Sprintf("extra replayed event %d: %s", i+1, g[i])
		case i >= len(g):
			return fmt.Sprintf("missing replayed event %d: %s", i+1, w[i])
		case w[i] != g[i]:
			return fmt.Sprintf("event %d: trace %s, replay %s", i+1, w[i], g[i])
		}
	}
	return ""
}

// compareEvents reports the first difference between the trace's replayable events
// and the replayed ones, or "" when they match.
func compareEvents(want []obs.EventLine, got []store.EventRow) string {
	var w, g []string
	for _, e := range want {
		if replayable[e.Type] {
			w = append(w, e.Type+" "+canonicalPayload(e.Payload))
		}
	}
	for _, e := range got {
		if replayable[e.Type] {
			g = append(g, e.Type+" "+canonicalPayload(e.Payload))
		}
	}
	for i := 0; i < len(w) || i < len(g); i++ {
		switch {
		case i >= len(w):
			return fmt.Sprintf("extra replayed event %d: %s", i+1, g[i])
		case i >= len(g):
			return fmt.Sprintf("missing replayed event %d: %s", i+1, w[i])
		case w[i] != g[i]:
			return fmt.Sprintf("event %d: trace %s, replay %s", i+1, w[i], g[i])
		}
	}
	return ""
}

// canonicalPayload re-encodes a payload with sorted keys and without "path", the
// key that an anonymized trace drops.
func canonicalPayload(raw json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return string(raw)
	}
	delete(m, "path")
	out, err := json.Marshal(m)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// writeReplay prints the final window and the events of a replay.
func writeReplay(w io.Writer, res replayResult) {
	tokens := 0
	for _, m := range res.Rendered {
		tokens += m.Tokens
	}
	fmt.Fprintf(w, "session %s: %d messages, %d events replayed, %d skipped, %d refused\n",
		res.Session, len(res.Window.Messages), len(res.Events), res.Skipped, len(res.Errors))
	fmt.Fprintf(w, "window: %d visible messages, %d tokens; active blocks: T1=%d T2=%d T3=%d\n",
		len(res.Rendered), tokens, res.BlockTier[1], res.BlockTier[2], res.BlockTier[3])
	for _, e := range res.Events {
		fmt.Fprintf(w, "%d\t%s\t%s\n", e.Seq, e.Type, e.Payload)
	}
}
