package feelgen

import (
	"strings"
	"testing"
)

// The check is the part of this feature that makes a free model usable: whatever it
// writes is compiled and evaluated by the engine that will run it, before anybody sees
// it. These tests pin that the check says what the engine says — no more forgiving, and
// no stricter.

func TestEvaluateReportsWhatTheEngineSays(t *testing.T) {
	for _, tc := range []struct {
		expr       string
		vars       map[string]any
		kind, text string
	}{
		{`if total > 1000 then total * 0.1 else 0`, map[string]any{"total": num("1500")}, "number", "150"},
		{`x > 1`, map[string]any{"x": num("2")}, "boolean", "true"},
		{`upper case(name)`, map[string]any{"name": "bern"}, "string", "BERN"},
		{`for x in xs return x * 2`, map[string]any{"xs": []any{num("1"), num("2")}}, "json", "[2,4]"},
		{`sum(xs)`, map[string]any{"xs": []any{}}, "null", "null"},
	} {
		c := Evaluate(tc.expr, tc.vars, nil, false)
		if !c.OK || c.Kind != tc.kind || c.Result != tc.text {
			t.Errorf("%s = %+v, want %s %s", tc.expr, c, tc.kind, tc.text)
		}
		if c.Matches != nil {
			t.Errorf("%s: no result was stated, yet the check says %v", tc.expr, *c.Matches)
		}
	}
}

// TestEvaluateNamesTheInputsAndWhatIsMissing: an input the example does not bind reads
// as null, which makes the example prove nothing about it.
func TestEvaluateNamesTheInputsAndWhatIsMissing(t *testing.T) {
	c := Evaluate(`a + b + c`, map[string]any{"a": num("1"), "c": nil}, nil, false)
	if strings.Join(c.Inputs, ",") != "a,b,c" {
		t.Errorf("inputs = %v", c.Inputs)
	}
	// c is bound to null on purpose, which is a value; b is not bound at all.
	if strings.Join(c.Missing, ",") != "b" {
		t.Errorf("missing = %v, want only b", c.Missing)
	}
}

// TestEvaluateRefusesWhatCanOnlyBeNull: the engine compiles an unknown function into a
// constant null and says nothing (ADR-0388). The check must say something, and say the
// same thing a deploy would — including the standard way to write a foreign name.
func TestEvaluateRefusesWhatCanOnlyBeNull(t *testing.T) {
	c := Evaluate(`is defined(x)`, map[string]any{"x": num("1")}, nil, false)
	if c.OK || !strings.Contains(c.Error, "x != null") {
		t.Errorf("check = %+v, want the refusal with the standard equivalent", c)
	}
	c = Evaluate(`summe(xs)`, map[string]any{"xs": []any{}}, nil, false)
	if c.OK || !strings.Contains(c.Error, "summe") {
		t.Errorf("check = %+v, want the unknown function named", c)
	}
}

func TestEvaluateReportsASyntaxError(t *testing.T) {
	c := Evaluate(`if x then`, nil, nil, false)
	if c.OK || c.Error == "" {
		t.Errorf("check = %+v, want a compile error", c)
	}
	if c := Evaluate("  ", nil, nil, false); c.OK {
		t.Errorf("an empty expression passed the check: %+v", c)
	}
}

// TestEvaluateComparesTheStatedResultAsFEELDoes: what the model says the expression
// returns is compared with FEEL's own equality, so 150 and 150.0 agree, and a date or
// duration written as JSON text agrees with the typed value the engine produced.
func TestEvaluateComparesTheStatedResultAsFEELDoes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expr     string
		vars     map[string]any
		expected any
		want     bool
	}{
		{"number", `1500 * 0.1`, nil, num("150.0"), true},
		{"wrong number", `1500 * 0.1`, nil, num("15"), false},
		{"boolean", `1 < 2`, nil, true, true},
		{"string", `"a" + "b"`, nil, "ab", true},
		{"list", `[1, 2]`, nil, []any{num("1"), num("2")}, true},
		{"context", `{a: 1}`, nil, map[string]any{"a": num("1")}, true},
		{"date", `date("2024-03-01") + duration("P1D")`, nil, "2024-03-02", true},
		{"date and time", `date and time("2024-03-01T10:00:00")`, nil, "2024-03-01T10:00:00", true},
		{"duration", `date("2024-03-02") - date("2024-03-01")`, nil, "P1D", true},
		{"wrong duration", `date("2024-03-03") - date("2024-03-01")`, nil, "P1D", false},
		{"null stated and produced", `sum(xs)`, map[string]any{"xs": []any{}}, nil, true},
		// The parse-based comparisons must not let a null result agree with any text:
		// date("nonsense") is null too, and null = null is true.
		{"null produced, text stated", `sum(xs)`, map[string]any{"xs": []any{}}, "nonsense", false},
		{"value produced, null stated", `1`, nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Evaluate(tc.expr, tc.vars, tc.expected, true)
			if !c.OK {
				t.Fatalf("check failed: %+v", c)
			}
			if c.Matches == nil || *c.Matches != tc.want {
				t.Errorf("matches = %v, want %v (result %s %s, expected %s)", c.Matches, tc.want, c.Kind, c.Result, c.Expected)
			}
		})
	}
}

// TestSettled is the rule the correction rounds stop on.
func TestSettled(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name string
		p    Proposal
		c    Check
		want bool
	}{
		{"a question", Proposal{Explanation: "Welche Variable?"}, Check{}, true},
		{"nothing at all", Proposal{}, Check{}, false},
		{"works, no claim", Proposal{Expression: "1"}, Check{OK: true}, true},
		{"works and agrees", Proposal{Expression: "1"}, Check{OK: true, Matches: &yes}, true},
		{"works and disagrees", Proposal{Expression: "1"}, Check{OK: true, Matches: &no}, false},
		{"works on a missing input", Proposal{Expression: "x"}, Check{OK: true, Missing: []string{"x"}}, false},
		{"does not compile", Proposal{Expression: "if"}, Check{Error: "boom"}, false},
	} {
		if got := settled(tc.p, tc.c); got != tc.want {
			t.Errorf("%s: settled = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestProblemTellsTheModelWhatToFix: the correction prompt is only as good as the
// sentence it carries.
func TestProblemTellsTheModelWhatToFix(t *testing.T) {
	no := false
	for _, tc := range []struct {
		p    Proposal
		c    Check
		want string
	}{
		{Proposal{}, Check{}, "neither an expression nor an explanation"},
		{Proposal{Expression: "trim(x)"}, Check{Error: "write `replace`"}, "write `replace`"},
		{Proposal{Expression: "a + b"}, Check{OK: true, Missing: []string{"b"}}, "b"},
		{Proposal{Expression: "1"}, Check{OK: true, Result: "1", Kind: "number", Expected: "2", Matches: &no}, "returns 1 (number), but you said it returns 2"},
	} {
		if got := problem(tc.p, tc.c); !strings.Contains(got, tc.want) {
			t.Errorf("problem = %q, want it to contain %q", got, tc.want)
		}
	}
}

// TestEvaluateNamesItsFault: the check says which kind of thing went wrong, and for a
// refused call which callee, because those are what the prompt is measured against.
func TestEvaluateNamesItsFault(t *testing.T) {
	c := Evaluate(`is defined(a) and trim(b) = ""`, map[string]any{"a": nil, "b": ""}, nil, false)
	if c.Fault != FaultCalls || strings.Join(c.Calls, ",") != "is defined,trim" {
		t.Errorf("check = %+v, want both refused callees", c)
	}
	if c := Evaluate(`if x then`, nil, nil, false); c.Fault != FaultCompile {
		t.Errorf("fault = %q, want compile", c.Fault)
	}
	if c := Evaluate(`1 + 1`, nil, nil, false); c.Fault != "" || c.Calls != nil {
		t.Errorf("a working expression reports a fault: %+v", c)
	}
}
