package dmn

import (
	"testing"

	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// TestOutputVariable covers both shapes of a decision result: a single-output
// decision stores its value directly (as a scalar), while a multi-output decision
// stores the whole output map as a structured JSON context.
func TestOutputVariable(t *testing.T) {
	single := outputVariable("dish", map[string]any{"Dish": "Roastbeef"})
	if single.Name != "dish" || single.Kind != model.VarString || single.Text != "Roastbeef" {
		t.Errorf("single output = %+v, want string Roastbeef", single)
	}

	multi := outputVariable("result", map[string]any{"Dish": "Salad", "Drink": "Water"})
	if multi.Kind != model.VarJSON {
		t.Fatalf("multi output kind = %v, want VarJSON", multi.Kind)
	}
	// Canonical JSON: keys sorted, so the encoding is deterministic across replays.
	if multi.Text != `{"Dish":"Salad","Drink":"Water"}` {
		t.Errorf("multi output text = %q, want the sorted JSON context", multi.Text)
	}
}

// TestFeelToInput covers the FEEL→decision-input conversion: a scalar keeps its
// type, and a number arrives as a JSON number so it is indistinguishable from a
// static input.
func TestFeelToInput(t *testing.T) {
	if got := feelToInput(expr.String("Winter")); got != "Winter" {
		t.Errorf("feelToInput(string) = %#v, want Winter", got)
	}
	if got := feelToInput(expr.Number(8)); got != float64(8) {
		t.Errorf("feelToInput(number) = %#v, want 8", got)
	}
	if got := feelToInput(expr.Null); got != nil {
		t.Errorf("feelToInput(null) = %#v, want nil", got)
	}
}

// TestBindStoredVars covers the binding of stored variables into an evaluation:
// the reserved processInstanceKey builtin, a present variable, an absent name
// (left unbound), and the empty-names shortcut.
func TestBindStoredVars(t *testing.T) {
	scopeVars := map[string]model.VariableValue{
		"season": {Name: "season", Kind: model.VarString, Text: "Winter"},
	}
	got := bindStoredVars(99, scopeVars, []string{builtinProcessInstanceKey, "season", "absent"})
	if v, ok := got[builtinProcessInstanceKey]; !ok || v.String() != "99" {
		t.Errorf("processInstanceKey binding = %v (ok=%v), want \"99\"", v, ok)
	}
	if v, ok := got["season"]; !ok || v.String() != "Winter" {
		t.Errorf("season binding = %v (ok=%v), want Winter", v, ok)
	}
	if _, ok := got["absent"]; ok {
		t.Error("absent variable was bound; want it left unbound (FEEL null)")
	}
	if bindStoredVars(1, nil, nil) != nil {
		t.Error("bindStoredVars with no names should return nil")
	}
}

// TestBuildInputsStaticOnly covers buildInputs' no-mapping path (static base
// returned as-is) and its decode-error path (malformed interned static JSON).
func TestBuildInputsStaticOnly(t *testing.T) {
	in, err := buildInputs(nil, 0, `{"Season":"Winter"}`, nil)
	if err != nil {
		t.Fatalf("buildInputs static: %v", err)
	}
	if in["Season"] != "Winter" {
		t.Errorf("Season = %v, want Winter", in["Season"])
	}
	if _, err := buildInputs(nil, 0, `{not json`, nil); err == nil {
		t.Fatal("buildInputs with malformed static JSON: got nil error, want an error")
	}
}

// TestVarKindRoundTrip covers every branch of the kind mappings the worker uses to
// bind stored variables and to store a decision result.
func TestVarKindRoundTrip(t *testing.T) {
	cases := []struct {
		vk model.VarKind
		ek expr.ValueKind
	}{
		{model.VarBool, expr.KindBool},
		{model.VarNumber, expr.KindNumber},
		{model.VarString, expr.KindString},
		{model.VarJSON, expr.KindJSON},
		{model.VarNull, expr.KindNull},
	}
	for _, c := range cases {
		if got := toExprKind(c.vk); got != c.ek {
			t.Errorf("toExprKind(%v) = %v, want %v", c.vk, got, c.ek)
		}
		if got := toVarKind(c.ek); got != c.vk {
			t.Errorf("toVarKind(%v) = %v, want %v", c.ek, got, c.vk)
		}
	}
}
