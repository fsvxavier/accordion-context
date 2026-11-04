package kernel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func intp(v int) *int { return &v }

func TestJSONFold(t *testing.T) {
	long := strings.Repeat("constant-value-", 10)
	tests := []struct {
		name   string
		in     string
		wantOK bool
	}{
		{"constant long field folds", `[{"schema":"` + long + `","id":1},{"schema":"` + long + `","id":2}]`, true},
		{"not json", `not json`, false},
		{"object not array", `{"a":1}`, false},
		{"single element", `[{"schema":"` + long + `","id":1}]`, false},
		{"no constant field", `[{"id":1},{"id":2}]`, false},
		{"empty array", `[]`, false},
		{"does not shrink", `[{"a":1},{"a":1}]`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := JSONFold(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("JSONFold ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

// Lossless: merging _const back into each item must reproduce the input.
func TestJSONFoldRoundTrip(t *testing.T) {
	long := strings.Repeat("x", 100)
	in := `[{"schema":"` + long + `","id":1,"ok":true},{"schema":"` + long + `","id":2,"ok":false}]`
	out, ok := JSONFold(in)
	if !ok {
		t.Fatal("expected fold")
	}
	var folded struct {
		Const map[string]any   `json:"_const"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &folded); err != nil {
		t.Fatalf("output not json: %v", err)
	}
	var rebuilt []map[string]any
	for _, it := range folded.Items {
		m := map[string]any{}
		for k, v := range folded.Const {
			m[k] = v
		}
		for k, v := range it {
			m[k] = v
		}
		rebuilt = append(rebuilt, m)
	}
	var want []map[string]any
	if err := json.Unmarshal([]byte(in), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rebuilt, want) {
		t.Fatalf("round trip mismatch:\n got %v\nwant %v", rebuilt, want)
	}
}

func TestLogSelect(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("ok line\n")
	}
	b.WriteString("--- FAIL: TestReconcile (0.02s)\n")
	b.WriteString("panic: boom\n")
	for i := 0; i < 50; i++ {
		b.WriteString("ok line\n")
	}
	in := b.String()

	out, ok := LogSelect(in)
	if !ok {
		t.Fatal("expected log-select")
	}
	for _, must := range []string{"--- FAIL: TestReconcile (0.02s)", "panic: boom", "lines elided"} {
		if !strings.Contains(out, must) {
			t.Fatalf("output missing %q:\n%s", must, out)
		}
	}
	if len(out) >= len(in) {
		t.Fatalf("output not shorter: %d >= %d", len(out), len(in))
	}
}

func TestLogSelectNoErrorsOrNoGain(t *testing.T) {
	if _, ok := LogSelect("all good\nno problems\n"); ok {
		t.Fatal("no error lines must not fold")
	}
	// error line but output would not be shorter than tiny input
	if _, ok := LogSelect("FAIL"); ok {
		t.Fatal("tiny input must not fold")
	}
}

func TestCodeTrim(t *testing.T) {
	in := "package x\n\n\n// comment\n# hash\nfunc A() {}\n\n\n\nfunc B() {}\n// trailing\n"
	got, ok := CodeTrim(in)
	if !ok {
		t.Fatal("expected trim")
	}
	if got.Lossless {
		t.Fatal("code-trim must be marked lossy")
	}
	if got.Strategy != StrategyCodeTrim {
		t.Fatalf("strategy = %s", got.Strategy)
	}
	if strings.Contains(got.Text, "comment") || strings.Contains(got.Text, "# hash") {
		t.Fatalf("comments kept:\n%s", got.Text)
	}
	if strings.Contains(got.Text, "\n\n\n") {
		t.Fatalf("blank runs not collapsed:\n%q", got.Text)
	}
}

func TestCrushOrder(t *testing.T) {
	long := strings.Repeat("y", 60)
	jsonIn := `[{"s":"` + long + `","id":1},{"s":"` + long + `","id":2}]`
	logIn := strings.Repeat("ok\n", 40) + "FAIL x\n" + strings.Repeat("ok\n", 40)

	tests := []struct {
		name     string
		in       string
		strategy Strategy
		lossless bool
	}{
		{"json first", jsonIn, StrategyJSONFold, true},
		{"log second", logIn, StrategyLogSelect, false},
		{"plain untouched", "hello", StrategyNone, true},
		{"empty", "", StrategyNone, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Crush(tt.in)
			if r.Strategy != tt.strategy || r.Lossless != tt.lossless {
				t.Fatalf("got %s lossless=%v, want %s lossless=%v", r.Strategy, r.Lossless, tt.strategy, tt.lossless)
			}
			if tt.strategy == StrategyNone && r.Text != tt.in {
				t.Fatal("none must return input unchanged")
			}
		})
	}
}

func TestStubDeterministic(t *testing.T) {
	text := "--- FAIL: TestReconcile (0.02s)\n    reconcile_test.go:88: expected 3 matches, got 2\nok 1\nok 2\nok 3\nok 4\nFAIL github.com/x/acerto/internal/match 0.412s\n"
	in := StubInput{Ref: "m00451", Tool: "bash", Cmd: "go test ./...", Exit: intp(1), Text: text, Tokens: 38200}
	a, b := Stub(in), Stub(in)
	if a != b {
		t.Fatal("stub must be deterministic")
	}
	for _, must := range []string{
		"[stored m00451 · tool=bash · 38.2K tokens · exit=1]",
		"cmd: go test ./...",
		"head:",
		"tail:",
		"errors (2):",
		`→ retrieve("m00451") para o conteúdo completo`,
	} {
		if !strings.Contains(a, must) {
			t.Fatalf("stub missing %q:\n%s", must, a)
		}
	}
}

func TestStubEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		contains []string
		absent   []string
	}{
		{"empty text", "", []string{"head:", "[stored m1 · tool=t · 0 tokens]"}, []string{"tail:", "errors ("}},
		{"no errors omits line", "a\nb\n", []string{"head:"}, []string{"errors ("}},
		{"no ellipsis for short", "a\nb\nc\nd\n", []string{"tail:"}, []string{"  ...\n"}},
		{"long truncates errors", "FAIL " + strings.Repeat("é", 200), []string{"errors (1): FAIL ", "…"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := StubInput{Ref: "m1", Tool: "t", Text: tt.text, Tokens: 0}
			out := Stub(in)
			for _, c := range tt.contains {
				if !strings.Contains(out, c) {
					t.Fatalf("missing %q in:\n%s", c, out)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(out, a) {
					t.Fatalf("unexpected %q in:\n%s", a, out)
				}
			}
		})
	}
}

func TestReduce(t *testing.T) {
	huge := strings.Repeat("line of tool output here\n", 2000) // ~12k tokens
	small := "short output"
	fold := `[{"s":"` + strings.Repeat("z", 5000) + `","id":1},{"s":"` + strings.Repeat("z", 5000) + `","id":2}]`
	logIn := strings.Repeat("ok\n", 40) + "FAIL x\n" + strings.Repeat("ok\n", 40)

	tests := []struct {
		name       string
		in         string
		threshold  int
		wantStored bool
		wantPrefix string
	}{
		{"small inline", small, 0, false, small},
		{"huge stored as stub", huge, 0, true, "[stored m00451 · tool=bash"},
		{"fold brings under threshold inline", fold, 0, false, `{"_const"`},
		{"zero threshold uses default", huge, 0, true, "[stored"},
		{"explicit low threshold stores", small, 1, true, "[stored m00451 · tool=bash"},
		{"lossy log-select inline is still stored", logIn, 0, true, "[... "},
		{"empty inline", "", 0, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Reduce(StubInput{Ref: "m00451", Tool: "bash", Text: tt.in}, tt.threshold)
			if r.Stored != tt.wantStored {
				t.Fatalf("Stored = %v, want %v", r.Stored, tt.wantStored)
			}
			if !strings.HasPrefix(r.Visible, tt.wantPrefix) {
				t.Fatalf("Visible prefix mismatch:\n%.200s", r.Visible)
			}
		})
	}
}

func TestFormatTokens(t *testing.T) {
	tests := map[int]string{
		0:     "0 tokens",
		999:   "999 tokens",
		1000:  "1.0K tokens",
		38200: "38.2K tokens",
	}
	for n, want := range tests {
		if got := FormatTokens(n); got != want {
			t.Fatalf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}
