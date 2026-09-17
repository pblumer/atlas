package expr_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// only returns the one fault src has, and fails when it has none or several — a
// test that says "reports something" would pass on the wrong sentence.
func only(t *testing.T, src string, declared ...string) expr.CallFault {
	t.Helper()
	faults := expr.CheckCalls(src, declared...)
	if len(faults) != 1 {
		t.Fatalf("CheckCalls(%q) = %d faults, want exactly 1: %+v", src, len(faults), faults)
	}
	return faults[0]
}

func none(t *testing.T, src string, declared ...string) {
	t.Helper()
	if faults := expr.CheckCalls(src, declared...); len(faults) != 0 {
		t.Fatalf("CheckCalls(%q) reported %+v, want nothing", src, faults)
	}
}

// The case this whole check exists for. `is defined` is a Camunda extension and
// the first thing somebody arriving from there writes; the engine compiles it to a
// constant null and says nothing, which is how a customer ends up INACTIV because
// a gateway read that null as "not true".
func TestAForeignDialectsFunctionIsNamedWithTheStandardWayToSayIt(t *testing.T) {
	f := only(t, "is defined(kunde.geburtsdatum)")
	if f.Name != "is defined" {
		t.Errorf("name = %q, want %q", f.Name, "is defined")
	}
	// Naming the equivalent is the point: a refusal that only says "no" leaves the
	// author to guess, and the guess is usually another Camunda function.
	if !strings.Contains(f.Message, "x != null") {
		t.Errorf("message does not say how to write it instead: %s", f.Message)
	}
}

// `put` and `put all` are the interesting half of the foreign set: the capability
// is here under the standard name, so the message points at it rather than at a
// rewrite.
func TestAForeignNameForSomethingThisBuildHasPointsAtTheStandardName(t *testing.T) {
	f := only(t, `put(c, "k", 1)`)
	if !strings.Contains(f.Message, "context put") {
		t.Errorf("message does not name the standard function: %s", f.Message)
	}
}

// Nothing about this is specific to Camunda: a typo produces the same silent null,
// and is far more common.
func TestAMisspelledFunctionIsReported(t *testing.T) {
	f := only(t, "strng length(s)")
	if f.Name != "strng length" {
		t.Errorf("name = %q, want %q", f.Name, "strng length")
	}
	if strings.Contains(f.Message, "another engine") {
		t.Errorf("a typo is not a dialect problem: %s", f.Message)
	}
}

// The second silent null from the same real model: `date()` is a real function
// called with no argument, which the engine binds to null exactly as it binds an
// unknown name.
func TestAKnownFunctionCalledWithTooFewArgumentsIsReported(t *testing.T) {
	f := only(t, "date()")
	if f.Name != "date" {
		t.Errorf("name = %q, want %q", f.Name, "date")
	}
	if !strings.Contains(f.Message, "0 arguments") {
		t.Errorf("message does not say what it was called with: %s", f.Message)
	}
	none(t, "date(x)")
}

func TestAKnownFunctionCalledWithTooManyArgumentsIsReported(t *testing.T) {
	only(t, `matches("a", "b", "c", "d")`)
	none(t, `matches("a", "b")`)
}

// A variadic builtin has no upper bound, and mirroring the engine's rule means
// mirroring that too.
func TestAVariadicCallIsNotAnArityFault(t *testing.T) {
	none(t, "count(a, b, c, d, e)")
	none(t, "sum([1, 2, 3])")
}

// A named call binds against whichever signature covers the names given, and an
// overloaded builtin has several. Second-guessing that here would be a second copy
// of a rule the engine already owns — so it is left alone.
func TestANamedCallIsLeftToTheEngine(t *testing.T) {
	none(t, `date(from: "2026-09-17")`)
}

// Everything that could legitimately hold a function value. Each of these is a
// callee the engine resolves against the scope at run time, and refusing one would
// block a model that works.
func TestWhatCouldHoldAFunctionIsLeftAlone(t *testing.T) {
	for _, src := range []string{
		"function(g) g(1)",                  // a parameter
		`{f: function(a) a + 1, r: f(1)}.r`, // a context entry
		"for f in fs return f(1)",           // a for iterator
		"some f in fs satisfies f(1) > 0",   // a quantifier iterator
		"[1, 2][item > 1]",                  // the filter's own name
		"fns[1](2)",                         // a callee that is not a plain name
	} {
		none(t, src)
	}
	// And a variable the caller says it will bind.
	none(t, "myfn(1)", "myfn")
}

// A syntax error is the real fault and the compiler reports it. Two of the Camunda
// functions cannot even be parsed, because `else` and `of` are keywords — saying
// something here as well would be one mistake reported twice, in two voices.
func TestASourceThatDoesNotParseIsNotSecondGuessed(t *testing.T) {
	none(t, `get or else(s, "fallback")`)
	none(t, `last day of month(date("2026-02-01"))`)
	none(t, "((((")
}

// One sentence per name, however often it is called: a loop body calling the same
// missing function five times is one mistake.
func TestOneSentencePerName(t *testing.T) {
	faults := expr.CheckCalls("is defined(a) and is defined(b) and is defined(c)")
	if len(faults) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(faults), faults)
	}
}

// Calls nested where a walker is easy to get wrong.
func TestCallsAreFoundWhereverTheySit(t *testing.T) {
	for _, src := range []string{
		"if is defined(a) then 1 else 2",
		"[1, is defined(a)]",
		"{k: is defined(a)}",
		"for i in [1] return is defined(a)",
		"some i in [1] satisfies is defined(a)",
		"upper case(string(is defined(a)))",
		"a.b[is defined(c)]",
		"1 + (2 * count(is defined(a)))",
	} {
		if f := only(t, src); f.Name != "is defined" {
			t.Errorf("CheckCalls(%q) found %q, want the nested call", src, f.Name)
		}
	}
}

// CheckCallsError is what a compile step wants: nil when there is nothing to say.
func TestCheckCallsErrorIsNilWhenThereIsNothingToSay(t *testing.T) {
	if err := expr.CheckCallsError("kunde.geburtsdatum != null"); err != nil {
		t.Fatalf("clean expression reported %v", err)
	}
	err := expr.CheckCallsError("is defined(x)")
	if err == nil {
		t.Fatal("a call that can only be null reported nothing")
	}
	if !strings.Contains(err.Error(), "is defined") {
		t.Errorf("error does not name the call: %v", err)
	}
}

// The engine still answers null — that is DMN's rule and this check does not touch
// it. What changed is only that a BPMN deploy now refuses to carry such a call.
func TestTheEngineStillEvaluatesAnUnknownCallToNull(t *testing.T) {
	c, err := expr.CompileAuto("is defined(x)")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	v, err := c.Eval(nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "null" {
		t.Errorf("evaluated to %s, want null — the engine's semantics must not have changed", got)
	}
}
