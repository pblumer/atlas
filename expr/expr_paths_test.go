package expr_test

import (
	"testing"

	"github.com/pblumer/atlas/expr"
)

// TestAsIntRefusesAFraction: a loop cardinality of 2.5 is not "two" or "three" — it
// is not a count, and rounding it either way would run a number of iterations the
// model never said.
func TestAsIntRefusesAFraction(t *testing.T) {
	if n, ok := expr.AsInt(evalAuto(t, "5 / 2")); ok {
		t.Fatalf("AsInt(2.5) = %d, true; want false", n)
	}
}
