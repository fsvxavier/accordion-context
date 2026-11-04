package anthropic

import "testing"

func TestParseRefAcceptsWindowLabels(t *testing.T) {
	for in, want := range map[string]int64{"[m00042]": 42, " [m42] ": 42, "m00042": 42, "#7": 7} {
		got, err := ParseRef(in)
		if err != nil || got != want {
			t.Errorf("ParseRef(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"[m00042", "[]", "[x42]"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) accepted", bad)
		}
	}
}
