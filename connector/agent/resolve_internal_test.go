package agent

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// The three helpers an ai task's prompt is evaluated through. They are this package's own
// copies of what every other connector carries — the mapping is deliberately not shared,
// so that the stored-variable enum and the FEEL one can evolve apart — and a copy owes its
// own tests, because a copy is exactly the thing that drifts unnoticed.

// A literal prompt is used verbatim, and a FEEL prompt is evaluated over the variables the
// task sees. Those are the two halves of the fx toggle every authored connector field has.
func TestResolveValueIsLiteralOrFeel(t *testing.T) {
	vars := map[string]model.VariableValue{
		"betreff": {Name: "betreff", Kind: model.VarString, Text: "Dachsanierung"},
	}
	if got := resolveValue(compiler.RestExpr{Literal: "Fasse zusammen"}, 7, vars); got != "Fasse zusammen" {
		t.Errorf("literal = %q, want it verbatim", got)
	}
	e, err := expr.CompileAuto(`"Klassifiziere: " + betreff`)
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	if got := resolveValue(compiler.RestExpr{Expr: e}, 7, vars); got != "Klassifiziere: Dachsanierung" {
		t.Errorf("feel = %q, want it evaluated over the scope", got)
	}
}

// A prompt that reads a variable the instance does not carry becomes the empty string,
// not an error and not the text "null". That is the engine's null-propagating contract
// (FEEL yields null rather than failing), and it is what the other connectors' fields do.
//
// It matters more here than elsewhere: an empty prompt is a question worth nothing, and it
// is better that a model refuse it visibly than that the engine block the lease and park
// the token with nothing said.
func TestResolveValueOnAnAbsentVariableIsEmpty(t *testing.T) {
	for _, src := range []string{`fehlt`, `fehlt + 1`, `fehlt.tiefer`} {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("CompileAuto(%s): %v", src, err)
		}
		if got := resolveValue(compiler.RestExpr{Expr: e}, 7, nil); got != "" {
			t.Errorf("%s = %q, want the empty string", src, got)
		}
	}
}

// processInstanceKey is bound as a string, and a name the scope does not carry is left
// unbound so FEEL sees null rather than a zero value that means something.
func TestBindVarsBindsTheBuiltinAndLeavesTheRestNull(t *testing.T) {
	if got := bindVars(7, nil, nil); got != nil {
		t.Errorf("bindVars with no names = %v, want nil", got)
	}
	got := bindVars(4711, map[string]model.VariableValue{
		"da": {Name: "da", Kind: model.VarString, Text: "hier"},
	}, []string{builtinProcessInstanceKey, "da", "fehlt"})
	if _, ok := got["fehlt"]; ok {
		t.Error("an absent variable was bound; FEEL must see null, not a zero value")
	}
	if _, _, text := expr.Classify(got[builtinProcessInstanceKey]); text != "4711" {
		t.Errorf("processInstanceKey = %q, want the instance's key as a string", text)
	}
	if _, _, text := expr.Classify(got["da"]); text != "hier" {
		t.Errorf("da = %q, want the scope's value", text)
	}
}

// Every stored kind maps to its FEEL counterpart, and anything this package does not know
// maps to null rather than to a plausible wrong kind. The table is written out because the
// two enums are separate on purpose: a new stored kind must fail here rather than silently
// arrive as a string.
func TestToExprKindMapsEveryStoredKind(t *testing.T) {
	for _, tc := range []struct {
		stored model.VarKind
		want   expr.ValueKind
	}{
		{model.VarBool, expr.KindBool},
		{model.VarNumber, expr.KindNumber},
		{model.VarString, expr.KindString},
		{model.VarJSON, expr.KindJSON},
		{model.VarKind(200), expr.KindNull},
	} {
		if got := toExprKind(tc.stored); got != tc.want {
			t.Errorf("toExprKind(%v) = %v, want %v", tc.stored, got, tc.want)
		}
	}
}

// A prompt that reads a number, a boolean or a JSON value gets that value's string form,
// which is what a model is actually put — there is no second wire shape for a question.
func TestAPromptReadsEveryKindOfVariable(t *testing.T) {
	vars := map[string]model.VariableValue{
		"anzahl": {Name: "anzahl", Kind: model.VarNumber, Text: "3"},
		"offen":  {Name: "offen", Kind: model.VarBool, Bool: true},
		"antrag": {Name: "antrag", Kind: model.VarJSON, Text: `{"art":"Dach"}`},
	}
	for src, want := range map[string]string{
		`string(anzahl)`: "3",
		`string(offen)`:  "true",
		`antrag.art`:     "Dach",
	} {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("CompileAuto(%s): %v", src, err)
		}
		if got := resolveValue(compiler.RestExpr{Expr: e}, 1, vars); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", src, got, want)
		}
	}
}
