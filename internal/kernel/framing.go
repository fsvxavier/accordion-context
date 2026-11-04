package kernel

import (
	"errors"
	"strings"
	"unicode"
)

// imperativeVerbs lists first words that read as imperative mood when they open
// a sentence, lower case. Only base forms that are not also common past tense
// or noun openers are listed (no "set", "read", "test", "análise"). Extend the
// lists here; FramingCheck reads them at call time.
var imperativeVerbs = map[string]bool{}

func init() {
	for _, list := range [][]string{framingEN, framingPT} {
		for _, w := range list {
			imperativeVerbs[w] = true
		}
	}
}

var framingEN = []string{
	"add", "avoid", "build", "check", "close", "configure", "create", "delete",
	"deploy", "document", "ensure", "fix", "implement", "install", "keep", "make",
	"migrate", "move", "open", "prefer", "rebuild", "refactor", "remove", "rename",
	"replace", "restart", "rewrite", "run", "start", "stop", "try", "update",
	"use", "validate", "verify", "write",
}

var framingPT = []string{
	"abra", "adicione", "altere", "atualize", "configure", "confira", "corrija",
	"crie", "delete", "documente", "evite", "execute", "extraia", "faça", "feche",
	"garanta", "implemente", "importe", "inicie", "instale", "mantenha", "migre",
	"mova", "mostre", "mude", "pare", "prefira", "reconstrua", "refatore",
	"remova", "renomeie", "reinicie", "reescreva", "revise", "rode", "substitua",
	"sincronize", "tente", "use", "valide", "verifique", "escreva", "leia",
}

// framingErr is what the model sees when a summary fails the check (spec §11.2).
var framingErr = errors.New(
	"summary is written in the imperative: most sentences start with a command verb; " +
		"rewrite it in the past tense and third person, recording what the user asked and what the agent did " +
		"(for example: \"the user asked for X; the agent implemented Y in foo.go:42\")")

// FramingCheck is the opt-in heuristic of spec §11.2. It rejects a summary when
// a strict majority of its sentences start with an imperative verb from the
// lists above, which is the wrong mood for a record of past events. The check
// depends on the language, so it is off by default. A summary with no countable
// sentence passes. Sentences end at ., ! or ? followed by whitespace or the end
// of a line, and at each line break. List bullets, numbering, markdown, backticks
// and [mNNNNN] refs are stripped before the first word is read, so "foo.go:42"
// does not split a sentence.
func FramingCheck(summary string) error {
	total, imperative := 0, 0
	for _, line := range strings.Split(summary, "\n") {
		for _, sent := range splitSentences(line) {
			w := firstWord(sent)
			if w == "" {
				continue
			}
			total++
			if imperativeVerbs[w] {
				imperative++
			}
		}
	}
	if total == 0 || imperative*2 <= total {
		return nil
	}
	return framingErr
}

// splitSentences cuts one line at terminators that end a sentence: ., !, or ?
// followed by a space or the end of the line.
func splitSentences(line string) []string {
	var out []string
	start := 0
	rs := []rune(line)
	for i, r := range rs {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			continue
		}
		out = append(out, string(rs[start:i]))
		start = i + 1
	}
	out = append(out, string(rs[start:]))
	return out
}

// firstWord returns the lower-cased first word of a sentence after bullets,
// list numbers, markdown and [mNNNNN] refs are skipped. It returns "" when the
// sentence does not open with a letter (a number, a code fence, a path).
func firstWord(s string) string {
	for {
		prev := s
		s = strings.TrimSpace(strings.TrimLeft(s, "-*+>#`_~[](|"))
		s = stripListNumber(s)
		s = stripRef(s)
		if s == prev {
			break
		}
	}
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsLetter(r) {
			break
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// stripListNumber removes "1." or "2)" when a space follows it.
func stripListNumber(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) || (s[i] != '.' && s[i] != ')') {
		return s
	}
	if i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
		return s[i+1:]
	}
	return s
}

// stripRef removes a leading "mNNNNN" ledger ref and a closing bracket after it.
func stripRef(s string) string {
	if len(s) < 6 || s[0] != 'm' {
		return s
	}
	for _, c := range s[1:6] {
		if c < '0' || c > '9' {
			return s
		}
	}
	return strings.TrimLeft(s[6:], "] ")
}
