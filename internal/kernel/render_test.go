package kernel

import (
	"strings"
	"testing"
)

func msgs() []Message {
	return []Message{
		{Seq: 1, Ref: "m00001", Role: RoleUser, Kind: KindText, Text: "first"},
		{Seq: 2, Ref: "m00002", Role: RoleAssistant, Kind: KindText, Text: "a"},
		{Seq: 3, Ref: "m00003", Role: RoleAssistant, Kind: KindText, Text: "b"},
		{Seq: 4, Ref: "m00004", Role: RoleUser, Kind: KindText, Text: "last"},
		{Seq: 5, Ref: "m00005", Role: RoleAssistant, Kind: KindText, Text: "c"},
	}
}

func TestRenderFoldsAndKeepsLastUserInPlace(t *testing.T) {
	s := Snapshot{Messages: msgs(), Blocks: []Block{{ID: "b1", Tier: 1, Summary: "sum", StartSeq: 2, EndSeq: 3, Active: true}}}
	out := Render(s)
	if out[0].Text != "first" {
		t.Fatalf("first user not pinned: %+v", out[0])
	}
	if !strings.Contains(out[1].Text, `covers="m00002..m00003"`) {
		t.Fatalf("missing envelope: %q", out[1].Text)
	}
	if out[2].Text != "last" || out[3].Text != "c" {
		t.Fatalf("last user must keep its natural position before later raw messages: %v", texts(out))
	}
	for _, m := range out {
		if m.Text == "a" || m.Text == "b" {
			t.Fatalf("folded message visible: %q", m.Text)
		}
	}
}

func TestRenderInactiveBlockIgnored(t *testing.T) {
	s := Snapshot{Messages: msgs(), Blocks: []Block{{ID: "b1", Tier: 1, StartSeq: 2, EndSeq: 3}}}
	if n := len(Render(s)); n != 5 {
		t.Fatalf("want 5 visible, got %d", n)
	}
}

func TestRefLabel(t *testing.T) {
	if got := RefLabel(Message{Ref: "m00042", Role: RoleUser, Kind: KindText}); got != "[m00042] " {
		t.Fatalf("label = %q", got)
	}
	if RefLabel(Message{Role: RoleUser, Kind: KindText, Text: "envelope"}) != "" {
		t.Fatal("synthetic message must have no label")
	}
	if RefLabel(Message{Ref: "m00001", Role: RoleSystem, Kind: KindText}) != "" {
		t.Fatal("system message must have no label")
	}
}

func TestRenderKeepsRefsOnRawMessages(t *testing.T) {
	s := Snapshot{Messages: msgs(), Blocks: []Block{{ID: "b1", Tier: 1, Summary: "sum", StartSeq: 2, EndSeq: 3, Active: true}}}
	for _, m := range Render(s) {
		if m.Text == "last" && m.Ref != "m00004" {
			t.Fatalf("raw message lost its ref: %+v", m)
		}
		if strings.HasPrefix(m.Text, "<history") && m.Ref != "" {
			t.Fatalf("envelope must not carry a ref: %+v", m)
		}
	}
}

// spanMsgs builds a ledger of n messages where index i has the given kind.
func spanMsgs(kinds ...Kind) []Message {
	out := make([]Message, len(kinds))
	for i, k := range kinds {
		role := RoleAssistant
		if i == 0 {
			role = RoleUser
		}
		m := Message{Seq: int64(i + 1), Ref: FormatRef(int64(i + 1)), Role: role, Kind: k, Tokens: 100}
		if k == KindToolCall || k == KindToolResult {
			m.ToolCallID = "t" + FormatRef(int64(i+1))
		}
		out[i] = m
	}
	return out
}

func TestCompressibleSpansExcludesProtectedAndCovered(t *testing.T) {
	// 12 text messages, keep=4: candidates are seq 2..8 (seq 1 is the first user message).
	kinds := make([]Kind, 12)
	for i := range kinds {
		kinds[i] = KindText
	}
	s := Snapshot{Messages: spanMsgs(kinds...)}
	got := CompressibleSpans(s, 4)
	if len(got) != 1 || got[0].Start != "m00002" || got[0].End != "m00008" || got[0].Tokens != 700 {
		t.Fatalf("spans = %+v", got)
	}
	// Cover seq 4..5: the candidates split into two spans.
	s.Blocks = []Block{{ID: "b1", Active: true, StartSeq: 4, EndSeq: 5}}
	got = CompressibleSpans(s, 4)
	if len(got) != 2 || got[0].End != "m00003" || got[1].Start != "m00006" {
		t.Fatalf("spans around block = %+v", got)
	}
}

func TestCompressibleSpansNeverSplitsToolPairs(t *testing.T) {
	// seq: 1 user, 2 text, 3 call, 4 result, 5 text, 6..9 text (protected with keep=4).
	kinds := []Kind{KindText, KindText, KindToolCall, KindToolResult, KindText, KindText, KindText, KindText, KindText}
	got := CompressibleSpans(Snapshot{Messages: spanMsgs(kinds...)}, 4)
	for _, sp := range got {
		if sp.StartSeq <= 4 && sp.EndSeq >= 3 && !(sp.StartSeq <= 3 && sp.EndSeq >= 4) {
			t.Fatalf("span %+v splits the pair 3..4", sp)
		}
		if sp.Start == "m00004" {
			t.Fatalf("span starts on a tool_result: %+v", sp)
		}
	}
	// A pair crossing the protected boundary must be excluded entirely.
	kinds = []Kind{KindText, KindText, KindText, KindToolCall, KindToolResult, KindText, KindText}
	for _, sp := range CompressibleSpans(Snapshot{Messages: spanMsgs(kinds...)}, 3) {
		if sp.EndSeq >= 4 && sp.StartSeq <= 5 {
			t.Fatalf("span %+v covers a pair that crosses the protected zone", sp)
		}
	}
}

func TestCompressibleSpansEmpty(t *testing.T) {
	if got := CompressibleSpans(Snapshot{}, 4); got != nil {
		t.Fatalf("empty ledger: %+v", got)
	}
}

func TestRenderIntentsCapsLongText(t *testing.T) {
	long := strings.Repeat("é", 5000)
	out := renderIntents([]Intent{
		{ID: 1, Status: "open", Text: long},
		{ID: 2, Status: "done", Text: long + "\nsecond line"},
		{ID: 3, Status: "open", Text: "short"},
	})
	if n := len([]rune(out)); n > 500+120+200 {
		t.Fatalf("intents block is %d runes, want it bounded", n)
	}
	if !strings.Contains(out, "[open #3] short\n") || !strings.Contains(out, "…") {
		t.Fatalf("unexpected render:\n%s", out)
	}
	if strings.Contains(out, "second line") {
		t.Fatal("done intent must render one line")
	}
}

func texts(ms []Message) []string {
	var o []string
	for _, m := range ms {
		o = append(o, m.Text)
	}
	return o
}

func TestRenderKeepsTurnOrderWithToolRound(t *testing.T) {
	s := Snapshot{Messages: []Message{
		{Seq: 1, Ref: "m00001", Role: RoleUser, Kind: KindText, Text: "first"},
		{Seq: 2, Ref: "m00002", Role: RoleAssistant, Kind: KindText, Text: "a"},
		{Seq: 3, Ref: "m00003", Role: RoleUser, Kind: KindText, Text: "last"},
		{Seq: 4, Ref: "m00004", Role: RoleAssistant, Kind: KindToolCall, ToolCallID: "c1", Text: "call"},
		{Seq: 5, Ref: "m00005", Role: RoleUser, Kind: KindToolResult, ToolCallID: "c1", Text: "res"},
	}}
	got := texts(Render(s))
	want := []string{"first", "a", "last", "call", "res"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func intentsOf(out []Message) string {
	for _, m := range out {
		if strings.HasPrefix(m.Text, "<intents>") {
			return m.Text
		}
	}
	return ""
}

func TestRenderIntentsStableWithoutFold(t *testing.T) {
	ms := msgs()
	in := []Intent{{ID: 1, OriginSeq: 1, Text: "first", Status: "open"}}
	if got := intentsOf(Render(Snapshot{Messages: ms, Intents: in})); got != "" {
		t.Fatalf("no active blocks must render no intents, got %q", got)
	}
	in = append(in, Intent{ID: 2, OriginSeq: 4, Text: "last", Status: "open"})
	if got := intentsOf(Render(Snapshot{Messages: ms, Intents: in})); got != "" {
		t.Fatalf("new user turn without fold must not add intents, got %q", got)
	}
}

func TestRenderIntentsAsOfLastFold(t *testing.T) {
	b := []Block{{ID: "b1", Tier: 1, Summary: "sum", StartSeq: 2, EndSeq: 3, Active: true}}
	in := []Intent{{ID: 1, OriginSeq: 1, Text: "first", Status: "done"}, {ID: 2, OriginSeq: 4, Text: "last", Status: "open"}}
	got := intentsOf(Render(Snapshot{Messages: msgs(), Blocks: b, Intents: in}))
	if !strings.Contains(got, "#1") || strings.Contains(got, "#2") {
		t.Fatalf("intents after fold = %q", got)
	}
	in = append(in, Intent{ID: 3, OriginSeq: 5, Text: "later", Status: "open"})
	if again := intentsOf(Render(Snapshot{Messages: msgs(), Blocks: b, Intents: in})); again != got {
		t.Fatalf("intents changed between folds: %q vs %q", again, got)
	}
}
