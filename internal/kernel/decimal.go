package kernel

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// decCtx is the shared arithmetic context of this package: 34 significant
// digits, round half to even. Traps is empty, so operations never return an
// error; callers guard against division by zero themselves.
var decCtx = func() *apd.Context {
	c := apd.BaseContext.WithPrecision(34)
	c.Rounding = apd.RoundHalfEven
	c.Traps = 0
	return c
}()

// dec parses a decimal literal. It panics on malformed input, so it is for
// constants only.
func dec(s string) apd.Decimal {
	d, _, err := decCtx.NewFromString(s)
	if err != nil {
		panic(fmt.Sprintf("kernel: bad decimal literal %q: %v", s, err))
	}
	return *d
}

// intDec returns n as a decimal.
func intDec(n int) apd.Decimal {
	var d apd.Decimal
	d.SetInt64(int64(n))
	return d
}

func add(x, y apd.Decimal) apd.Decimal {
	var r apd.Decimal
	_, _ = decCtx.Add(&r, &x, &y)
	return r
}

func sub(x, y apd.Decimal) apd.Decimal {
	var r apd.Decimal
	_, _ = decCtx.Sub(&r, &x, &y)
	return r
}

func mul(x, y apd.Decimal) apd.Decimal {
	var r apd.Decimal
	_, _ = decCtx.Mul(&r, &x, &y)
	return r
}

// quo divides x by y. y must not be zero.
func quo(x, y apd.Decimal) apd.Decimal {
	var r apd.Decimal
	_, _ = decCtx.Quo(&r, &x, &y)
	return r
}

func abs(x apd.Decimal) apd.Decimal {
	var r apd.Decimal
	_, _ = decCtx.Abs(&r, &x)
	return r
}

// cmp returns -1, 0 or +1 as x is less than, equal to or greater than y.
func cmp(x, y apd.Decimal) int {
	return x.Cmp(&y)
}

// truncInt drops the fractional part toward zero, like int(float64) in Go.
func truncInt(x apd.Decimal) int {
	var integ apd.Decimal
	x.Modf(&integ, nil)
	n, err := integ.Int64()
	if err != nil {
		panic(fmt.Sprintf("kernel: value %s does not fit an int: %v", x.String(), err))
	}
	return int(n)
}

// roundInt rounds x to an integer, half to even (spec §15 rounding rule).
func roundInt(x apd.Decimal) int {
	var r apd.Decimal
	_, _ = decCtx.RoundToIntegralValue(&r, &x)
	n, err := r.Int64()
	if err != nil {
		panic(fmt.Sprintf("kernel: value %s does not fit an int: %v", x.String(), err))
	}
	return int(n)
}

// geFrac reports whether n/d >= thr for d > 0, without dividing (exact).
func geFrac(n, d int, thr apd.Decimal) bool {
	return cmp(intDec(n), mul(thr, intDec(d))) >= 0
}

// ltFrac reports whether n/d < thr for d > 0, without dividing (exact).
func ltFrac(n, d int, thr apd.Decimal) bool {
	return cmp(intDec(n), mul(thr, intDec(d))) < 0
}
