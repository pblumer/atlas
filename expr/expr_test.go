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
