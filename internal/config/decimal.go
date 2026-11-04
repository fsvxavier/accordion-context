package config

import (
	"fmt"
	"math"
	"strconv"

	"github.com/cockroachdb/apd/v3"
)

// decCtx parses decimal settings: 34 significant digits, round half to even.
// Traps is empty, so parsing reports errors only through its return values.
var decCtx = func() *apd.Context {
	c := apd.BaseContext.WithPrecision(34)
	c.Rounding = apd.RoundHalfEven
	c.Traps = 0
	return c
}()

// dec parses a decimal literal. It panics on malformed input, so it is for
// constants only.
func dec(s string) apd.Decimal {
	d, err := parseDecimal(s)
	if err != nil {
		panic(fmt.Sprintf("config: bad decimal literal %q: %v", s, err))
	}
	return d
}

// parseDecimal parses a finite decimal number such as "0.75" or "1e-3".
// NaN and infinities are rejected.
func parseDecimal(s string) (apd.Decimal, error) {
	d, _, err := decCtx.NewFromString(s)
	if err != nil {
		return apd.Decimal{}, err
	}
	if d.Form != apd.Finite {
		return apd.Decimal{}, fmt.Errorf("not a finite number: %s", s)
	}
	return *d, nil
}

// decimalFromFloat converts a YAML float. It goes through the shortest decimal
// string, so 0.75 stays exactly 0.75.
func decimalFromFloat(f float64) (apd.Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return apd.Decimal{}, fmt.Errorf("not a finite number: %v", f)
	}
	return parseDecimal(strconv.FormatFloat(f, 'g', -1, 64))
}

// formatDecimal renders d without trailing zeros, so 0.30 prints as 0.3.
func formatDecimal(d apd.Decimal) string {
	var r apd.Decimal
	r.Reduce(&d)
	return r.Text('f')
}

// intDec returns n as a decimal.
func intDec(n int) apd.Decimal {
	var d apd.Decimal
	d.SetInt64(int64(n))
	return d
}
