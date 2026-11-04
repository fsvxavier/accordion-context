package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The fake model is dialect-neutral: the Anthropic and OpenAI parsers both turn
// a request into nmsg values, and decide works on those only.

// nmsg is one message of a request, reduced to what the fake model reads.
type nmsg struct {
	role    string   // user, assistant or tool (system messages are dropped)
	texts   []string // text parts, with their [mNNNNN] label when the proxy added one
	calls   []ncall  // tool calls of an assistant message
	results []nres   // tool results (Anthropic tool_result, OpenAI role tool)
}

type ncall struct {
	id   string
	name string
	args string // the arguments as sent: a JSON object
}

type nres struct {
	id      string
	content string
}

// action is what the fake model answers: text, or one tool call.
type action struct {
	text  string
	name  string         // tool name when the answer is a call
	input map[string]any // tool arguments
}

var (
	reLabel     = regexp.MustCompile(`^\[(m\d{5})\] `)
	reSpan      = regexp.MustCompile(`(m\d{5})\.\.(m\d{5}) \((\d+) tok\)`)
	reFact      = regexp.MustCompile(`FACT-\d{4}`)
	reTurn      = regexp.MustCompile(`^Turn (\d+):`)
	reQuestion  = regexp.MustCompile(`^Question: .*?(FACT-\d{4})`)
	reValue     = regexp.MustCompile(`VALUE\[(FACT-\d{4})\]=([0-9a-f]+)`)
	reHistory   = regexp.MustCompile(`(?s)<history block="(b\d+)"[^>]*>(.*?)</history>`)
	reSpanCount = regexp.MustCompile(`(?s)<context_nudge>`)
)

// stripLabel removes a leading [mNNNNN] label.
func stripLabel(s string) string {
	return reLabel.ReplaceAllString(s, "")
}

// factValue is the value the fixture writes for a fact. The test knows it
// independently of the proxy, so a returned value proves the raw text came back.
func factValue(n int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("value-%d", n)))
	return hex.EncodeToString(sum[:8])
}

// decide is the model policy. It answers a nudge with a compress call over the
// first advertised span, reads a module when a turn asks for it, and recovers a
// folded fact with decompress when a question needs it.
func decide(msgs []nmsg, tools map[string]bool) action {
	if len(msgs) == 0 {
		return action{text: "Ready."}
	}
	last := msgs[len(msgs)-1]
	if tools["compress"] && last.role == "user" && hasNudge(last) {
		if spans := parseSpans(last); len(spans) > 0 {
			sp := spans[0]
			return action{name: "compress", input: map[string]any{"ranges": []map[string]any{{
				"startRef": sp[0], "endRef": sp[1],
				"summary": fmt.Sprintf("Summarized %s..%s in past tense: the agent read module files and "+
					"recorded %s; the raw values stay recoverable through decompress.",
					sp[0], sp[1], strings.Join(rangeFacts(msgs, sp[0], sp[1]), ", ")),
				"topic": "module reads",
			}}}}
		}
	}

	// The turn is the newest user text. The window may place it after the rounds it
	// started (the last user message is rendered last, kernel.Render), so the policy
	// reads results by content, not by position.
	ti, turn := -1, ""
	for i := len(msgs) - 1; i >= 0 && ti < 0; i-- {
		if msgs[i].role != "user" {
			continue
		}
		for _, t := range msgs[i].texts {
			s := stripLabel(t)
			if strings.HasPrefix(s, "Turn ") || strings.HasPrefix(s, "Question:") {
				ti, turn = i, s
				break
			}
		}
	}
	if ti < 0 {
		return action{text: "Ready."}
	}

	if strings.HasPrefix(turn, "Question:") {
		m := reQuestion.FindStringSubmatch(turn)
		if m == nil {
			return action{text: "I need a FACT id."}
		}
		fact := m[1]
		if v, ok := findValue(msgs, fact); ok {
			return action{text: fmt.Sprintf("The value recorded for %s is %s.", fact, v)}
		}
		if tools["decompress"] {
			if id := findBlock(msgs, fact); id != "" {
				if expandedIn(msgs, id) {
					return action{text: fmt.Sprintf("Block %s is expanded; the original text returns on the next request.", id)}
				}
				return action{name: "decompress", input: map[string]any{"blockId": id, "mode": "context"}}
			}
		}
		return action{text: fmt.Sprintf("No value for %s is available in the window.", fact)}
	}

	n, _ := strconv.Atoi(reTurn.FindStringSubmatch(turn)[1])
	marker := fmt.Sprintf("// src/mod%03d.go", n)
	for _, mm := range msgs {
		for _, r := range mm.results {
			if strings.Contains(r.content, marker) {
				return action{text: fmt.Sprintf("Recorded %s from module %d.", firstFact(r.content), n)}
			}
		}
	}
	return action{name: "read_file", input: map[string]any{"path": fmt.Sprintf("src/mod%03d.go", n)}}
}

// expandedIn reports whether a decompress result for block id is in the window.
func expandedIn(msgs []nmsg, id string) bool {
	for _, m := range msgs {
		for _, r := range m.results {
			if strings.HasPrefix(r.content, "Expanded "+id+" ") {
				return true
			}
		}
	}
	return false
}

func hasNudge(m nmsg) bool {
	for _, t := range m.texts {
		if reSpanCount.MatchString(t) {
			return true
		}
	}
	return false
}

// parseSpans returns the [start, end] refs of the spans the nudge lists.
func parseSpans(m nmsg) [][2]string {
	var out [][2]string
	for _, t := range m.texts {
		for _, s := range reSpan.FindAllStringSubmatch(t, -1) {
			out = append(out, [2]string{s[1], s[2]})
		}
	}
	return out
}

// rangeFacts lists the facts of the raw values whose message ref lies in
// [start, end]. The refs follow the labels in body order; unlabelled tool
// results belong to the last label before them.
func rangeFacts(msgs []nmsg, start, end string) []string {
	s, e := refNum(start), refNum(end)
	cur := int64(-1)
	seen := map[string]bool{}
	var out []string
	add := func(text string) {
		if cur < s || cur > e || !strings.Contains(text, "VALUE[FACT-") {
			return
		}
		for _, m := range reValue.FindAllStringSubmatch(text, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	for _, m := range msgs {
		for _, t := range m.texts {
			if l := reLabel.FindStringSubmatch(t); l != nil {
				cur = refNum(l[1])
			}
			add(t)
		}
		for _, r := range m.results {
			add(r.content)
		}
	}
	return out
}

func refNum(ref string) int64 {
	n, _ := strconv.ParseInt(strings.TrimPrefix(ref, "m"), 10, 64)
	return n
}

// findValue returns the raw value of a fact when the raw text is in the window.
func findValue(msgs []nmsg, fact string) (string, bool) {
	for _, m := range msgs {
		texts := append([]string{}, m.texts...)
		for _, r := range m.results {
			texts = append(texts, r.content)
		}
		for _, t := range texts {
			for _, v := range reValue.FindAllStringSubmatch(t, -1) {
				if v[1] == fact {
					return v[2], true
				}
			}
		}
	}
	return "", false
}

// findBlock returns the id of the history block whose envelope names the fact.
func findBlock(msgs []nmsg, fact string) string {
	for _, m := range msgs {
		for _, t := range m.texts {
			for _, h := range reHistory.FindAllStringSubmatch(t, -1) {
				if strings.Contains(h[2], fact) {
					return h[1]
				}
			}
		}
	}
	return ""
}

// firstFact returns the first fact id in a tool result.
func firstFact(content string) string {
	if f := reFact.FindString(content); f != "" {
		return f
	}
	return "FACT-????"
}

// flattenContent reads a content field that is a string or an array of text parts.
func flattenContent(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}
