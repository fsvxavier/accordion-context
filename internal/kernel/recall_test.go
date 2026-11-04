package kernel

import "testing"

// msg builds a ledger message whose ref is FormatRef(seq).
func msg(seq int64, role Role, kind Kind, tool, text string) Message {
	return Message{Seq: seq, Ref: FormatRef(seq), Role: role, Kind: kind, ToolName: tool, Text: text}
}

func TestDetectRecall(t *testing.T) {
	const readA = `{"file_path":"x.go"}`
	// b1 folds seq 2..4: a Read of x.go, its result, and an assistant note.
	folded := []Message{
		msg(0, RoleSystem, KindText, "", "sys"),
		msg(1, RoleUser, KindText, "", "first"),
		msg(2, RoleAssistant, KindToolCall, "Read", readA),
		msg(3, RoleTool, KindToolResult, "Read", "package x"),
		msg(4, RoleAssistant, KindText, "", "ok"),
	}
	b1 := Block{ID: "b1", Tier: 1, StartSeq: 2, EndSeq: 4, SrcTokens: 900, SumTokens: 100, Active: true}
	// A later user text opens the current turn.
	turn := func(extra ...Message) []Message {
		return append(append([]Message(nil), folded...),
			append([]Message{msg(5, RoleUser, KindText, "", "continue")}, extra...)...)
	}

	tests := []struct {
		name    string
		snap    Snapshot
		want    []RecallKind
		wantBlk string
	}{
		{
			name: "reread of the same path",
			snap: Snapshot{Blocks: []Block{b1}, Messages: turn(
				msg(6, RoleAssistant, KindToolCall, "Read", readA))},
			want: []RecallKind{RecallReread}, wantBlk: "b1",
		},
		{
			name: "same path with different args still matches by path",
			snap: Snapshot{Blocks: []Block{b1}, Messages: turn(
				msg(6, RoleAssistant, KindToolCall, "Read", `{"file_path":"x.go","offset":10}`))},
			want: []RecallKind{RecallReread}, wantBlk: "b1",
		},
		{
			name: "different tool name with same path is not a reread",
			snap: Snapshot{Blocks: []Block{b1}, Messages: turn(
				msg(6, RoleAssistant, KindToolCall, "grep", readA))},
		},
		{
			name: "different path is not a reread",
			snap: Snapshot{Blocks: []Block{b1}, Messages: turn(
				msg(6, RoleAssistant, KindToolCall, "Read", `{"file_path":"y.go"}`))},
		},
		{
			name: "call before the latest user text is not reported",
			snap: Snapshot{Blocks: []Block{b1}, Messages: append(append([]Message(nil), folded...),
				msg(5, RoleUser, KindText, "", "again"),
				msg(6, RoleAssistant, KindText, "", "done"))},
		},
		{
			name: "ref cited in latest assistant text",
			snap: Snapshot{Blocks: []Block{b1}, Messages: turn(
				msg(6, RoleAssistant, KindText, "", "veja m00003 de novo"))},
			want: []RecallKind{RecallRef}, wantBlk: "b1",
		},
		{
			name: "ref cited only in an older assistant text is not reported",
			snap: Snapshot{Blocks: []Block{b1}, Messages: append(append([]Message(nil), folded...),
				msg(5, RoleAssistant, KindText, "", "m00003 antes"),
				msg(6, RoleUser, KindText, "", "obrigado"),
				msg(7, RoleAssistant, KindText, "", "de nada"))},
		},
		{
			name: "ref to a visible message is not reported",
			snap: Snapshot{Blocks: []Block{b1}, Messages: turn(
				msg(6, RoleAssistant, KindText, "", "m00005 ok"))},
		},
		{
			name: "block id of folded block is reported with its active ancestor",
			snap: Snapshot{
				Blocks: []Block{
					{ID: "b1", Tier: 1, StartSeq: 2, EndSeq: 3, SrcTokens: 900, SumTokens: 100, Active: false},
					{ID: "b1x", Tier: 1, StartSeq: 4, EndSeq: 4, SrcTokens: 300, SumTokens: 50, Active: false},
					{ID: "b2", Tier: 2, StartSeq: 2, EndSeq: 4, SrcTokens: 150, SumTokens: 40, Active: true, Sources: []string{"b1", "b1x"}},
				},
				Messages: turn(msg(6, RoleAssistant, KindText, "", "o que tinha em b1?")),
			},
			want: []RecallKind{RecallBlockID}, wantBlk: "b2",
		},
		{
			name: "block id of an active block is not reported",
			snap: Snapshot{Blocks: []Block{b1}, Messages: turn(
				msg(6, RoleAssistant, KindText, "", "b1 segue"))},
		},
		{
			name: "no blocks means no hints",
			snap: Snapshot{Messages: turn(
				msg(6, RoleAssistant, KindToolCall, "Read", readA))},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectRecall(tc.snap)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d hints %+v, want %d kinds %v", len(got), got, len(tc.want), tc.want)
			}
			for i, h := range got {
				if h.Kind != tc.want[i] {
					t.Errorf("hint %d kind = %s, want %s", i, h.Kind, tc.want[i])
				}
				if h.BlockID != tc.wantBlk {
					t.Errorf("hint %d block = %s, want %s", i, h.BlockID, tc.wantBlk)
				}
				if h.Text == "" {
					t.Errorf("hint %d has empty text", i)
				}
			}
		})
	}
}

func TestDetectRecallDedupesAndIsDeterministic(t *testing.T) {
	const readA = `{"file_path":"x.go"}`
	snap := Snapshot{
		Blocks: []Block{{ID: "b1", Tier: 1, StartSeq: 2, EndSeq: 3, SrcTokens: 900, SumTokens: 100, Active: true}},
		Messages: []Message{
			msg(2, RoleAssistant, KindToolCall, "Read", readA),
			msg(3, RoleTool, KindToolResult, "Read", "x"),
			msg(4, RoleUser, KindText, "", "go"),
			msg(5, RoleAssistant, KindToolCall, "Read", readA),
			msg(6, RoleAssistant, KindToolCall, "Read", readA),
			msg(7, RoleAssistant, KindText, "", "m00002 e m00002"),
		},
	}
	first := DetectRecall(snap)
	if len(first) != 2 {
		t.Fatalf("want one reread and one ref, got %+v", first)
	}
	for i := 0; i < 5; i++ {
		again := DetectRecall(snap)
		if len(again) != len(first) {
			t.Fatalf("run %d: length changed", i)
		}
		for j := range again {
			if again[j] != first[j] {
				t.Fatalf("run %d: hint %d differs", i, j)
			}
		}
	}
}

func TestCallSignature(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantPath string
		sameAs   string // another text that must produce the same key
	}{
		{name: "path key ignores other args", text: `{"path":"a"}`, wantPath: "a", sameAs: `{"path":"a","limit":3}`},
		{name: "canonical args ignore key order", text: `{"b":1,"a":2}`, sameAs: `{"a":2, "b":1}`},
		{name: "non-json is trimmed text", text: "  ls -la  ", sameAs: "ls -la"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k1, p1 := callSignature(Message{ToolName: "Read", Text: tc.text})
			k2, _ := callSignature(Message{ToolName: "Read", Text: tc.sameAs})
			if k1 != k2 {
				t.Errorf("keys differ: %q vs %q", k1, k2)
			}
			if p1 != tc.wantPath {
				t.Errorf("path = %q, want %q", p1, tc.wantPath)
			}
		})
	}
}
