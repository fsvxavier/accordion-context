package kernel

import (
	"fmt"
	"slices"
	"sort"
)

// DefaultKeepRecent is the number of trailing raw messages emergency truncation
// never drops when the caller does not pass its own count (spec §10.2
// PreserveRecentMessages).
const DefaultKeepRecent = 3

// truncatedMarkerFmt is the single marker that stands where dropped raw messages
// were. It is a user message with no ref, so later truncations keep it.
const truncatedMarkerFmt = `<history truncated="true" covers="%s..%s">Mensagens antigas removidas da janela; originais recuperáveis com decompress/retrieve.</history>`

// Truncation is the outcome of an emergency truncation (spec §17.1 I8).
type Truncation struct {
	Window []Message // the window to send
	// Dropped lists the refs of the raw messages removed from the window, in
	// ledger order. The originals stay in the ledger.
	Dropped []string
	// Marker reports that the truncated-history marker is in Window.
	Marker bool
	// Overflow reports that the protected content alone (system, pinned first
	// user message, history envelopes, intents, the last user turn and the recent
	// zone, with tool pairs kept whole) exceeds the limit. Window then holds only
	// that protected content, without a marker, and cannot fit.
	Overflow bool
}

// EmergencyTruncate is RenderWithLimit's core applied to an existing window; see
// EmergencyTruncateWith for the rules. It uses DefaultKeepRecent.
func EmergencyTruncate(window []Message, limit int, tokensOf func(Message) int) []Message {
	return EmergencyTruncateWith(window, limit, DefaultKeepRecent, tokensOf).Window
}

// EmergencyTruncateWith drops the oldest raw messages of window until its
// tokens fit limit, keeping one marker in their place. The rules:
//
//   - A message is raw when it has a Ref. Messages without a Ref are synthetic
//     (history envelopes, intents, markers) and are kept, as are system messages.
//   - The first raw user text message is pinned and kept.
//   - Protected tail: everything from the last raw user text message onward, and
//     the last keep raw messages. Tool pairs are kept whole, so a pair that
//     reaches into the tail is protected entirely.
//   - Droppable units are built from the oldest raw messages before the tail.
//     A unit is the smallest contiguous run that closes every tool pair it
//     opens, so no tool_call or tool_result is split (I3).
//   - Units are dropped oldest first and the scan stops at the first unit that
//     cannot be dropped. The marker covers the first and last dropped refs and
//     replaces the first dropped message in position.
//
// A window that already fits is returned unchanged, with no marker. When the
// marker itself does not fit after every unit is gone, the units are dropped
// without a marker, and Overflow reports whether the protected content still
// exceeds limit. limit <= 0 means no limit. The result depends only on the
// input, so it is byte-stable and idempotent: applying it to its own output
// changes nothing.
func EmergencyTruncateWith(window []Message, limit, keep int, tokensOf func(Message) int) Truncation {
	toks := make([]int, len(window))
	total := 0
	for i, m := range window {
		toks[i] = tokensOf(m)
		total += toks[i]
	}
	if limit <= 0 || total <= limit {
		return Truncation{Window: slices.Clone(window)}
	}

	// An opaque item is never dropped: it is not raw, so no unit may contain it.
	raw := func(i int) bool {
		return window[i].Ref != "" && window[i].Role != RoleSystem && window[i].Kind != KindOpaque
	}

	pinned := -1
	lastUser := len(window)
	var rawIdx []int
	for i, m := range window {
		if !raw(i) {
			continue
		}
		rawIdx = append(rawIdx, i)
		if m.Role == RoleUser && m.Kind == KindText {
			if pinned < 0 {
				pinned = i
			}
			lastUser = i
		}
	}
	keepStart := len(window)
	switch {
	case keep <= 0:
	case len(rawIdx) >= keep:
		keepStart = rawIdx[len(rawIdx)-keep]
	default:
		keepStart = 0
	}
	p := min(lastUser, keepStart)

	// Tool pairs: first and last index of every tool_call_id.
	type span struct {
		id     string
		lo, hi int
	}
	pairs := map[string]*span{}
	for i, m := range window {
		if m.ToolCallID == "" {
			continue
		}
		if s, ok := pairs[m.ToolCallID]; ok {
			s.hi = i
		} else {
			pairs[m.ToolCallID] = &span{id: m.ToolCallID, lo: i, hi: i}
		}
	}
	ordered := make([]span, 0, len(pairs))
	for _, s := range pairs {
		ordered = append(ordered, *s)
	}
	// Sorted so the fixed point below does not depend on map order.
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].lo != ordered[j].lo {
			return ordered[i].lo < ordered[j].lo
		}
		return ordered[i].id < ordered[j].id
	})
	// A pair that straddles the tail boundary moves the boundary to its start.
	// Repeat until stable, since a new boundary can straddle another pair.
	for changed := true; changed; {
		changed = false
		for _, s := range ordered {
			if s.lo < p && s.hi >= p {
				p = s.lo
				changed = true
			}
		}
	}

	type unit struct{ lo, hi, tok int }
	var units []unit
	for i := 0; i < p; i++ {
		if !raw(i) || i == pinned {
			continue
		}
		hi := i
		for j := i; j <= hi; j++ {
			if id := window[j].ToolCallID; id != "" {
				if s := pairs[id]; s.hi > hi {
					hi = s.hi
				}
			}
		}
		ok := hi < p
		for j := i; j <= hi && ok; j++ {
			ok = raw(j) && j != pinned
		}
		if !ok {
			break
		}
		u := unit{lo: i, hi: hi}
		for j := i; j <= hi; j++ {
			u.tok += toks[j]
		}
		units = append(units, u)
		i = hi
	}

	if len(units) == 0 {
		return Truncation{Window: slices.Clone(window), Overflow: true}
	}

	allTok := 0
	for _, u := range units {
		allTok += u.tok
	}
	removed := 0
	chosen := 0
	var marker Message
	for k := 1; k <= len(units); k++ {
		removed += units[k-1].tok
		mk := Message{Role: RoleUser, Kind: KindText, Text: fmt.Sprintf(truncatedMarkerFmt,
			window[units[0].lo].Ref, window[units[k-1].hi].Ref)}
		mk.Tokens = EstimateTokens(mk.Text)
		if total-removed+tokensOf(mk) <= limit {
			chosen, marker = k, mk
			break
		}
	}
	drop := units
	if chosen > 0 {
		drop = units[:chosen]
	}

	dropped := make([]bool, len(window))
	var refs []string
	for _, u := range drop {
		for j := u.lo; j <= u.hi; j++ {
			dropped[j] = true
			refs = append(refs, window[j].Ref)
		}
	}
	out := make([]Message, 0, len(window))
	for i := range window {
		if chosen > 0 && i == units[0].lo {
			out = append(out, marker)
		}
		if !dropped[i] {
			out = append(out, window[i])
		}
	}
	rem := total - allTok
	if chosen > 0 {
		rem = total - removed
	}
	return Truncation{
		Window:   out,
		Dropped:  refs,
		Marker:   chosen > 0,
		Overflow: chosen == 0 && rem > limit,
	}
}

// RenderWithLimit is Render followed by emergency truncation (spec §11, I8): the
// visible window never exceeds limit unless the protected content alone does.
// tok is the token count of one message, for example a calibrated estimate.
// limit <= 0 disables truncation.
func RenderWithLimit(s Snapshot, limit int, tok func(Message) int) []Message {
	return RenderTruncated(s, limit, DefaultKeepRecent, tok).Window
}

// RenderTruncated is RenderWithLimit with the recent zone size given by the
// caller and the full Truncation report.
func RenderTruncated(s Snapshot, limit, keep int, tok func(Message) int) Truncation {
	return EmergencyTruncateWith(Render(s), limit, keep, tok)
}
