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
