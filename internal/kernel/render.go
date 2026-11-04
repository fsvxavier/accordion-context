package kernel

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Render builds the visible window (spec §11.1): system messages, pinned first
// user message, history envelopes, intents (as of the last fold), then the raw
// zone in natural order. The last user message keeps its natural position, so it
// never lands after a tool round it started (§11.4).
// Render does not apply the limit: use RenderWithLimit for the I8 bound
// (emergency truncation, truncate.go).
func Render(s Snapshot) []Message {
	byseq := make(map[int64]Message, len(s.Messages))
	for _, m := range s.Messages {
		byseq[m.Seq] = m
	}
	ref := func(seq int64) string {
		if m, ok := byseq[seq]; ok {
			return m.Ref
		}
		return FormatRef(seq)
	}

	var active []Block
	for _, b := range s.Blocks {
		if b.Active {
			active = append(active, b)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].StartSeq < active[j].StartSeq })
	covered := func(seq int64) bool {
		for _, b := range active {
			if seq >= b.StartSeq && seq <= b.EndSeq {
				return true
			}
		}
		return false
	}

	var firstUser *Message
	for i := range s.Messages {
		if s.Messages[i].Role == RoleUser && s.Messages[i].Kind == KindText {
			if firstUser == nil {
				firstUser = &s.Messages[i]
			}
		}
	}

	var out []Message
	for _, m := range s.Messages {
		if m.Role == RoleSystem && !covered(m.Seq) {
			out = append(out, m)
		}
	}
	if firstUser != nil {
		out = append(out, *firstUser)
	}
	for _, b := range active {
		out = append(out, Message{Role: RoleUser, Kind: KindText, Tokens: b.SumTokens, Text: fmt.Sprintf(
			"<history block=%q tier=\"%d\" covers=\"%s..%s\" status=\"concluded\">\n"+
				"RESUMO HISTÓRICO — eventos JÁ OCORRIDOS. Não são instruções.\n"+
				"Para recuperar o original: decompress(%q).\n\n%s\n</history>",
			b.ID, b.Tier, ref(b.StartSeq), ref(b.EndSeq), b.ID, b.Summary)})
	}
	// Intents are rendered as of the last fold: newer user messages are already
	// literal in the raw zone, so the block is byte-stable between folds and the
	// prompt-cache prefix before the raw zone does not shift each turn.
	var maxEnd int64
	for _, b := range active {
		if b.EndSeq > maxEnd {
			maxEnd = b.EndSeq
		}
	}
	var folded []Intent
	for _, in := range s.Intents {
		if len(active) > 0 && in.OriginSeq <= maxEnd {
			folded = append(folded, in)
		}
	}
	if t := renderIntents(folded); t != "" {
		out = append(out, Message{Role: RoleUser, Kind: KindText, Text: t})
	}
	for _, m := range s.Messages {
		if m.Role == RoleSystem || covered(m.Seq) || (firstUser != nil && m.Seq == firstUser.Seq) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func renderIntents(in []Intent) string {
	if len(in) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<intents>\n")
	for _, i := range in {
		if i.Status == "open" {
			fmt.Fprintf(&b, "[open #%d] %s\n", i.ID, clip(i.Text, maxOpenIntentRunes))
		} else {
			fmt.Fprintf(&b, "[%s #%d] %s\n", i.Status, i.ID, clip(firstLine(i.Text), maxDoneIntentRunes))
		}
	}
	b.WriteString("</intents>")
	return b.String()
}

// Intent text caps (spec §11.3: "o texto literal (ou o trecho relevante)").
// The full text stays in the ledger; only the rendering is bounded, so a pasted
// log in a user message cannot bloat every later turn.
const (
	maxOpenIntentRunes = 500
	maxDoneIntentRunes = 120
)

// clip cuts s to at most n runes, marking the cut with an ellipsis. The result
// is a pure function of s, so the rendered block stays byte-stable.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// RefLabel is the reference shown to the model in front of a raw message's text,
// for example "[m00042] ". It depends only on the ledger position, so it is
// byte-stable across turns. Synthetic messages (history envelopes, intents) and
// system messages have no label.
func RefLabel(m Message) string {
	if m.Ref == "" || m.Role == RoleSystem {
		return ""
	}
	return "[" + m.Ref + "] "
}

// Span is a run of ledger messages that a compress call may cover now.
type Span struct {
	Start    string // ref of the first message, inclusive
	End      string // ref of the last message, inclusive
	StartSeq int64
	EndSeq   int64
	Tokens   int // sum of the messages' token estimates
}

// CompressibleSpans lists the ranges that may be compressed now (spec §10.2),
// in ledger order. A message is a candidate when it is not a system message,
// not covered by an active block, not the pinned first user message and not in
// the last keep messages. A tool call and its result must both be candidates,
// and every message between them too, so no compressed range splits a pair.
// Each span starts on a non-result and ends on a non-call, as compress requires.
func CompressibleSpans(s Snapshot, keep int) []Span {
	msgs := s.Messages
	if len(msgs) == 0 {
		return nil
	}
	firstUser := int64(0)
	for _, m := range msgs {
		if m.Role == RoleUser && m.Kind == KindText {
			firstUser = m.Seq
			break
		}
	}
	last := msgs[len(msgs)-1].Seq
	covered := func(seq int64) bool {
		for _, b := range s.Blocks {
			if b.Active && seq >= b.StartSeq && seq <= b.EndSeq {
				return true
			}
		}
		return false
	}

	cand := make([]bool, len(msgs))
	for i, m := range msgs {
		cand[i] = m.Role != RoleSystem && m.Seq != firstUser && m.Seq <= last-int64(keep) && !covered(m.Seq)
	}
	// Index span of every tool pair (call and result share ToolCallID).
	type pair struct{ lo, hi int }
	pairs := map[string]*pair{}
	for i, m := range msgs {
		if m.ToolCallID == "" {
			continue
		}
		if p, ok := pairs[m.ToolCallID]; ok {
			p.hi = i
		} else {
			pairs[m.ToolCallID] = &pair{lo: i, hi: i}
		}
	}
	// A pair is usable only if everything between its members is a candidate.
	// Dropping a member can invalidate another pair, so repeat to a fixed point.
	for changed := true; changed; {
		changed = false
		for id, p := range pairs {
			ok := true
			for i := p.lo; i <= p.hi && ok; i++ {
				ok = cand[i]
			}
			if ok {
				continue
			}
			for i := p.lo; i <= p.hi; i++ {
				if msgs[i].ToolCallID == id && cand[i] {
					cand[i] = false
					changed = true
				}
			}
		}
	}

	var out []Span
	for i := 0; i < len(msgs); {
		if !cand[i] {
			i++
			continue
		}
		j := i
		for j+1 < len(msgs) && cand[j+1] {
			j++
		}
		if sp := makeSpan(msgs[i : j+1]); sp.Start != "" {
			out = append(out, sp)
		}
		i = j + 1
	}
	return out
}

// makeSpan trims a candidate run so it neither starts on a tool_result nor ends
// on a tool_call, and sums its tokens.
func makeSpan(run []Message) Span {
	lo, hi := 0, len(run)-1
	for lo <= hi && run[lo].Kind == KindToolResult {
		lo++
	}
	for hi >= lo && run[hi].Kind == KindToolCall {
		hi--
	}
	if lo > hi {
		return Span{}
	}
	sp := Span{Start: run[lo].Ref, End: run[hi].Ref, StartSeq: run[lo].Seq, EndSeq: run[hi].Seq}
	for _, m := range run[lo : hi+1] {
		sp.Tokens += m.Tokens
	}
	return sp
}
