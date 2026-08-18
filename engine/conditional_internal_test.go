package engine

import (
	"testing"

	"github.com/pblumer/atlas/expr"
)

// TestConditionHoldsNil pins the defensive guard in conditionHolds (ADR-0137): a nil compiled
// condition never holds, so an armed conditional with no predicate can never fire. The guard
// returns before touching the context, so it is safe to exercise directly with a nil condition.
func TestConditionHoldsNil(t *testing.T) {
	if conditionHolds(nil, nil, 0) {
		t.Error("conditionHolds(nil condition) = true, want false (a missing predicate never fires)")
	}
}

// TestConditionHoldsNonBoolean checks that a compiled condition evaluating to a non-boolean is not
// treated as satisfied (ADR-0137): only a FEEL-true value fires a conditional, so a number or a
// string leaves it armed rather than firing it. This exercises the expr.IsTrue guard on the eval
// result without needing a running instance (the expression reads no variables).
func TestConditionHoldsNonBoolean(t *testing.T) {
	cond, err := expr.CompileAuto("41 + 1")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	if conditionHolds(&ProcessingContext{}, cond, 0) {
		t.Error("conditionHolds(numeric) = true, want false (only a FEEL-true value fires)")
	}
}
