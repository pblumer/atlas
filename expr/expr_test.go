package expr_test

import (
	"testing"

	"github.com/pblumer/atlas/expr"
)

func TestCompileEvalArithmetic(t *testing.T) {
	c, err := expr.Compile("Amount * 2 + 1", "Amount", "Unused")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	v, err := c.Eval(map[string]expr.Value{"Amount": expr.Number(20)})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "41" {
		t.Fatalf("result = %q, want 41", got)
	}
}

func TestInputsReportsOnlyReadVariables(t *testing.T) {
	c, err := expr.Compile(`if Season = "Winter" then "Soup" else "Salad"`, "Season", "Guests")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got := c.Inputs()
	if len(got) != 1 || got[0] != "Season" {
		t.Fatalf("Inputs = %v, want [Season] (Guests is declared but unused)", got)
	}
	v, err := c.Eval(map[string]expr.Value{"Season": expr.String("Winter")})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.String() != "Soup" {
		t.Fatalf("result = %q, want Soup", v.String())
	}
}

func TestCompileRejectsBadExpression(t *testing.T) {
	if _, err := expr.Compile("1 + "); err == nil {
		t.Fatal("want a compile error for a malformed expression")
	}
}

// TestCompileRejectsUndeclaredVariable documents that referencing a variable not
// declared to Compile is caught at compile (deploy) time, not at runtime.
func TestCompileRejectsUndeclaredVariable(t *testing.T) {
	if _, err := expr.Compile("Mystery + 1"); err == nil {
		t.Fatal("want a compile error for an undeclared variable")
	}
}

func TestCompileAutoDiscoversInputs(t *testing.T) {
	c, err := expr.CompileAuto(`amount * (1 + taxRate)`)
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	got := c.Inputs()
	if len(got) != 2 || got[0] != "amount" || got[1] != "taxRate" {
		t.Fatalf("Inputs = %v, want [amount taxRate]", got)
	}
	v, err := c.Eval(map[string]expr.Value{
		"amount":  expr.Number(100),
		"taxRate": expr.FromStored(expr.KindNumber, false, "0.19"),
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.String() != "119" {
		t.Fatalf("result = %q, want 119", v.String())
	}
}

// TestCompileAutoStillRejectsSyntaxErrors ensures the discovery loop doesn't mask
// genuine compile errors.
func TestCompileAutoStillRejectsSyntaxErrors(t *testing.T) {
	if _, err := expr.CompileAuto("amount * "); err == nil {
		t.Fatal("want a compile error for malformed FEEL")
	}
}

// TestIsTrue covers the boolean coercion a sequence-flow condition relies on:
// only FEEL boolean true is true; false, null, and every non-boolean value are
// not (so a guard that doesn't evaluate to true is simply not taken).
func TestIsTrue(t *testing.T) {
	cases := []struct {
		name string
		v    expr.Value
		want bool
	}{
		{"bool true", expr.Bool(true), true},
		{"bool false", expr.Bool(false), false},
		{"null", expr.Null, false},
		{"number", expr.Number(1), false},
		{"string", expr.String("true"), false},
	}
	for _, tc := range cases {
		if got := expr.IsTrue(tc.v); got != tc.want {
			t.Errorf("IsTrue(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFromStoredRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		kind expr.ValueKind
		b    bool
		text string
		want string
	}{
		{expr.KindNumber, false, "42", "42"},
		{expr.KindString, false, "hi", "hi"},
		{expr.KindBool, true, "", "true"},
		{expr.KindNull, false, "", "null"},
	} {
		if got := expr.FromStored(tc.kind, tc.b, tc.text).String(); got != tc.want {
			t.Errorf("FromStored(%d,%v,%q) = %q, want %q", tc.kind, tc.b, tc.text, got, tc.want)
		}
	}
}
