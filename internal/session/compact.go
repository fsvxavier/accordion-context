package session

import (
	"context"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// CompactOptions tunes a forced compaction.
type CompactOptions struct {
	// MinTokens is the smallest span offered. Zero takes MinCompressRange, the
	// smallest range HandleCompress accepts, so every offered span can be compressed.
	MinTokens int
}

// CompactResult is what a forced compaction offers and whether it nudges.
type CompactResult struct {
	Spans        []kernel.Span // compressible ranges, ledger order
	NudgePending bool          // true when the next Plan nudges
}

// Compact offers the compressible spans of the session and, when there are
// any, arms a forced nudge so the next Plan asks the model to compress them
// (spec §19.4). Without spans nothing is armed: there is nothing to nudge about.
func (s *Session) Compact(ctx context.Context, opts CompactOptions) (CompactResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return CompactResult{}, err
	}
	min := opts.MinTokens
	if min <= 0 {
		min = s.opts.MinCompressRange
	}
	spans := []kernel.Span{}
	for _, sp := range kernel.CompressibleSpans(snap, s.opts.RecentKeep) {
		if sp.Tokens >= min {
			spans = append(spans, sp)
		}
	}
	res := CompactResult{Spans: spans}
	if len(spans) == 0 {
		return res, nil
	}
	if err := s.st.RequestNudge(ctx, s.sid, map[string]any{"spans": SpanPayload(spans)}); err != nil {
		return CompactResult{}, err
	}
	res.NudgePending = true
	return res, nil
}

// SpanPayload is the JSON-ready form of spans, for events and tool results.
func SpanPayload(spans []kernel.Span) []map[string]any {
	out := make([]map[string]any, 0, len(spans))
	for _, sp := range spans {
		out = append(out, map[string]any{"start": sp.Start, "end": sp.End, "tokens": sp.Tokens})
	}
	return out
}
