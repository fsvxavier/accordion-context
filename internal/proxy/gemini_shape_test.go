package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestGeminiShapeRules(t *testing.T) {
	const nonStream = "/v1beta/models/gemini-2.5-pro:generateContent"
	const stream = "/v1beta/models/gemini-2.5-pro:streamGenerateContent"
	cases := []struct {
		name       string
		path, body string
		noTools    bool
		compress   bool
	}{
		{"compression shape", nonStream, `{"systemInstruction":{"parts":[{"text":"x"}]},"contents":[]}`, true, true},
		{"with generationConfig", nonStream, `{"systemInstruction":{"parts":[{"text":"x"}]},"generationConfig":{"temperature":1},"contents":[]}`, true, false},
		{"with tools", nonStream, `{"systemInstruction":{"parts":[{"text":"x"}]},"tools":[{"functionDeclarations":[]}],"contents":[]}`, false, false},
		{"empty tools array", nonStream, `{"systemInstruction":{"parts":[{"text":"x"}]},"tools":[],"contents":[]}`, true, true},
		{"streaming path", stream, `{"systemInstruction":{"parts":[{"text":"x"}]},"contents":[]}`, true, false},
		{"no systemInstruction", nonStream, `{"contents":[]}`, true, false},
		{"unreadable body", nonStream, `{`, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := geminiHasNoTools([]byte(c.body)); got != c.noTools {
				t.Errorf("geminiHasNoTools = %v, want %v", got, c.noTools)
			}
			if got := geminiCompressionShape(c.path, []byte(c.body)); got != c.compress {
				t.Errorf("geminiCompressionShape = %v, want %v", got, c.compress)
			}
		})
	}
}

func postGemini(t *testing.T, base, path, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
}

// TestKiloHeaderForwardedUnchangedEvenWithTextRulesOff: the deterministic header
// signal does not depend on the text-rule mode.
func TestKiloHeaderForwardedUnchangedEvenWithTextRulesOff(t *testing.T) {
	ps, s, _, up := compactionProxyWith(t, map[string]string{"ACD_CTX_COMPACTION_TEXT_RULES": "off"})
	body := string(anthropicBody(t, "You are kilo.", "hello"))
	postMessages(t, ps.URL, body, map[string]string{"x-kilocode-mode": "compaction"})
	if sent, _ := up.last(); string(sent) != body {
		t.Fatalf("not forwarded unchanged:\n%s", sent)
	}
	if got := signalCount(t, s, "anthropic", signalKiloHeader); got != 1 {
		t.Fatalf("kilo-header counter = %v", got)
	}
	// Another Kilo agent is an ordinary turn.
	before := up.count()
	postMessages(t, ps.URL, body, map[string]string{"x-kilocode-mode": "code"})
	if sent, _ := up.last(); string(sent) == body {
		t.Fatal("a non-compaction Kilo agent was forwarded unchanged")
	}
	if up.count() != before+1 {
		t.Fatalf("upstream calls = %d", up.count()-before)
	}
}

// TestGeminiUtilityCallPassesThrough: a non-streaming generateContent with the
// shape of the compression call is the client's internal call; a call with a
// generationConfig, or a streaming turn, is not.
func TestGeminiUtilityCallPassesThrough(t *testing.T) {
	ps, s, _, up := compactionProxyWith(t, nil)
	body := string(googleBody(t, "You are helpful.", "check for loops"))
	postGemini(t, ps.URL, "/v1beta/models/gemini-2.5-pro:generateContent", body)
	if sent, _ := up.last(); string(sent) != body {
		t.Fatalf("utility call not forwarded unchanged:\n%s", sent)
	}
	if got := signalCount(t, s, "google", signalGeminiUtility); got != 1 {
		t.Fatalf("gemini-utility counter = %v", got)
	}
	withCfg := strings.Replace(body, "{", `{"generationConfig":{"temperature":0},`, 1)
	postGemini(t, ps.URL, "/v1beta/models/gemini-2.5-pro:generateContent", withCfg)
	if sent, _ := up.last(); string(sent) == withCfg {
		t.Fatal("a call with generationConfig was forwarded unchanged")
	}
	postGemini(t, ps.URL, "/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse", body)
	if got := signalCount(t, s, "google", signalGeminiUtility); got != 1 {
		t.Fatalf("an ordinary turn was counted as utility: %v", got)
	}
}
