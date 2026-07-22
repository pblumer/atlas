package expr_test

import (
	"testing"

	"github.com/pblumer/atlas/expr"
)

// TestClassifyScalars checks that each FEEL scalar reduces to the storable
// (kind, bool, text) triple Atlas persists, and that a non-scalar value is
// rendered to canonical text and stored as a string (the lossy fallback).
func TestClassifyScalars(t *testing.T) {
	list, err := expr.Compile("[1, 2, 3]")
	if err != nil {
		t.Fatalf("Compile list: %v", err)
	}
	listVal, err := list.Eval(nil)
	if err != nil {
		t.Fatalf("Eval list: %v", err)
	}

	for _, tc := range []struct {
		name     string
		v        expr.Value
		wantKind expr.ValueKind
		wantBool bool
		wantText string
	}{
		{"null", expr.Null, expr.KindNull, false, ""},
		{"bool-true", expr.Bool(true), expr.KindBool, true, ""},
		{"bool-false", expr.Bool(false), expr.KindBool, false, ""},
		{"number", expr.Number(42), expr.KindNumber, false, "42"},
		{"string", expr.String("hi"), expr.KindString, false, "hi"},
		// A list is non-scalar: it falls through to the default arm and is stored
		// as its canonical FEEL text under KindString.
		{"list", listVal, expr.KindString, false, listVal.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, b, text := expr.Classify(tc.v)
			if kind != tc.wantKind || b != tc.wantBool || text != tc.wantText {
				t.Errorf("Classify(%s) = (%d,%v,%q), want (%d,%v,%q)",
					tc.name, kind, b, text, tc.wantKind, tc.wantBool, tc.wantText)
			}
		})
	}
}

// TestClassifyFromStoredRoundTrip drives a scalar through Classify and back
// through FromStored, proving the two are inverses for the storable subset.
func TestClassifyFromStoredRoundTrip(t *testing.T) {
	for _, v := range []expr.Value{
		expr.Bool(true),
		expr.Number(-7),
		expr.String("round trip"),
	} {
		kind, b, text := expr.Classify(v)
		got := expr.FromStored(kind, b, text)
		if got.String() != v.String() {
			t.Errorf("round trip of %q = %q", v.String(), got.String())
		}
	}
}

// TestBoolConstructor covers the expr.Bool helper.
func TestBoolConstructor(t *testing.T) {
	if got := expr.Bool(true).String(); got != "true" {
		t.Errorf("Bool(true) = %q, want true", got)
	}
	if got := expr.Bool(false).String(); got != "false" {
		t.Errorf("Bool(false) = %q, want false", got)
	}
}

// TestFromStoredUnparseableNumber documents the inverse's failure mode: a stored
// number text that no longer parses degrades to FEEL null rather than erroring.
func TestFromStoredUnparseableNumber(t *testing.T) {
	if got := expr.FromStored(expr.KindNumber, false, "not-a-number").String(); got != "null" {
		t.Errorf("FromStored(number,%q) = %q, want null", "not-a-number", got)
	}
}
