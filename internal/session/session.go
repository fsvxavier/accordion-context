// Package session orchestrates one conversation on top of store (persistence)
// and kernel (pure decisions). It owns no SQL and no protocol parsing.
package session

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

const (
	// recentKeep is how many trailing messages a compress call may never touch.
	// Stand-in for spec §10.2 PreserveRecentMessages (not yet configurable).
	recentKeep = 10
	// charsPerToken is the token estimate used until the §15 calibration exists.
	charsPerToken = 4
)

// defaults supplies the §10.2 minimums (MinCompressRange, MinSummaryChars).
// Config wiring is not in place yet, so the spec defaults apply.
var defaults = config.Defaults()

// MinCompressRange and MinSummaryChars are the §10.2 minimums that HandleCompress
// enforces. The proxy must filter the spans it advertises (kernel.CompressibleSpans
// has no token floor) by MinCompressRange, or it offers ranges that compress
// then rejects.
var (
	MinCompressRange = defaults.MinCompressRange
	MinSummaryChars  = defaults.MinSummaryChars
)

// RecentKeep exports recentKeep so the proxy's nudge lists the same spans that
// HandleCompress accepts.
const RecentKeep = recentKeep

// Options are the per-session settings that the configuration supplies. A zero
// field takes its default; a zero Kernel (StepBase 0) means kernel.DefaultConfig.
type Options struct {
	Kernel           kernel.Config // trigger thresholds (§8)
	MinCompressRange int           // smallest range or fold in tokens (§10.2)
	MinSummaryChars  int           // shortest accepted summary in characters (§10.2)
	RecentKeep       int           // trailing messages no compress call may touch (§10.2)
	FramingCheck     bool          // reject imperative-mood summaries (§11.2, opt-in)
}

// withDefaults fills the zero fields of o with the spec defaults.
func (o Options) withDefaults() Options {
	if o.Kernel.StepBase == 0 {
		o.Kernel = kernel.DefaultConfig()
	}
	if o.MinCompressRange <= 0 {
		o.MinCompressRange = defaults.MinCompressRange
	}
	if o.MinSummaryChars <= 0 {
		o.MinSummaryChars = defaults.MinSummaryChars
	}
	if o.RecentKeep <= 0 {
		o.RecentKeep = recentKeep
	}
	return o
}

// Session binds one store session id to the orchestration steps.
type Session struct {
	mu   sync.Mutex
	st   *store.Store
	sid  string
	opts Options
	// hinted remembers the recall hints already logged for a user turn
	// (keyed by the turn's last user seq), so a tool loop that calls Plan on
	// every request logs each hint once. Hints are still returned every time.
	hinted map[string]bool
}

// New wraps an existing store session with the spec defaults. The caller creates
// the session row (store.CreateSession) before calling New.
func New(st *store.Store, sid string) *Session {
	return NewWithOptions(st, sid, Options{})
}

// NewWithOptions wraps an existing store session with opts (zero fields take
// their defaults). See New for the session row.
func NewWithOptions(st *store.Store, sid string, opts Options) *Session {
	return &Session{st: st, sid: sid, opts: opts.withDefaults()}
}

// Settings returns the effective options of the session.
func (s *Session) Settings() Options {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts
}

// SetOptions replaces the options of a live session. The proxy calls it with the
// configuration of the session's project, which may change between requests.
func (s *Session) SetOptions(opts Options) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts = opts.withDefaults()
}

// Ingest appends the messages that the store does not have yet. msgs is the
// full client transcript: the first len(stored) entries must match the stored
// ledger (role, kind, tool fields, text) or Ingest fails without writing.
// Returns the number of newly appended messages.
func (s *Session) Ingest(ctx context.Context, msgs []kernel.Message) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return 0, err
	}
	stored := len(snap.Messages)
	added := 0
	for i, m := range msgs {
		if i < stored {
			if !sameMessage(snap.Messages[i], m) {
				return 0, fmt.Errorf("ingest: message %d diverges from stored %s", i+1, snap.Messages[i].Ref)
			}
			continue
		}
		if _, err := s.st.AppendMessageWithIntent(ctx, s.sid, store.NewMessage{
			Role: m.Role, Kind: m.Kind, ToolName: m.ToolName,
			ToolCallID: m.ToolCallID, Text: m.Text, Tokens: m.Tokens,
		}); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// Plan is the per-turn result: the trigger decision, the visible window and the
// recall hints for this turn (spec §13.2). Hints are ephemeral: they are
// returned for the proxy to inject, and also logged as 'recall_hint' events.
type Plan struct {
	Decision kernel.Decision
	LowWater int
	Visible  []kernel.Message
	Hints    []kernel.RecallHint
	// Before is the trigger state before this Plan, and After the state it wrote.
	// DeferNudge uses them to undo the change without losing a later one.
	Before kernel.TriggerState
	After  kernel.TriggerState
}

// Plan updates the low-water mark for tokenCount, asks the kernel whether to
// nudge, persists the trigger state and renders the visible window.
func (s *Session) Plan(ctx context.Context, tokenCount int) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return Plan{}, err
	}
	trig, err := s.st.GetTrigger(ctx, s.sid)
	if err != nil {
		return Plan{}, err
	}
	before := trig
	trig.LowWater = kernel.UpdateLowWater(trig.LowWater, tokenCount)

	pend := kernel.PendingTokens(snap, s.opts.RecentKeep)
	d := kernel.DecideNudge(kernel.TriggerInput{
		TokenCount:   tokenCount,
		ContextLimit: snap.ContextLimit,
		ReadDensity:  kernel.ReadDensity(snap),
		Pending:      pend,
		Trigger:      trig,
	}, s.opts.Kernel)

	// A forced nudge (Compact) is one-shot: it nudges with tier 1 whatever the
	// usage bands say, as long as tier-1 content is pending, and it is not
	// counted as a refusal, since the model has not seen it yet.
	forced, err := s.st.ForcedNudge(ctx, s.sid)
	if err != nil {
		return Plan{}, err
	}
	forcedNow := false
	if forced {
		if pend[1] > 0 {
			if !d.Nudge {
				d = kernel.Decision{Nudge: true, Tier: 1, Voice: kernel.VoiceFirm, Row: "forced",
					Reason: "compaction requested by the user"}
			}
			forcedNow = true
		}
		if err := s.st.ClearForcedNudge(ctx, s.sid); err != nil {
			return Plan{}, err
		}
	}

	hints := kernel.DetectRecall(snap)
	var turnSeq int64
	for _, m := range snap.Messages {
		if m.Role == kernel.RoleUser && m.Kind == kernel.KindText {
			turnSeq = m.Seq
		}
	}
	for _, h := range hints {
		key := fmt.Sprintf("%d|%s|%s|%s|%s", turnSeq, h.Kind, h.BlockID, h.Ref, h.Path)
		if s.hinted[key] {
			continue
		}
		if s.hinted == nil || len(s.hinted) > 1024 {
			s.hinted = map[string]bool{}
		}
		s.hinted[key] = true
		if err := s.st.AppendEvent(ctx, s.sid, "recall_hint", map[string]any{
			"kind": string(h.Kind), "block": h.BlockID, "ref": h.Ref, "path": h.Path,
		}); err != nil {
			return Plan{}, err
		}
	}

	if d.Nudge {
		if trig.NudgePending && !forcedNow {
			// Previous nudge was shown and the model never compressed: refusal (spec §8.3).
			trig.Refusals++
			if err := s.st.AppendEvent(ctx, s.sid, "refuse", map[string]any{"refusals": trig.Refusals}); err != nil {
				return Plan{}, err
			}
		}
		trig.NudgePending = true
		trig.NudgeShownAt = tokenCount // schema: tokens when the nudge was shown
		switch d.Tier {
		case 2:
			trig.LastShownT2 = tokenCount
		case 3:
			trig.LastShownT3 = tokenCount
		}
	}
	visible := s.truncated(snap, tokenCount)
	if len(visible.Dropped) > 0 || visible.Overflow {
		payload := map[string]any{
			"limit": snap.ContextLimit, "tokens": tokenCount, "dropped": len(visible.Dropped),
			"marker": visible.Marker, "overflow": visible.Overflow,
		}
		if n := len(visible.Dropped); n > 0 {
			payload["from"], payload["to"] = visible.Dropped[0], visible.Dropped[n-1]
		}
		if err := s.st.AppendEvent(ctx, s.sid, "truncate", payload); err != nil {
			return Plan{}, err
		}
	}
	if err := s.st.SetTrigger(ctx, s.sid, trig); err != nil {
		return Plan{}, err
	}
	return Plan{
		Decision: d, LowWater: trig.LowWater, Visible: visible.Window, Hints: hints,
		Before: before, After: trig,
	}, nil
}

// truncated renders the visible window and applies the I8 bound (spec §17.1):
// the window never exceeds the context limit unless its protected content does.
// tokenCount is the calibrated size of the window the proxy measures. It is
// spread over the messages in proportion to their estimates, rounded up, so the
// scaled sum never falls below tokenCount. The untruncated Render still feeds
// tokensAfter, so the trigger low-water does not depend on a per-request valve.
func (s *Session) truncated(snap kernel.Snapshot, tokenCount int) kernel.Truncation {
	raw := kernel.Render(snap)
	rawTotal := int64(0)
	for _, m := range raw {
		rawTotal += int64(m.Tokens)
	}
	if snap.ContextLimit <= 0 || tokenCount <= snap.ContextLimit || rawTotal == 0 {
		return kernel.Truncation{Window: raw}
	}
	tok := func(m kernel.Message) int {
		return int((int64(m.Tokens)*int64(tokenCount) + rawTotal - 1) / rawTotal)
	}
	return kernel.EmergencyTruncateWith(raw, snap.ContextLimit, s.opts.RecentKeep, tok)
}

// ErrNudgeStale reports that the trigger state moved after the Plan, so a
// deferral cannot be undone without also discarding that later change.
var ErrNudgeStale = errors.New("nudge undo: trigger state changed after plan")

// DeferNudge undoes the trigger change of p for a nudge that the cache gate
// deferred (spec §9.3). The nudge never reached the client, so it counts as
// neither shown nor refused. The undo runs under the session lock. It fails with
// ErrNudgeStale when another change came after p; the caller then keeps the nudge.
func (s *Session) DeferNudge(ctx context.Context, p Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.st.GetTrigger(ctx, s.sid)
	if err != nil {
		return err
	}
	if cur != p.After {
		return ErrNudgeStale
	}
	restore := p.Before
	restore.LowWater = p.LowWater
	return s.st.SetTrigger(ctx, s.sid, restore)
}

// DecompressResult reports what a decompress call expanded.
type DecompressResult struct {
	Target      string   // the block that became inactive
	Reactivated []string // its direct sources, visible again
	Redirected  bool     // the requested block was folded; its active ancestor was expanded
}

// Decompress expands one level of folding (spec §13.1, mode "context"): the
// target block is deactivated and its direct sources are reactivated. Nothing
// is deleted, and the change commits only if the invariants hold.
func (s *Session) Decompress(ctx context.Context, id string) (DecompressResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return DecompressResult{}, err
	}
	plan, err := kernel.PlanDecompress(snap, id)
	if err != nil {
		return DecompressResult{}, fmt.Errorf("decompress %q: %w", id, err)
	}
	if err := s.st.ApplyDecompress(ctx, s.sid, plan); err != nil {
		return DecompressResult{}, err
	}
	return DecompressResult{Target: plan.Target, Reactivated: plan.Reactivate, Redirected: plan.Redirected}, nil
}

// CompressRange is one range of a compress tool call (spec §16), by message refs.
// It creates a T1 block.
type CompressRange struct {
	StartRef string
	EndRef   string
	Summary  string
	Topic    string
}

// FoldRange is a T2 or T3 fold (spec §12), by block ids. StartBlock and EndBlock
// name the first and last of a contiguous run of active blocks one tier below
// Tier. Every active block of that tier between them is consumed.
type FoldRange struct {
	Tier       int
	StartBlock string
	EndBlock   string
	Summary    string
	Topic      string
}

// CompressArgs is the validated-shape input of the compress tool.
type CompressArgs struct {
	Ranges       []CompressRange
	Folds        []FoldRange
	CloseIntents []int64 // open intents completed by the summarized ranges (§11.3)
}

// CompressResult reports the blocks written and the token count after the fold.
type CompressResult struct {
	BlockIDs    []string
	TokensAfter int
}

// HandleCompress validates every range and fold against the current snapshot
// and then writes all of them in one store transaction. Invalid input writes
// nothing. Folds are checked by kernel.ApplyTierPlan, so the same invariants
// (I1, I2, I5, I6) guard the in-memory plan and the stored one.
//
// A call that is refused still answers the nudge: the model tried. It clears
// the pending nudge and the refusal count, so a run of rejected ranges (for
// example while a barrier made every range invalid) does not suspend nudges
// as if the model had ignored them (spec §8.3 refusal row).
func (s *Session) HandleCompress(ctx context.Context, args CompressArgs) (CompressResult, error) {
	res, err := s.handleCompress(ctx, args)
	if err != nil {
		if aerr := s.noteCompressAttempt(ctx); aerr != nil {
			return res, errors.Join(err, aerr)
		}
	}
	return res, err
}

// noteCompressAttempt clears the pending nudge and the refusal count after a
// compress call that wrote nothing.
func (s *Session) noteCompressAttempt(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	trig, err := s.st.GetTrigger(ctx, s.sid)
	if err != nil {
		return err
	}
	if !trig.NudgePending && trig.Refusals == 0 {
		return nil
	}
	trig.NudgePending, trig.Refusals = false, 0
	return s.st.SetTrigger(ctx, s.sid, trig)
}

func (s *Session) handleCompress(ctx context.Context, args CompressArgs) (CompressResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(args.Ranges) == 0 && len(args.Folds) == 0 {
		return CompressResult{}, errors.New("compress: no ranges")
	}
	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return CompressResult{}, err
	}

	byRef := make(map[string]kernel.Message, len(snap.Messages))
	for _, m := range snap.Messages {
		byRef[m.Ref] = m
	}
	firstUser := firstUserSeq(snap)
	last := lastSeq(snap)
	pairSeqs := toolSeqsByID(snap.Messages)

	type span struct{ lo, hi int64 }
	var spans []span
	plan := store.CompressPlan{Now: time.Now().Unix(), ParentOf: map[string]string{}}
	nextID := len(snap.Blocks) + 1

	for i, r := range args.Ranges {
		start, ok := byRef[r.StartRef]
		if !ok {
			return CompressResult{}, fmt.Errorf("range %d: unknown startRef %q", i, r.StartRef)
		}
		end, ok := byRef[r.EndRef]
		if !ok {
			return CompressResult{}, fmt.Errorf("range %d: unknown endRef %q", i, r.EndRef)
		}
		if start.Seq > end.Seq {
			return CompressResult{}, fmt.Errorf("range %d: %s after %s", i, r.StartRef, r.EndRef)
		}
		summary := stripForgedMarkers(r.Summary)
		if err := s.validateSummary(summary); err != nil {
			return CompressResult{}, fmt.Errorf("range %d: %w", i, err)
		}
		if hasSystemMessage(snap.Messages, start.Seq, end.Seq) {
			return CompressResult{}, fmt.Errorf("range %d: covers a system message", i)
		}
		if ref, ok := barrierInside(snap.Messages, start.Seq, end.Seq); ok {
			return CompressResult{}, fmt.Errorf("range %d: covers the opaque input item %s, which is never compressed; split the range at it", i, ref)
		}
		if kernel.Carried(start) || kernel.Carried(end) {
			return CompressResult{}, fmt.Errorf("range %d: starts or ends on a client system message; start and end on conversation messages", i)
		}
		if firstUser != 0 && start.Seq <= firstUser && firstUser <= end.Seq {
			return CompressResult{}, fmt.Errorf("range %d: covers the pinned first user message", i)
		}
		if end.Seq > last-int64(s.opts.RecentKeep) {
			return CompressResult{}, fmt.Errorf("range %d: %s is inside the protected recent zone", i, r.EndRef)
		}
		if start.Kind == kernel.KindToolResult {
			return CompressResult{}, fmt.Errorf("range %d: %s starts on a tool_result, splitting its tool pair", i, r.StartRef)
		}
		if end.Kind == kernel.KindToolCall {
			return CompressResult{}, fmt.Errorf("range %d: %s ends on a tool_call, splitting its tool pair", i, r.EndRef)
		}
		if id, split := splitsToolPair(snap.Messages, pairSeqs, start.Seq, end.Seq); split {
			return CompressResult{}, fmt.Errorf("range %d: tool pair %q is split by the range", i, id)
		}
		if overlapsActive(snap.Blocks, start.Seq, end.Seq) {
			return CompressResult{}, fmt.Errorf("range %d: %s..%s overlaps an active block", i, r.StartRef, r.EndRef)
		}
		for _, p := range spans {
			if start.Seq <= p.hi && p.lo <= end.Seq {
				return CompressResult{}, fmt.Errorf("range %d: overlaps another range of the same call", i)
			}
		}

		src := sourceTokens(snap.Messages, start.Seq, end.Seq)
		if src < s.opts.MinCompressRange {
			return CompressResult{}, fmt.Errorf("range %d: covers %d tokens, minimum is %d", i, src, s.opts.MinCompressRange)
		}
		sum := estimateTokens(summary)
		if sum >= src {
			return CompressResult{}, fmt.Errorf("range %d: summary %d tokens is not smaller than source %d", i, sum, src)
		}

		b := store.NewBlock{
			ID: fmt.Sprintf("b%d", nextID), Tier: 1, Topic: r.Topic, Summary: summary,
			StartSeq: start.Seq, EndSeq: end.Seq, SrcTokens: src, SumTokens: sum,
		}
		nextID++
		plan.NewBlocks = append(plan.NewBlocks, b)
		spans = append(spans, span{start.Seq, end.Seq})
	}

	for i, f := range args.Folds {
		if !s.opts.Kernel.TiersEnabled {
			return CompressResult{}, errors.New("fold: tiers are disabled")
		}
		if f.Tier != 2 && f.Tier != 3 {
			return CompressResult{}, fmt.Errorf("fold %d: tier %d is not 2 or 3", i, f.Tier)
		}
		tp, err := resolveFold(snap.Blocks, f)
		if err != nil {
			return CompressResult{}, fmt.Errorf("fold %d: %w", i, err)
		}
		if tp.EndSeq > last-int64(s.opts.RecentKeep) {
			return CompressResult{}, fmt.Errorf("fold %d: %s..%s reaches the protected recent zone", i, kernel.FormatRef(tp.StartSeq), kernel.FormatRef(tp.EndSeq))
		}
		if firstUser != 0 && tp.StartSeq <= firstUser && firstUser <= tp.EndSeq {
			return CompressResult{}, fmt.Errorf("fold %d: covers the pinned first user message", i)
		}
		for _, p := range spans {
			if tp.StartSeq <= p.hi && p.lo <= tp.EndSeq {
				return CompressResult{}, fmt.Errorf("fold %d: overlaps another range of the same call", i)
			}
		}
		// The raw tokens are the messages in the span, whatever the tier of the
		// sources: a T3 fold's sources carry only their summary tokens.
		if raw := sourceTokens(snap.Messages, tp.StartSeq, tp.EndSeq); raw < s.opts.MinCompressRange {
			return CompressResult{}, fmt.Errorf("fold %d: covers %d tokens, minimum is %d", i, raw, s.opts.MinCompressRange)
		}
		summary := stripForgedMarkers(f.Summary)
		if err := s.validateSummary(summary); err != nil {
			return CompressResult{}, fmt.Errorf("fold %d: %w", i, err)
		}
		sum := estimateTokens(summary)
		if sum >= tp.SrcTokens {
			return CompressResult{}, fmt.Errorf("fold %d: summary %d tokens is not smaller than source %d", i, sum, tp.SrcTokens)
		}

		id := fmt.Sprintf("b%d", nextID)
		nb := kernel.Block{ID: id, Topic: f.Topic, Summary: summary, SumTokens: sum}
		if _, err := kernel.ApplyTierPlan(snap.Blocks, tp, nb); err != nil {
			return CompressResult{}, fmt.Errorf("fold %d: %w", i, err)
		}
		nextID++
		plan.NewBlocks = append(plan.NewBlocks, store.NewBlock{
			ID: id, Tier: tp.Tier, Topic: f.Topic, Summary: summary,
			StartSeq: tp.StartSeq, EndSeq: tp.EndSeq, SrcTokens: tp.SrcTokens, SumTokens: sum,
			Sources: tp.Sources,
		})
		for src, parent := range tp.ParentOf(id) {
			plan.Deactivate = append(plan.Deactivate, src)
			plan.ParentOf[src] = parent
		}
		sort.Strings(plan.Deactivate)
		spans = append(spans, span{tp.StartSeq, tp.EndSeq})
	}

	open := make(map[int64]bool, len(snap.Intents))
	for _, in := range snap.Intents {
		if in.Status == "open" {
			open[in.ID] = true
		}
	}
	seenClose := make(map[int64]bool, len(args.CloseIntents))
	for _, id := range args.CloseIntents {
		if !open[id] {
			return CompressResult{}, fmt.Errorf("closeIntents: intent %d is not open", id)
		}
		if seenClose[id] {
			return CompressResult{}, fmt.Errorf("closeIntents: intent %d listed twice", id)
		}
		seenClose[id] = true
		plan.CloseIntents = append(plan.CloseIntents, id)
	}

	plan.TokensAfter = tokensAfter(snap, plan)

	if err := s.st.ApplyCompress(ctx, s.sid, plan); err != nil {
		return CompressResult{}, err
	}
	ids := make([]string, 0, len(plan.NewBlocks))
	for _, b := range plan.NewBlocks {
		ids = append(ids, b.ID)
	}
	return CompressResult{BlockIDs: ids, TokensAfter: plan.TokensAfter}, nil
}

// Recall returns the original content of the message identified by ref,
// including messages folded into a block (spec §13.1 retrieve, simplified).
func (s *Session) Recall(ctx context.Context, ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return "", err
	}
	for _, m := range snap.Messages {
		if m.Ref == ref {
			return m.Text, nil
		}
	}
	return "", fmt.Errorf("recall: unknown ref %q", ref)
}

// validateSummary applies the §10.2 summary rules and, when enabled, the §11.2
// framing check. The summary has its forged markers stripped already.
func (s *Session) validateSummary(summary string) error {
	if err := checkSummary(summary, s.opts.MinSummaryChars); err != nil {
		return err
	}
	if s.opts.FramingCheck {
		return kernel.FramingCheck(summary)
	}
	return nil
}

// checkSummary applies the non-empty and MinSummaryChars rules of spec §10.2 to a
// summary whose forged markers are already stripped.
func checkSummary(summary string, minChars int) error {
	t := strings.TrimSpace(summary)
	if t == "" {
		return errors.New("empty summary")
	}
	if n := utf8.RuneCountInString(t); n < minChars {
		return fmt.Errorf("summary has %d characters, minimum is %d", n, minChars)
	}
	return nil
}

// resolveFold turns a FoldRange into a kernel.TierPlan: the active blocks one
// tier below, from StartBlock to EndBlock, in ledger order. The plan is only
// shaped here. kernel.ApplyTierPlan checks contiguity and the rest.
func resolveFold(blocks []kernel.Block, f FoldRange) (kernel.TierPlan, error) {
	var lo, hi *kernel.Block
	for i := range blocks {
		b := &blocks[i]
		if b.ID == f.StartBlock {
			lo = b
		}
		if b.ID == f.EndBlock {
			hi = b
		}
	}
	if lo == nil || hi == nil {
		return kernel.TierPlan{}, fmt.Errorf("unknown block %q or %q", f.StartBlock, f.EndBlock)
	}
	if lo.StartSeq > hi.StartSeq {
		return kernel.TierPlan{}, fmt.Errorf("%s after %s", f.StartBlock, f.EndBlock)
	}
	tp := kernel.TierPlan{Tier: f.Tier}
	for _, b := range blocks {
		if !b.Active || b.Tier != f.Tier-1 || b.StartSeq < lo.StartSeq || b.EndSeq > hi.EndSeq {
			continue
		}
		tp.Sources = append(tp.Sources, b.ID)
		tp.SrcTokens += b.SumTokens
	}
	if len(tp.Sources) < 2 {
		return kernel.TierPlan{}, fmt.Errorf("%s..%s has %d foldable tier-%d blocks, need at least 2", f.StartBlock, f.EndBlock, len(tp.Sources), f.Tier-1)
	}
	if tp.Sources[0] != f.StartBlock || tp.Sources[len(tp.Sources)-1] != f.EndBlock {
		return kernel.TierPlan{}, fmt.Errorf("%s..%s does not start and end on active tier-%d blocks", f.StartBlock, f.EndBlock, f.Tier-1)
	}
	first, last := blockByID(blocks, tp.Sources[0]), blockByID(blocks, tp.Sources[len(tp.Sources)-1])
	tp.StartSeq, tp.EndSeq = first.StartSeq, last.EndSeq
	return tp, nil
}

func blockByID(blocks []kernel.Block, id string) kernel.Block {
	for _, b := range blocks {
		if b.ID == id {
			return b
		}
	}
	return kernel.Block{}
}

// stripForgedMarkers removes lines that imitate the confirmation marker
// ("📦 ... Compressed ...", spec §10.2): only a compression_events row confirms.
func stripForgedMarkers(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.Contains(l, "📦") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func hasSystemMessage(msgs []kernel.Message, lo, hi int64) bool {
	for _, m := range msgs {
		if m.Seq >= lo && m.Seq <= hi && m.Role == kernel.RoleSystem {
			return true
		}
	}
	return false
}

// barrierInside returns the ref of the first barrier opaque item in [lo, hi].
// Such an item is kept in place, so no range may cover it. Carried items
// (kernel.Carried) may lie inside a range: Render keeps them after the envelope.
func barrierInside(msgs []kernel.Message, lo, hi int64) (string, bool) {
	for _, m := range msgs {
		if m.Seq >= lo && m.Seq <= hi && kernel.Barrier(m) {
			return m.Ref, true
		}
	}
	return "", false
}

func sameMessage(a, b kernel.Message) bool {
	return a.Role == b.Role && a.Kind == b.Kind && a.ToolName == b.ToolName &&
		a.ToolCallID == b.ToolCallID && a.Text == b.Text
}

func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	return (n + charsPerToken - 1) / charsPerToken
}

func lastSeq(snap kernel.Snapshot) int64 {
	if len(snap.Messages) == 0 {
		return 0
	}
	return snap.Messages[len(snap.Messages)-1].Seq
}

func firstUserSeq(snap kernel.Snapshot) int64 {
	for _, m := range snap.Messages {
		if m.Role == kernel.RoleUser && m.Kind == kernel.KindText {
			return m.Seq
		}
	}
	return 0
}

func overlapsActive(blocks []kernel.Block, lo, hi int64) bool {
	for _, b := range blocks {
		if b.Active && lo <= b.EndSeq && b.StartSeq <= hi {
			return true
		}
	}
	return false
}

// toolSeqsByID maps each tool_call_id to the seqs of every tool message that
// carries it (the call and its result).
func toolSeqsByID(msgs []kernel.Message) map[string][]int64 {
	out := map[string][]int64{}
	for _, m := range msgs {
		if m.ToolCallID != "" && (m.Kind == kernel.KindToolCall || m.Kind == kernel.KindToolResult) {
			out[m.ToolCallID] = append(out[m.ToolCallID], m.Seq)
		}
	}
	return out
}

// splitsToolPair reports the first tool_call_id that has a member inside
// [lo, hi] and a member outside it.
func splitsToolPair(msgs []kernel.Message, pairs map[string][]int64, lo, hi int64) (string, bool) {
	for _, m := range msgs {
		if m.Seq < lo || m.Seq > hi || m.ToolCallID == "" {
			continue
		}
		for _, s := range pairs[m.ToolCallID] {
			if s < lo || s > hi {
				return m.ToolCallID, true
			}
		}
	}
	return "", false
}

func sourceTokens(msgs []kernel.Message, lo, hi int64) int {
	total := 0
	for _, m := range msgs {
		// A carried item stays in the window, so a block does not replace it.
		if m.Seq >= lo && m.Seq <= hi && !kernel.Carried(m) {
			total += m.Tokens
		}
	}
	return total
}

// tokensAfter renders the window as it would be with the plan applied (new
// blocks active, consumed blocks inactive, closed intents done) and sums the
// visible tokens. The result is stored as the trigger low-water after a fold.
func tokensAfter(snap kernel.Snapshot, p store.CompressPlan) int {
	hyp := snap
	hyp.Blocks = append([]kernel.Block(nil), snap.Blocks...)
	consumed := make(map[string]bool, len(p.Deactivate))
	for _, id := range p.Deactivate {
		consumed[id] = true
	}
	for i := range hyp.Blocks {
		if consumed[hyp.Blocks[i].ID] {
			hyp.Blocks[i].Active = false
		}
	}
	for _, b := range p.NewBlocks {
		hyp.Blocks = append(hyp.Blocks, kernel.Block{
			ID: b.ID, Tier: b.Tier, Topic: b.Topic, Summary: b.Summary,
			StartSeq: b.StartSeq, EndSeq: b.EndSeq, SrcTokens: b.SrcTokens,
			SumTokens: b.SumTokens, Active: true, Sources: b.Sources,
		})
	}
	closing := make(map[int64]bool, len(p.CloseIntents))
	for _, id := range p.CloseIntents {
		closing[id] = true
	}
	hyp.Intents = append([]kernel.Intent(nil), snap.Intents...)
	for i := range hyp.Intents {
		if closing[hyp.Intents[i].ID] {
			hyp.Intents[i].Status = "done"
		}
	}
	total := 0
	for _, m := range kernel.Render(hyp) {
		total += m.Tokens
	}
	return total
}
