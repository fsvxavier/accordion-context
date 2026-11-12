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
