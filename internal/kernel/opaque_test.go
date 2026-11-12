package kernel

import (
	"strings"
	"testing"
)

// opaqueLedger is a ledger of text turns with one opaque item at seq 4.
func opaqueLedger() []Message {
	mk := func(seq int64, role Role, kind Kind, text string, tokens int) Message {
		return Message{Seq: seq, Ref: FormatRef(seq), Role: role, Kind: kind, Text: text, Tokens: tokens}
	}
	return []Message{
		mk(1, RoleUser, KindText, "first question", 10),
		mk(2, RoleAssistant, KindText, "answer one", 100),
		mk(3, RoleUser, KindText, "follow up", 100),
		mk(4, RoleAssistant, KindOpaque, "opaque compaction sha256:0123", 50),
		mk(5, RoleAssistant, KindText, "answer two", 100),
		mk(6, RoleUser, KindText, "question three", 100),
		mk(7, RoleAssistant, KindText, "answer three", 100),
		mk(8, RoleUser, KindText, "last question", 10),
	}
}

// TestCompressibleSpansStopAtOpaque: no span covers the opaque item, and the
// spans on either side of it are the usual candidates.
func TestCompressibleSpansStopAtOpaque(t *testing.T) {
	spans := CompressibleSpans(Snapshot{Messages: opaqueLedger()}, 2)
	var got []string
	for _, sp := range spans {
		got = append(got, sp.Start+".."+sp.End)
		if sp.StartSeq <= 4 && 4 <= sp.EndSeq {
			t.Fatalf("span %s..%s covers the opaque item", sp.Start, sp.End)
		}
	}
	want := []string{"m00002..m00003", "m00005..m00006"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("spans = %v, want %v", got, want)
	}
}

// TestRenderPlacesBlocksAroundOpaque: a block that covers messages after an opaque
// item is rendered right after it, and a block before it stays at the top.
func TestRenderPlacesBlocksAroundOpaque(t *testing.T) {
	s := Snapshot{
		Messages: opaqueLedger(),
		Blocks: []Block{
			{ID: "b1", Tier: 1, StartSeq: 2, EndSeq: 2, Active: true, Summary: "before", SumTokens: 5},
			{ID: "b2", Tier: 1, StartSeq: 5, EndSeq: 6, Active: true, Summary: "after", SumTokens: 5},
		},
	}
	out := Render(s)
	var seq []string
	for _, m := range out {
		switch {
		case strings.Contains(m.Text, "<history block=\"b1\""):
			seq = append(seq, "block b1")
		case strings.Contains(m.Text, "<history block=\"b2\""):
			seq = append(seq, "block b2")
		case m.Kind == KindOpaque:
			seq = append(seq, "opaque")
		default:
			seq = append(seq, m.Ref)
		}
	}
	// first user (m1), then the top block b1, then the raw zone: m3, the opaque item,
	// the block b2 that followed it, and the last user message. m2 is covered by b1,
	// and m5 and m6 by b2.
	want := "m00001 block b1 m00003 opaque block b2 m00007 m00008"
	if got := strings.Join(seq, " "); got != want {
		t.Fatalf("render order = %q, want %q", got, want)
	}
}

// TestEmergencyTruncateNeverDropsOpaque: units that would have to pass over the
// opaque item are dropped without it, and the item stays in the window.
func TestEmergencyTruncateNeverDropsOpaque(t *testing.T) {
	window := []Message{
		{Ref: "m00001", Role: RoleUser, Kind: KindText, Text: "first", Tokens: 10},
		{Ref: "m00002", Role: RoleAssistant, Kind: KindText, Text: "a", Tokens: 100},
		{Ref: "m00003", Role: RoleAssistant, Kind: KindOpaque, Text: "opaque", Tokens: 100},
		{Ref: "m00004", Role: RoleAssistant, Kind: KindText, Text: "b", Tokens: 100},
		{Ref: "m00005", Role: RoleUser, Kind: KindText, Text: "last", Tokens: 10},
	}
	tr := EmergencyTruncateWith(window, 150, 1, func(m Message) int { return m.Tokens })
	kept := false
	for _, m := range tr.Window {
		if m.Kind == KindOpaque {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("opaque item dropped: %+v", tr.Window)
	}
	for _, r := range tr.Dropped {
		if r == "m00003" {
			t.Fatal("opaque item listed as dropped")
		}
	}
}

// carriedLedger is opaqueLedger with the opaque item turned into a client
// system message (a carried item) and a second one at seq 6.
func carriedLedger() []Message {
	msgs := opaqueLedger()
	msgs[3].Role = RoleUser
	msgs[5].Role, msgs[5].Kind = RoleUser, KindOpaque
	return msgs
}

// TestCompressibleSpansCrossCarried: carried items do not split spans, never
// bound one and are not counted in its tokens.
func TestCompressibleSpansCrossCarried(t *testing.T) {
	spans := CompressibleSpans(Snapshot{Messages: carriedLedger()}, 2)
	if len(spans) != 1 {
		t.Fatalf("spans = %+v, want one", spans)
	}
	sp := spans[0]
	if sp.Start != "m00002" || sp.End != "m00005" || sp.Tokens != 300 {
		t.Fatalf("span = %s..%s %d tokens, want m00002..m00005 300", sp.Start, sp.End, sp.Tokens)
	}
}

// TestRenderKeepsCarriedAfterEnvelope: a block over carried items renders them
// verbatim after its envelope, and they do not anchor later blocks.
func TestRenderKeepsCarriedAfterEnvelope(t *testing.T) {
	s := Snapshot{
		Messages: carriedLedger(),
		Blocks:   []Block{{ID: "b1", Tier: 1, StartSeq: 2, EndSeq: 6, Active: true, Summary: "s", SumTokens: 5}},
	}
	var got []string
	for _, m := range Render(s) {
		if strings.Contains(m.Text, `<history block="b1"`) {
			got = append(got, "b1")
			continue
		}
		got = append(got, m.Ref)
	}
	want := "m00001 b1 m00004 m00006 m00007 m00008"
	if strings.Join(got, " ") != want {
		t.Fatalf("window = %v, want %s", got, want)
	}
}
