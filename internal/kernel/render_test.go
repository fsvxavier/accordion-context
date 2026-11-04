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

func TestRenderFoldsAndKeepsLastUserLast(t *testing.T) {
	s := Snapshot{Messages: msgs(), Blocks: []Block{{ID: "b1", Tier: 1, Summary: "sum", StartSeq: 2, EndSeq: 3, Active: true}}}
	out := Render(s)
	if out[0].Text != "first" {
		t.Fatalf("first user not pinned: %+v", out[0])
	}
	if !strings.Contains(out[1].Text, `covers="m00002..m00003"`) {
		t.Fatalf("missing envelope: %q", out[1].Text)
	}
	if got := out[len(out)-1].Text; got != "last" {
		t.Fatalf("last user not last: %q", got)
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
