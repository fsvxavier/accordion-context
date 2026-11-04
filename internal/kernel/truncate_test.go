package kernel

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func tokOf(m Message) int { return m.Tokens }

func sumTok(ms []Message) int {
	n := 0
	for _, m := range ms {
		n += m.Tokens
	}
	return n
}

func refsOf(ms []Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Ref+"|"+m.Text)
	}
	return out
}

func mk(seq int64, role Role, kind Kind, tokens int, text string) Message {
	return Message{Seq: seq, Ref: FormatRef(seq), Role: role, Kind: kind, Tokens: tokens, Text: text}
}

// window: system, pinned user, one history envelope, then raw turns.
func sampleWindow() []Message {
	return []Message{
		{Ref: "m00000", Role: RoleSystem, Kind: KindText, Tokens: 10, Text: "SYSTEM"},
		mk(1, RoleUser, KindText, 20, "first"),
		{Role: RoleUser, Kind: KindText, Tokens: 30, Text: "<history block=\"b1\">envelope</history>"},
		mk(2, RoleAssistant, KindText, 100, "old answer"),
		mk(3, RoleUser, KindText, 100, "old question"),
		mk(4, RoleAssistant, KindToolCall, 100, "call"),
		mk(5, RoleTool, KindToolResult, 100, "result"),
		mk(6, RoleAssistant, KindText, 100, "after tool"),
		mk(7, RoleUser, KindText, 50, "last user"),
		mk(8, RoleAssistant, KindText, 60, "recent a"),
		mk(9, RoleAssistant, KindText, 60, "recent b"),
	}
}

func withToolIDs(ms []Message) []Message {
	for i := range ms {
		if ms[i].Seq == 4 || ms[i].Seq == 5 {
			ms[i].ToolCallID = "call-1"
			ms[i].ToolName = "read"
		}
	}
	return ms
}

func TestEmergencyTruncateFitsIsUnchanged(t *testing.T) {
	w := sampleWindow()
	tr := EmergencyTruncateWith(w, sumTok(w), DefaultKeepRecent, tokOf)
	if tr.Marker || tr.Overflow || len(tr.Dropped) != 0 {
		t.Fatalf("fits: unexpected truncation %+v", tr)
	}
	if fmt.Sprint(refsOf(tr.Window)) != fmt.Sprint(refsOf(w)) {
		t.Fatal("fits: window changed")
	}
}

func TestEmergencyTruncateDropsOldestWithMarker(t *testing.T) {
	w := withToolIDs(sampleWindow())
	limit := sumTok(w) - 150
	tr := EmergencyTruncateWith(w, limit, DefaultKeepRecent, tokOf)
	if !tr.Marker || tr.Overflow {
		t.Fatalf("want marker, no overflow: %+v", tr)
	}
	if got := sumTok(tr.Window); got > limit {
		t.Fatalf("window %d tokens exceeds limit %d", got, limit)
	}
	if tr.Dropped[0] != "m00002" {
		t.Fatalf("oldest raw message must go first, dropped %v", tr.Dropped)
	}
	var marker *Message
	for i := range tr.Window {
		if strings.HasPrefix(tr.Window[i].Text, `<history truncated="true"`) {
			marker = &tr.Window[i]
		}
	}
	if marker == nil {
		t.Fatal("marker missing")
	}
	if !strings.Contains(marker.Text, `covers="m00002..m0000`) || !strings.HasSuffix(marker.Text, "</history>") {
		t.Fatalf("marker text: %q", marker.Text)
	}
	if marker.Role != RoleUser || marker.Kind != KindText || marker.Ref != "" {
		t.Fatalf("marker envelope: %+v", marker)
	}
	// Pinned first user, system and the envelope survive; the originals are not in the window.
	if tr.Window[0].Text != "SYSTEM" || tr.Window[1].Text != "first" {
		t.Fatalf("protected head lost: %v", texts(tr.Window))
	}
}

func TestEmergencyTruncateNeverSplitsToolPair(t *testing.T) {
	w := withToolIDs(sampleWindow())
	// Choose a limit so that the cut lands on the pair (call at 4, result at 5).
	for limit := 0; limit <= sumTok(w); limit += 7 {
		tr := EmergencyTruncateWith(w, limit, DefaultKeepRecent, tokOf)
		have := map[string]int{}
		for _, m := range tr.Window {
			if m.ToolCallID != "" {
				have[m.ToolCallID]++
			}
		}
		if have["call-1"] == 1 {
			t.Fatalf("limit %d: tool pair split: %v", limit, texts(tr.Window))
		}
	}
}

func TestEmergencyTruncateKeepsLastUserTurnAndRecentZone(t *testing.T) {
	w := sampleWindow()
	tr := EmergencyTruncateWith(w, 200, DefaultKeepRecent, tokOf)
	got := texts(tr.Window)
	for _, want := range []string{"last user", "recent a", "recent b"} {
		if !contains(got, want) {
			t.Fatalf("protected %q dropped: %v", want, got)
		}
	}
}

func TestEmergencyTruncateOverflowReturnsProtectedOnly(t *testing.T) {
	w := sampleWindow()
	// Protected content alone: system 10 + first 20 + envelope 30 + last user 50 + recent 120 = 230.
	tr := EmergencyTruncateWith(w, 200, DefaultKeepRecent, tokOf)
	if !tr.Overflow || tr.Marker {
		t.Fatalf("want overflow without marker: %+v", tr)
	}
	if sumTok(tr.Window) != 230 {
		t.Fatalf("overflow must hold protected content only, got %v tokens: %v", sumTok(tr.Window), texts(tr.Window))
	}
}

func TestEmergencyTruncateMarkerDoesNotFitIsNotOverflow(t *testing.T) {
	// Protected content is 230 tokens; a limit of 230 leaves no room for the marker.
	w := sampleWindow()
	tr := EmergencyTruncateWith(w, 230, DefaultKeepRecent, tokOf)
	if tr.Overflow {
		t.Fatalf("protected content fits: overflow must be false: %+v", tr)
	}
	if tr.Marker {
		t.Fatal("marker must not be added when it would push the window over the limit")
	}
	if sumTok(tr.Window) > 230 {
		t.Fatalf("window %d exceeds limit 230", sumTok(tr.Window))
	}
}

func TestEmergencyTruncateIdempotent(t *testing.T) {
	w := withToolIDs(sampleWindow())
	for _, limit := range []int{1, 100, 200, 300, 500} {
		once := EmergencyTruncateWith(w, limit, DefaultKeepRecent, tokOf)
		twice := EmergencyTruncateWith(once.Window, limit, DefaultKeepRecent, tokOf)
		if fmt.Sprint(refsOf(once.Window)) != fmt.Sprint(refsOf(twice.Window)) {
			t.Fatalf("limit %d: not idempotent", limit)
		}
		if len(twice.Dropped) != 0 {
			t.Fatalf("limit %d: second pass dropped %v", limit, twice.Dropped)
		}
	}
}

func TestEmergencyTruncateParallelCallsStayTogether(t *testing.T) {
	w := []Message{
		mk(1, RoleUser, KindText, 10, "first"),
		mk(2, RoleAssistant, KindToolCall, 100, "call a"),
		mk(3, RoleAssistant, KindToolCall, 100, "call b"),
		mk(4, RoleTool, KindToolResult, 100, "result b"),
		mk(5, RoleTool, KindToolResult, 100, "result a"),
		mk(6, RoleUser, KindText, 10, "last"),
	}
	w[1].ToolCallID, w[4].ToolCallID = "a", "a"
	w[2].ToolCallID, w[3].ToolCallID = "b", "b"
	tr := EmergencyTruncateWith(w, 150, 1, tokOf)
	have := map[string]int{}
	for _, m := range tr.Window {
		if m.ToolCallID != "" {
			have[m.ToolCallID]++
		}
	}
	for id, n := range have {
		if n != 2 {
			t.Fatalf("pair %q split: %v", id, texts(tr.Window))
		}
	}
}

func TestRenderWithLimitUsesRender(t *testing.T) {
	s := Snapshot{Messages: msgs()}
	out := RenderWithLimit(s, 1_000_000, func(m Message) int { return 1 })
	if fmt.Sprint(refsOf(out)) != fmt.Sprint(refsOf(Render(s))) {
		t.Fatal("RenderWithLimit with a generous limit must equal Render")
	}
	if got := RenderWithLimit(s, 0, func(m Message) int { return 1 }); len(got) != len(Render(s)) {
		t.Fatal("limit 0 disables truncation")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// genWindow builds a random window: system, a pinned first user message, history
// envelopes (no ref), then raw turns with tool pairs that may be parallel.
func genWindow(t *rapid.T) []Message {
	var w []Message
	seq := int64(0)
	next := func(role Role, kind Kind, text string, tool bool) Message {
		seq++
		m := Message{Seq: seq, Ref: FormatRef(seq), Role: role, Kind: kind, Text: text, Tokens: rapid.IntRange(0, 400).Draw(t, "tokens")}
		_ = tool
		return m
	}
	if rapid.Bool().Draw(t, "hasSystem") {
		w = append(w, Message{Ref: "m00000", Role: RoleSystem, Kind: KindText, Tokens: rapid.IntRange(0, 200).Draw(t, "systok"), Text: "SYSTEM"})
	}
	w = append(w, next(RoleUser, KindText, "pinned", false))
	for i := rapid.IntRange(0, 2).Draw(t, "envelopes"); i > 0; i-- {
		w = append(w, Message{Role: RoleUser, Kind: KindText, Tokens: rapid.IntRange(0, 100).Draw(t, "envtok"), Text: "<history>env</history>"})
	}
	groups := rapid.IntRange(0, 12).Draw(t, "groups")
	pairID := 0
	for g := 0; g < groups; g++ {
		switch rapid.IntRange(0, 2).Draw(t, "kind") {
		case 0:
			role := RoleAssistant
			if rapid.Bool().Draw(t, "userTurn") {
				role = RoleUser
			}
			w = append(w, next(role, KindText, fmt.Sprintf("text %d", g), false))
		default:
			// One or two calls, then their results in order (parallel when two).
			pairID++
			n := rapid.IntRange(1, 2).Draw(t, "calls")
			ids := make([]string, n)
			for k := 0; k < n; k++ {
				ids[k] = fmt.Sprintf("t%d-%d", pairID, k)
				c := next(RoleAssistant, KindToolCall, "call", false)
				c.ToolCallID, c.ToolName = ids[k], "read"
				w = append(w, c)
			}
			for k := 0; k < n; k++ {
				r := next(RoleTool, KindToolResult, "result", false)
				r.ToolCallID, r.ToolName = ids[k], "read"
				w = append(w, r)
			}
		}
	}
	// The last message is a user or assistant text, so a recent zone exists.
	w = append(w, next(RoleUser, KindText, "last user", false))
	return w
}

func TestEmergencyTruncateProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := genWindow(t)
		total := sumTok(w)
		limit := rapid.IntRange(1, total+200).Draw(t, "limit")
		keep := rapid.IntRange(0, 5).Draw(t, "keep")

		tr := EmergencyTruncateWith(w, limit, keep, tokOf)
		out := tr.Window

		// I8: within the limit unless the protected content alone exceeds it.
		if !tr.Overflow && sumTok(out) > limit {
			t.Fatalf("I8: %d tokens over limit %d without overflow", sumTok(out), limit)
		}
		if tr.Overflow && (tr.Marker || sumTok(out) <= limit) {
			t.Fatalf("overflow must be the protected content alone, over the limit, no marker: marker=%v tokens=%d limit=%d", tr.Marker, sumTok(out), limit)
		}
		if total > limit && !tr.Overflow && len(tr.Dropped) == 0 {
			t.Fatal("over the limit but nothing dropped")
		}

		// Subsequence: out without the marker is w without the dropped refs, in order.
		var markers int
		var kept []Message
		for _, m := range out {
			if strings.HasPrefix(m.Text, `<history truncated="true"`) && m.Ref == "" {
				markers++
				continue
			}
			kept = append(kept, m)
		}
		if markers > 1 || (markers == 1) != tr.Marker {
			t.Fatalf("markers=%d, flag=%v", markers, tr.Marker)
		}
		dropped := map[string]bool{}
		for _, r := range tr.Dropped {
			dropped[r] = true
		}
		var expect []Message
		for _, m := range w {
			if m.Ref != "" && dropped[m.Ref] {
				continue
			}
			expect = append(expect, m)
		}
		if fmt.Sprint(kept) != fmt.Sprint(expect) {
			t.Fatalf("window is not the input minus dropped messages:\nkept=%v\nexpect=%v", refsOf(kept), refsOf(expect))
		}

		// Dropped messages are raw, and only droppable ones.
		for _, m := range w {
			if !dropped[m.Ref] || m.Ref == "" {
				continue
			}
			if m.Role == RoleSystem || m.Text == "pinned" {
				t.Fatalf("dropped protected message %+v", m)
			}
		}

		// I3: no tool pair is split.
		seen := map[string]int{}
		for _, m := range out {
			if m.ToolCallID != "" {
				seen[m.ToolCallID]++
			}
		}
		for _, m := range w {
			if m.ToolCallID != "" && seen[m.ToolCallID] != 0 && seen[m.ToolCallID] != 2 {
				t.Fatalf("I3: pair %q split", m.ToolCallID)
			}
		}

		// Protected: system, pinned first user, envelopes and the last user turn with everything after it.
		kept2 := map[string]bool{}
		for _, m := range out {
			kept2[m.Ref+"|"+m.Text] = true
		}
		lastUser := -1
		for i, m := range w {
			if m.Ref != "" && m.Role == RoleUser && m.Kind == KindText {
				lastUser = i
			}
		}
		for i, m := range w {
			protected := m.Ref == "" || m.Role == RoleSystem || m.Text == "pinned" || i >= lastUser
			if protected && !kept2[m.Ref+"|"+m.Text] {
				t.Fatalf("protected message dropped: %+v", m)
			}
		}

		// Determinism and idempotence.
		again := EmergencyTruncateWith(w, limit, keep, tokOf)
		if fmt.Sprint(refsOf(again.Window)) != fmt.Sprint(refsOf(out)) {
			t.Fatal("not deterministic")
		}
		twice := EmergencyTruncateWith(out, limit, keep, tokOf)
		if fmt.Sprint(refsOf(twice.Window)) != fmt.Sprint(refsOf(out)) {
			t.Fatalf("not idempotent: %v vs %v", refsOf(twice.Window), refsOf(out))
		}
	})
}
