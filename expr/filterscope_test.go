package expr_test

import (
	"testing"

	"github.com/pblumer/atlas/expr"
)

// A process variable read only inside a filter is an input like any other: before,
// CompileAuto never declared it (inside a filter an unknown name is not an error, it
// may be a key of the element), so it read null and the filter matched nothing. Found
// against a real tenant: examples/m365-dienste's governance check compared each group's
// alias with application.NAME_PREFIX inside a filter and reported no deviation at all.
func TestAVariableReadOnlyInsideAFilterIsBound(t *testing.T) {
	c, err := expr.CompileAuto(`count(gruppen[not(matches(mailNickname, "^" + application.NAME_PREFIX + "tm-"))])`)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"application", "gruppen"} {
		found := false
		for _, got := range c.Inputs() {
			found = found || got == in
		}
		if !found {
			t.Errorf("inputs %v miss %q", c.Inputs(), in)
		}
	}
	if got := c.FilterOnly(); len(got) != 2 || got[0] != "application" || got[1] != "mailNickname" {
		t.Errorf("filter-only names %v, want [application mailNickname]", got)
	}
	v, err := c.Eval(map[string]expr.Value{
		"gruppen": expr.FromJSON([]any{
			map[string]any{"mailNickname": "p-tm-acc-projekt"},
			map[string]any{"mailNickname": "p-projektraum"},
		}),
		"application": expr.ContextOf(map[string]expr.Value{"NAME_PREFIX": expr.String("p-")}),
		// a process variable named like the elements' key does not shadow it
		"mailNickname": expr.String("p-tm-x-shadow"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := expr.ToJSON(v); got != "1" {
		t.Fatalf("deviations = %s, want 1", got)
	}

	// A filter on the elements' own keys needs no variable at all.
	c, err = expr.CompileAuto(`people[age >= 18].name`)
	if err != nil {
		t.Fatal(err)
	}
	v, err = c.Eval(map[string]expr.Value{"people": expr.FromJSON([]any{
		map[string]any{"name": "a", "age": 17.0}, map[string]any{"name": "b", "age": 30.0},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := expr.ToJSON(v); got != `["b"]` {
		t.Fatalf("people[age >= 18].name = %s", got)
	}
}

// UnbindFilterOnly is how the compiler keeps a variable it must not bind out of a
// filter that only names it: the name is no longer an input, so nothing binds it, and
// inside the filter it reads the element's key or null — as before filter-only names
// were bound. A name read outside the filter as well stays an input.
func TestUnbindFilterOnlyLeavesTheNameToTheElements(t *testing.T) {
	c, err := expr.CompileAuto(`count(people[vorname != null]) + count(people[alter > grenze]) + grenze`)
	if err != nil {
		t.Fatal(err)
	}
	dropped := c.UnbindFilterOnly(func(n string) bool { return n == "vorname" || n == "grenze" })
	if len(dropped) != 1 || dropped[0] != "vorname" {
		t.Fatalf("dropped %v, want [vorname] (grenze is read outside the filter too)", dropped)
	}
	for _, in := range c.Inputs() {
		if in == "vorname" {
			t.Fatalf("inputs %v still bind vorname", c.Inputs())
		}
	}
	for _, in := range c.FilterOnly() {
		if in == "vorname" {
			t.Fatalf("filter-only names %v still list vorname", c.FilterOnly())
		}
	}
	// A caller binds only Inputs; even if a variable vorname were passed, the element
	// without the key reads null.
	v, err := c.Eval(map[string]expr.Value{
		"people": expr.FromJSON([]any{
			map[string]any{"vorname": "Ada", "alter": 40.0},
			map[string]any{"alter": 10.0},
		}),
		"grenze": expr.Number(18),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := expr.ToJSON(v); got != "20" {
		t.Fatalf("result = %s, want 20 (1 named + 1 adult + 18)", got)
	}
	if again := c.UnbindFilterOnly(func(string) bool { return true }); len(again) != 1 || again[0] != "alter" {
		t.Fatalf("second unbind dropped %v, want [alter]", again)
	}
}
