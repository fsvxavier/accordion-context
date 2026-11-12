package kernel

import "strings"

// harnessEnvelopes are the tags a coding client wraps around text it adds to a
// user turn on its own: environment notes, the file open in the editor, the
// editor selection. They are context, not a request from the user.
var harnessEnvelopes = []string{"system-reminder", "ide_opened_file", "ide_selection"}

// harnessLines are whole lines a client sends as a user turn on its own.
var harnessLines = map[string]bool{"Tool loaded.": true}

// IntentText is the relevant excerpt of a user message for its intent (spec
// §11.3: "o texto literal (ou o trecho relevante)"). It drops the harness
// envelopes and lines above and trims the rest. An empty result means the
// message carries no request from the user, so it opens no intent.
// An unclosed envelope is kept as text.
func IntentText(text string) string {
	for _, tag := range harnessEnvelopes {
		text = stripEnvelope(text, "<"+tag+">", "</"+tag+">")
	}
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if !harnessLines[strings.TrimSpace(l)] {
			kept = append(kept, l)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// IntentPreview is IntentText cut to the length the <intents> block shows for
// an open intent.
func IntentPreview(text string) string {
	return clip(IntentText(text), maxOpenIntentRunes)
}

// stripEnvelope removes every open...close span from s.
func stripEnvelope(s, open, close string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, open)
		if i < 0 {
			break
		}
		j := strings.Index(s[i+len(open):], close)
		if j < 0 {
			break
		}
		b.WriteString(s[:i])
		s = s[i+len(open)+j+len(close):]
	}
	if b.Len() == 0 {
		return s
	}
	b.WriteString(s)
	return b.String()
}
