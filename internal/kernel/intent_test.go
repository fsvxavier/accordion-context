package kernel

import (
	"strings"
	"testing"
)

func TestIntentText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "fix the parser", "fix the parser"},
		{"reminder only", "<system-reminder>\nToday is 2026-11-12.\n</system-reminder>", ""},
		{"several envelopes", "<system-reminder>a</system-reminder>\n<ide_opened_file>f.go</ide_opened_file>", ""},
		{"tool loaded", "Tool loaded.", ""},
		{"selection then request", "<ide_selection>x := 1</ide_selection>\nrename x to count", "rename x to count"},
		{"request then reminder", "commit it\n<system-reminder>auto mode</system-reminder>", "commit it"},
		{"unclosed envelope kept", "<system-reminder>never closed", "<system-reminder>never closed"},
		{"tool loaded inside text kept", "the log said Tool loaded. twice", "the log said Tool loaded. twice"},
		{"subagent report", "Another Claude session sent a message:\n<agent-message from=\"a2dfea\">\n[Subagent hand-back] report\n</agent-message>", ""},
		{"subagent report with note", "Another Claude session sent a message:\n<agent-message from=\"a\">r</agent-message>\n\nThat \"other Claude session\" is an agent working inside this same session — a subagent.", ""},
		{"mid-turn note", "The user sent a new message while you were working:\nverifique o acd-ctx\n\nThis is how Claude Code surfaces messages the user sends mid-turn — within the running turn.", "verifique o acd-ctx"},
		{"task notification", "<task-notification>\n<task-id>b1</task-id>\n</task-notification>", ""},
		{"relayed message keeps the request", "The user sent a new message while you were working:\n<ide_opened_file>a.go</ide_opened_file>\nrun the tests too", "run the tests too"},
		{"continued session summary", "This session is being continued from a previous conversation that ran out of context. The summary below", ""},
		{"resume request", "Your response above was cut off mid-stream. Resume directly from where it stops", ""},
		{"interrupted", "[Request interrupted by user]", ""},
		{"acknowledgement", "mergeado", ""},
		{"acknowledgement with punctuation", "Sim!", ""},
		{"short request kept", "ctx_status", "ctx_status"},
		{"acknowledgement plus request kept", "mergeado, agora atualize o README", "mergeado, agora atualize o README"},
		{"similar tag name kept", "<agent-messages>x</agent-messages>", "<agent-messages>x</agent-messages>"},
		{"pasted content kept", "resolva:\n<pasted_content id=\"b874\">bug</pasted_content>", "resolva:\n<pasted_content id=\"b874\">bug</pasted_content>"},
	}
	for _, c := range cases {
		if got := IntentText(c.in); got != c.want {
			t.Errorf("%s: IntentText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestIntentPreviewIsBounded(t *testing.T) {
	got := IntentPreview("<system-reminder>x</system-reminder>" + strings.Repeat("é", 5000))
	if n := len([]rune(got)); n != maxOpenIntentRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview is %d runes (%q...), want %d plus an ellipsis", n, got[:10], maxOpenIntentRunes)
	}
}

func TestRenderIntentsSkipsHarnessOnly(t *testing.T) {
	out := renderIntents([]Intent{
		{ID: 1, Status: "open", Text: "<system-reminder>env</system-reminder>"},
		{ID: 2, Status: "open", Text: "<ide_opened_file>a.go</ide_opened_file>\nreview a.go"},
		{ID: 3, Status: "done", Text: "Tool loaded."},
	})
	if out != "<intents>\n[open #2] review a.go\n</intents>" {
		t.Fatalf("render:\n%s", out)
	}
	if got := renderIntents([]Intent{{ID: 1, Status: "open", Text: "Tool loaded."}}); got != "" {
		t.Fatalf("harness-only intents rendered %q, want nothing", got)
	}
}
