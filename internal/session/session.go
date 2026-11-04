// Package session orchestrates one conversation on top of store (persistence)
// and kernel (pure decisions). It owns no SQL and no protocol parsing.
package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

// Session binds one store session id to the orchestration steps.
type Session struct {
	mu  sync.Mutex
	st  *store.Store
	sid string
	cfg kernel.Config
}

// New wraps an existing store session. The caller creates the session row
// (store.CreateSession) before calling New.
func New(st *store.Store, sid string) *Session {
	return &Session{st: st, sid: sid, cfg: kernel.DefaultConfig()}
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
		if _, err := s.st.AppendMessage(ctx, s.sid, store.NewMessage{
			Role: m.Role, Kind: m.Kind, ToolName: m.ToolName,
			ToolCallID: m.ToolCallID, Text: m.Text, Tokens: m.Tokens,
		}); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// Plan is the per-turn result: the trigger decision and the visible window.
type Plan struct {
	Decision kernel.Decision
	LowWater int
	Visible  []kernel.Message
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
	trig.LowWater = kernel.UpdateLowWater(trig.LowWater, tokenCount)

	d := kernel.DecideNudge(kernel.TriggerInput{
		TokenCount:   tokenCount,
		ContextLimit: snap.ContextLimit,
		ReadDensity:  readDensity(snap),
		Pending:      pending(snap),
		Trigger:      trig,
	}, s.cfg)

	if d.Nudge {
		trig.NudgePending = true
		trig.NudgeShownAt = int(lastSeq(snap))
		switch d.Tier {
		case 2:
			trig.LastShownT2 = tokenCount
		case 3:
			trig.LastShownT3 = tokenCount
		}
	}
	if err := s.st.SetTrigger(ctx, s.sid, trig); err != nil {
		return Plan{}, err
	}
	return Plan{Decision: d, LowWater: trig.LowWater, Visible: kernel.Render(snap)}, nil
}

// CompressRange is one range of a compress tool call (spec §16).
type CompressRange struct {
	StartRef string
	EndRef   string
	Summary  string
	Topic    string
}

// CompressArgs is the validated-shape input of the compress tool.
type CompressArgs struct {
	Ranges []CompressRange
}

// CompressResult reports the blocks written and the token count after the fold.
type CompressResult struct {
	BlockIDs    []string
	TokensAfter int
}

// HandleCompress validates every range against the current snapshot and then
// writes all of them in one store transaction. Invalid input writes nothing.
func (s *Session) HandleCompress(ctx context.Context, args CompressArgs) (CompressResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(args.Ranges) == 0 {
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
		if strings.TrimSpace(r.Summary) == "" {
			return CompressResult{}, fmt.Errorf("range %d: empty summary", i)
		}
		if firstUser != 0 && start.Seq <= firstUser && firstUser <= end.Seq {
			return CompressResult{}, fmt.Errorf("range %d: covers the pinned first user message", i)
		}
		if end.Seq > last-recentKeep {
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
		sum := estimateTokens(r.Summary)
		if sum >= src {
			return CompressResult{}, fmt.Errorf("range %d: summary %d tokens is not smaller than source %d", i, sum, src)
		}

		b := store.NewBlock{
			ID: fmt.Sprintf("b%d", nextID), Tier: 1, Topic: r.Topic, Summary: r.Summary,
			StartSeq: start.Seq, EndSeq: end.Seq, SrcTokens: src, SumTokens: sum,
		}
		nextID++
		plan.NewBlocks = append(plan.NewBlocks, b)
		spans = append(spans, span{start.Seq, end.Seq})
	}

	plan.TokensAfter = tokensAfter(snap, plan.NewBlocks)

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
		if m.Seq >= lo && m.Seq <= hi {
			total += m.Tokens
		}
	}
	return total
}

// covered reports whether seq is inside an active block.
func covered(blocks []kernel.Block, seq int64) bool {
	for _, b := range blocks {
		if b.Active && seq >= b.StartSeq && seq <= b.EndSeq {
			return true
		}
	}
	return false
}

// pending is the compressible tokens per tier. Only tier 1 is produced so far:
// uncovered, non-system, non-pinned messages outside the recent zone.
func pending(snap kernel.Snapshot) map[int]int {
	p := map[int]int{}
	first := firstUserSeq(snap)
	last := lastSeq(snap)
	for _, m := range snap.Messages {
		if m.Role == kernel.RoleSystem || m.Seq == first || m.Seq > last-recentKeep {
			continue
		}
		if covered(snap.Blocks, m.Seq) {
			continue
		}
		p[1] += m.Tokens
	}
	return p
}

// readDensity is the share of tool_result tokens in the whole ledger (spec §8.4 input).
func readDensity(snap kernel.Snapshot) float64 {
	total, reads := 0, 0
	for _, m := range snap.Messages {
		total += m.Tokens
		if m.Kind == kernel.KindToolResult {
			reads += m.Tokens
		}
	}
	if total == 0 {
		return 0
	}
	return float64(reads) / float64(total)
}

// tokensAfter renders the window as it would be with the new blocks active and
// sums the visible tokens (the value stored as trigger low-water after a fold).
func tokensAfter(snap kernel.Snapshot, nb []store.NewBlock) int {
	hyp := snap
	hyp.Blocks = append([]kernel.Block(nil), snap.Blocks...)
	for _, b := range nb {
		hyp.Blocks = append(hyp.Blocks, kernel.Block{
			ID: b.ID, Tier: b.Tier, Topic: b.Topic, Summary: b.Summary,
			StartSeq: b.StartSeq, EndSeq: b.EndSeq, SrcTokens: b.SrcTokens,
			SumTokens: b.SumTokens, Active: true,
		})
	}
	total := 0
	for _, m := range kernel.Render(hyp) {
		total += m.Tokens
	}
	return total
}
