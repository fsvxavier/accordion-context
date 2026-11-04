package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// DefaultCCRThreshold is the token count above which a tool output is stored
// in CCR and replaced by a stub (spec §14.1, CCRThresholdTokens).
const DefaultCCRThreshold = 4000

// Strategy names the deterministic crush strategy that produced a result.
type Strategy string

const (
	StrategyNone      Strategy = "none"
	StrategyJSONFold  Strategy = "json-fold"
	StrategyLogSelect Strategy = "log-select"
	StrategyCodeTrim  Strategy = "code-trim"
)

// CrushResult is the output of a crush strategy. Lossless is false when the
// strategy dropped content (code-trim); the caller must keep the original in CCR.
type CrushResult struct {
	Text     string
	Strategy Strategy
	Lossless bool
}

// errorLine matches lines that must survive log-select verbatim (spec §14.2).
var errorLine = regexp.MustCompile(`ERROR|FAIL|panic`)

const logContext = 3 // head/tail lines kept around the error lines

// JSONFold folds fields that hold the same value in every element of a JSON
// array of objects into a shared "_const" header. Lossless: each item is
// rebuilt by merging _const with its own fields. ok=false when the input is
// not an array of >=2 objects, has no constant field, or does not get shorter.
func JSONFold(text string) (string, bool) {
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &arr); err != nil || len(arr) < 2 {
		return "", false
	}
	constant := map[string]json.RawMessage{}
	for k, v := range arr[0] {
		same := true
		for _, it := range arr[1:] {
			w, ok := it[k]
			if !ok || !rawEqual(w, v) {
				same = false
				break
			}
		}
		if same {
			constant[k] = v
		}
	}
	if len(constant) == 0 {
		return "", false
	}
	items := make([]map[string]json.RawMessage, len(arr))
	for i, it := range arr {
		m := make(map[string]json.RawMessage, len(it))
		for k, v := range it {
			if _, c := constant[k]; !c {
				m[k] = v
			}
		}
		items[i] = m
	}
	out, err := json.Marshal(struct {
		Const map[string]json.RawMessage   `json:"_const"`
		Items []map[string]json.RawMessage `json:"items"`
	}{constant, items})
	if err != nil || len(out) >= len(text) {
		return "", false
	}
	return string(out), true
}

// LogSelect keeps every ERROR/FAIL/panic line verbatim plus a few lines of
// head and tail, eliding the rest with a marker. It requires at least one
// error line (otherwise there is nothing to guarantee) and ok=false when the
// result is not shorter than the input.
func LogSelect(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	keep := make([]bool, len(lines))
	matches := 0
	for i, l := range lines {
		if errorLine.MatchString(l) {
			matches++
			for j := max(0, i-logContext); j <= min(len(lines)-1, i+logContext); j++ {
				keep[j] = true
			}
		}
	}
	if matches == 0 {
		return "", false
	}
	var b strings.Builder
	elided := 0
	flush := func() {
		if elided > 0 {
			fmt.Fprintf(&b, "[... %d lines elided]\n", elided)
			elided = 0
		}
	}
	for i, l := range lines {
		if !keep[i] {
			elided++
			continue
		}
		flush()
		b.WriteString(l)
		b.WriteByte('\n')
	}
	flush()
	out := strings.TrimSuffix(b.String(), "\n")
	if len(out) >= len(text) {
		return "", false
	}
	return out, true
}

// CodeTrim removes whole-line comments (// and #) and collapses runs of blank
// lines. Lossy: Lossless is false. It is never chosen automatically because
// the kernel cannot tell comment syntax per language; callers opt in.
func CodeTrim(text string) (CrushResult, bool) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
			continue
		}
		if t == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	res := strings.Join(out, "\n")
	if len(res) >= len(text) {
		return CrushResult{}, false
	}
	return CrushResult{Text: res, Strategy: StrategyCodeTrim, Lossless: false}, true
}

// Crush applies the automatic strategies in order (json-fold, then
// log-select) and returns the first that shrinks the text. json-fold is
// lossless; log-select only guarantees error lines (spec §14.2), so its result
// is marked lossy. Otherwise the text is returned unchanged with StrategyNone.
func Crush(text string) CrushResult {
	if s, ok := JSONFold(text); ok {
		return CrushResult{Text: s, Strategy: StrategyJSONFold, Lossless: true}
	}
	if s, ok := LogSelect(text); ok {
		return CrushResult{Text: s, Strategy: StrategyLogSelect, Lossless: false}
	}
	return CrushResult{Text: text, Strategy: StrategyNone, Lossless: true}
}

// StubInput describes a stored tool output for the CCR placeholder.
type StubInput struct {
	Ref    string // "m00451"
	Tool   string
	Cmd    string // optional
	Exit   *int   // optional
	Text   string // original full output
	Tokens int    // estimated tokens of Text
}

const (
	stubHeadLines = 3
	stubTailLines = 3
	stubMaxErrors = 3
	stubErrWidth  = 80
)

// Stub renders the deterministic CCR placeholder (spec §14.1). It is a pure
// function of its input, so the same output always yields the same bytes
// (cache-stable).
func Stub(in StubInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[stored %s · tool=%s · %s", in.Ref, in.Tool, FormatTokens(in.Tokens))
	if in.Exit != nil {
		fmt.Fprintf(&b, " · exit=%d", *in.Exit)
	}
	b.WriteString("]\n")
	if in.Cmd != "" {
		fmt.Fprintf(&b, "cmd: %s\n", in.Cmd)
	}
	lines := strings.Split(in.Text, "\n")
	b.WriteString("head:\n")
	for _, l := range lines[:min(stubHeadLines, len(lines))] {
		fmt.Fprintf(&b, "  %s\n", l)
	}
	if len(lines) > stubHeadLines+stubTailLines {
		b.WriteString("  ...\n")
	}
	tailStart := max(stubHeadLines, len(lines)-stubTailLines)
	if tailStart < len(lines) {
		b.WriteString("tail:\n")
		for _, l := range lines[tailStart:] {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	var errs []string
	nErr := 0
	for _, l := range lines {
		if errorLine.MatchString(l) {
			nErr++
			if len(errs) < stubMaxErrors {
				errs = append(errs, truncateRunes(strings.TrimSpace(l), stubErrWidth))
			}
		}
	}
	if nErr > 0 {
		fmt.Fprintf(&b, "errors (%d): %s\n", nErr, strings.Join(errs, " | "))
	}
	fmt.Fprintf(&b, "→ retrieve(%q) para o conteúdo completo", in.Ref)
	return b.String()
}

// ReduceResult is what a tool output becomes for the visible window.
// Stored=true means the caller must persist the original Text in CCR
// (contents, deduplicated by hash). Visible is then either the stub (the
// crushed text still exceeds the threshold) or the lossy crushed text (the
// crush dropped content, so the original must stay recoverable).
type ReduceResult struct {
	Visible string
	Stored  bool
}

// Reduce crushes text and, if the result still exceeds threshold tokens,
// replaces it with a CCR stub built from the original text. Otherwise the
// crushed text is shown inline; if that crush was lossy, the original is
// still stored. threshold <= 0 uses DefaultCCRThreshold.
func Reduce(in StubInput, threshold int) ReduceResult {
	if threshold <= 0 {
		threshold = DefaultCCRThreshold
	}
	in.Tokens = EstimateTokens(in.Text)
	c := Crush(in.Text)
	if EstimateTokens(c.Text) <= threshold {
		return ReduceResult{Visible: c.Text, Stored: !c.Lossless}
	}
	return ReduceResult{Visible: Stub(in), Stored: true}
}

// FormatTokens renders a token count like "512 tokens" or "38.2K tokens".
func FormatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d tokens", n)
	}
	return fmt.Sprintf("%.1fK tokens", float64(n)/1000)
}

func rawEqual(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
