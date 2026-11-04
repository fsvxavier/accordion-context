package kernel

import (
	"strings"
	"testing"
)

func TestFramingCheck(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		reject  bool
	}{
		{"past tense passes", "O usuário pediu o parser. O agente implementou o parser em parser.go:42.", false},
		{"pt imperative rejected", "Implemente o parser. Adicione testes. Corrija o bug.", true},
		{"pt non-ascii opener", "Faça o deploy. Verifique os logs.", true},
		{"en imperative rejected", "Fix the bug. Add tests. Run go test.", true},
		{"en past passes", "The user asked for a parser. The agent added tests in parser_test.go.", false},
		{"minority imperative passes", "O usuário pediu X. O agente implementou Y em foo.go:42. Adicione Z.", false},
		{"majority imperative rejected", "Adicione X. Remova Y. O agente leu Z.", true},
		{"bullets are sentences", "- Implement the cache\n- Add a test\n- The agent read the log", true},
		{"ref marker stripped", "[m00042] Remove the old file. [m00043] Update the readme.", true},
		{"file path does not split", "Editou foo.go:42 para corrigir o erro.", false},
		{"numbered list", "1. Install the tool\n2. Configure it", true},
		{"empty passes", "", false},
		{"no words passes", "42 ... !!!", false},
	}
	for _, c := range cases {
		err := FramingCheck(c.summary)
		if (err != nil) != c.reject {
			t.Errorf("%s: reject=%v err=%v", c.name, c.reject, err)
		}
	}
}

func TestFramingErrorAsksForPastTense(t *testing.T) {
	err := FramingCheck("Implement it. Add tests.")
	if err == nil {
		t.Fatal("want rejection")
	}
	for _, want := range []string{"past tense", "third person"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error must mention %q: %v", want, err)
		}
	}
}
