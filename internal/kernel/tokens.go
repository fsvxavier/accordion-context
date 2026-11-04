package kernel

import (
	"math"
	"unicode"
	"unicode/utf8"
)

// DefaultCalibAlpha is the EMA smoothing factor for calibration (spec §15).
const DefaultCalibAlpha = 0.2

// EstimateTokens is a fast local heuristic by character class (spec §15.1):
// runs of ASCII letters/digits cost ceil(len/4); each other ASCII symbol costs
// 1; each non-ASCII rune (CJK, accents, emoji) costs 1; whitespace only ends a
// run. Non-empty input yields at least 1. Empty input yields 0.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	word := 0
	flush := func() {
		if word > 0 {
			n += (word + 3) / 4
			word = 0
		}
	}
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf && isASCIIWord(r):
			word++
		case r < utf8.RuneSelf && unicode.IsSpace(r):
			flush()
		case r < utf8.RuneSelf:
			flush()
			n++
		default:
			flush()
			n++
		}
	}
	flush()
	return max(n, 1)
}

func isASCIIWord(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// Calib maps local estimates onto provider-reported token counts (spec §15).
// The zero value is usable: Factor 0 means 1.0 and Alpha 0 means
// DefaultCalibAlpha. Only feed clean samples (no images, no failures); the
// kernel does not judge cleanliness.
type Calib struct {
	Alpha   float64 // EMA weight of a new sample
	Factor  float64 // reported/estimated ratio, EMA-smoothed
	Samples int
	MAE     float64 // EMA of |reported - predicted| / reported, before each update
}

// NewCalib returns a calibrator with Factor 1.0 and the given alpha
// (DefaultCalibAlpha when alpha <= 0 or > 1).
func NewCalib(alpha float64) Calib {
	if alpha <= 0 || alpha > 1 {
		alpha = DefaultCalibAlpha
	}
	return Calib{Alpha: alpha, Factor: 1}
}

func (c *Calib) alpha() float64 {
	if c.Alpha <= 0 || c.Alpha > 1 {
		return DefaultCalibAlpha
	}
	return c.Alpha
}

func (c *Calib) factor() float64 {
	if c.Factor <= 0 {
		return 1
	}
	return c.Factor
}

// Update folds one sample in. It is a no-op when either count is <= 0.
func (c *Calib) Update(estimated, reported int) {
	if estimated <= 0 || reported <= 0 {
		return
	}
	a := c.alpha()
	pred := c.Tokens(estimated)
	err := math.Abs(float64(reported-pred)) / float64(reported)
	if c.Samples == 0 {
		c.MAE = err
	} else {
		c.MAE = c.MAE*(1-a) + err*a
	}
	r := float64(reported) / float64(estimated)
	c.Factor = c.factor()*(1-a) + r*a
	c.Samples++
}

// Tokens returns the calibrated count for a local estimate.
func (c *Calib) Tokens(estimated int) int {
	if estimated <= 0 {
		return 0
	}
	return int(math.Round(float64(estimated) * c.factor()))
}
