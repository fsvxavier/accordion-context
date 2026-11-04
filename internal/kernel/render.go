package kernel

import (
	"fmt"
	"sort"
	"strings"
)

// Render builds the visible window (spec §11.1): system messages, pinned first
// user message, history envelopes, intents, raw recent zone, last user message last.
// shortcut: no emergencyTruncate (I8) and no recent-zone protection, arrive with §10.
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

	var firstUser, lastUser *Message
	for i := range s.Messages {
		if s.Messages[i].Role == RoleUser && s.Messages[i].Kind == KindText {
			if firstUser == nil {
				firstUser = &s.Messages[i]
			}
			lastUser = &s.Messages[i]
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
	if t := renderIntents(s.Intents); t != "" {
		out = append(out, Message{Role: RoleUser, Kind: KindText, Text: t})
	}
	for _, m := range s.Messages {
		if m.Role == RoleSystem || covered(m.Seq) || (firstUser != nil && m.Seq == firstUser.Seq) ||
			(lastUser != nil && m.Seq == lastUser.Seq) {
			continue
		}
		out = append(out, m)
	}
	if lastUser != nil && (firstUser == nil || lastUser.Seq != firstUser.Seq) && !covered(lastUser.Seq) {
		out = append(out, *lastUser)
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
			fmt.Fprintf(&b, "[open #%d] %s\n", i.ID, i.Text)
		} else {
			fmt.Fprintf(&b, "[%s #%d] %s\n", i.Status, i.ID, firstLine(i.Text))
		}
	}
	b.WriteString("</intents>")
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
