package kernel

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"single ascii", "a", 1},
		{"word of 4 is one token", "abcd", 1},
		{"word of 5 is two tokens", "abcde", 2},
		{"two words", "hello world", 4},
		{"symbols count one each", "a+b", 3},
		{"cjk one per rune", "日本語", 3},
		{"accent one rune", "é", 1},
		{"emoji one rune", "🙂", 1},
		{"whitespace only floors to 1", "   ", 1},
		{"newline separates words", "ab\ncd", 2},
		{"underscore is word char", "a_b", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTokens(tt.in); got != tt.want {
				t.Fatalf("EstimateTokens(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestEstimateTokensHuge(t *testing.T) {
	big := strings.Repeat("abcd", 250_000) // 1e6 word chars -> 250000 tokens
	if got := EstimateTokens(big); got != 250_000 {
		t.Fatalf("huge word run = %d, want 250000", got)
	}
	cjk := strings.Repeat("字", 100_000)
	if got := EstimateTokens(cjk); got != 100_000 {
		t.Fatalf("huge cjk = %d, want 100000", got)
	}
}

func TestCalibZeroValue(t *testing.T) {
	var c Calib
	if got := c.Tokens(100); got != 100 {
		t.Fatalf("zero value Tokens(100) = %d, want 100", got)
	}
	if got := c.Tokens(0); got != 0 {
		t.Fatalf("Tokens(0) = %d, want 0", got)
	}
}

func TestCalibUpdate(t *testing.T) {
	tests := []struct {
		name       string
		alpha      string
		samples    [][2]int // estimated, reported
		wantFactor string
		wantTokens int // Tokens(100) after samples
		wantN      int
	}{
		{"no samples keeps factor 1", "0.2", nil, "1", 100, 0},
		{"single sample EMA from 1", "0.2", [][2]int{{100, 200}}, "1.2", 120, 1},
		{"two samples", "0.2", [][2]int{{100, 200}, {100, 200}}, "1.36", 136, 2},
		{"zero estimate ignored", "0.2", [][2]int{{0, 200}}, "1", 100, 0},
		{"zero reported ignored", "0.2", [][2]int{{100, 0}}, "1", 100, 0},
		{"negative ignored", "0.2", [][2]int{{-5, 10}, {10, -1}}, "1", 100, 0},
		{"alpha 1 takes sample", "1", [][2]int{{100, 50}}, "0.5", 50, 1},
		{"bad alpha uses default", "7", [][2]int{{100, 200}}, "1.2", 120, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCalib(dec(tt.alpha))
			for _, s := range tt.samples {
				c.Update(s[0], s[1])
			}
			if cmp(c.Factor, dec(tt.wantFactor)) != 0 {
				t.Fatalf("Factor = %s, want %s", c.Factor.String(), tt.wantFactor)
			}
			if got := c.Tokens(100); got != tt.wantTokens {
				t.Fatalf("Tokens(100) = %d, want %d", got, tt.wantTokens)
			}
			if c.Samples != tt.wantN {
				t.Fatalf("Samples = %d, want %d", c.Samples, tt.wantN)
			}
		})
	}
}

func TestCalibMAE(t *testing.T) {
	c := NewCalib(dec("0.2"))
	c.Update(100, 200) // pred 100, err 0.5 -> MAE 0.5
	if cmp(c.MAE, dec("0.5")) != 0 {
		t.Fatalf("first MAE = %s, want 0.5", c.MAE.String())
	}
	c.Update(100, 200) // pred 120, err 0.4 -> MAE 0.5*0.8+0.4*0.2 = 0.48
	if cmp(c.MAE, dec("0.48")) != 0 {
		t.Fatalf("second MAE = %s, want 0.48", c.MAE.String())
	}
}

func TestCalibConverges(t *testing.T) {
	c := NewCalib(dec("0.2"))
	for i := 0; i < 200; i++ {
		c.Update(100, 150)
	}
	if cmp(abs(sub(c.Factor, dec("1.5"))), dec("0.000001")) > 0 {
		t.Fatalf("Factor = %s, want ~1.5", c.Factor.String())
	}
	if got := c.Tokens(1000); got != 1500 {
		t.Fatalf("Tokens(1000) = %d, want 1500", got)
	}
}
