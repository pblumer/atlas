package ldap

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

func TestResolveSecret(t *testing.T) {
	if got := resolveSecret(nil, "X"); got != "" {
		t.Errorf("resolveSecret(nil) = %q, want empty", got)
	}
	if got := resolveSecret(func(string) string { return "  tok  " }, "X"); got != "tok" {
		t.Errorf("resolveSecret(trim) = %q, want tok", got)
	}
}

func TestScalarString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hi", "hi"},
		{json.Number("42"), "42"},
		{true, "true"},
		{false, "false"},
		{nil, ""},
		{map[string]any{"k": "v"}, "map[k:v]"}, // default → fmt
	}
	for _, c := range cases {
		if got := scalarString(c.in); got != c.want {
			t.Errorf("scalarString(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToStrings(t *testing.T) {
	if got := toStrings([]any{"a", json.Number("2"), true}); !reflect.DeepEqual(got, []string{"a", "2", "true"}) {
		t.Errorf("toStrings(array) = %v, want [a 2 true]", got)
	}
	if got := toStrings("solo"); !reflect.DeepEqual(got, []string{"solo"}) {
		t.Errorf("toStrings(scalar) = %v, want [solo]", got)
	}
}

func TestAttrsFromVar(t *testing.T) {
	vars := map[string]model.VariableValue{
		"entry":  {Name: "entry", Kind: model.VarJSON, Text: `{"cn":"Arno","objectClass":["top","person"]}`},
		"scalar": {Name: "scalar", Kind: model.VarString, Text: "not-an-object"},
	}
	attrs, err := attrsFromVar("entry", vars)
	if err != nil {
		t.Fatalf("attrsFromVar(entry): %v", err)
	}
	if !reflect.DeepEqual(attrs["cn"], []string{"Arno"}) || len(attrs["objectClass"]) != 2 {
		t.Errorf("attrs = %v, want cn=[Arno] and two objectClass values", attrs)
	}
	if _, err := attrsFromVar("missing", vars); err == nil {
		t.Error("attrsFromVar(missing): want an error for an absent variable")
	}
	if _, err := attrsFromVar("scalar", vars); err == nil {
		t.Error("attrsFromVar(scalar): want an error for a non-object variable")
	}
}

func TestToExprKind(t *testing.T) {
	cases := map[model.VarKind]expr.ValueKind{
		model.VarBool:   expr.KindBool,
		model.VarNumber: expr.KindNumber,
		model.VarString: expr.KindString,
		model.VarJSON:   expr.KindJSON,
		model.VarNull:   expr.KindNull,
	}
	for k, want := range cases {
		if got := toExprKind(k); got != want {
			t.Errorf("toExprKind(%v) = %v, want %v", k, got, want)
		}
	}
}

func TestToVarKind(t *testing.T) {
	cases := map[expr.ValueKind]model.VarKind{
		expr.KindBool:   model.VarBool,
		expr.KindNumber: model.VarNumber,
		expr.KindString: model.VarString,
		expr.KindJSON:   model.VarJSON,
		expr.KindNull:   model.VarNull,
	}
	for k, want := range cases {
		if got := toVarKind(k); got != want {
			t.Errorf("toVarKind(%v) = %v, want %v", k, got, want)
		}
	}
}

func TestVarToAny(t *testing.T) {
	if got := varToAny(&model.VariableValue{Kind: model.VarBool, Bool: true}); got != true {
		t.Errorf("bool = %#v", got)
	}
	if got := varToAny(&model.VariableValue{Kind: model.VarNumber, Text: "7"}); got != json.Number("7") {
		t.Errorf("number = %#v", got)
	}
	if got := varToAny(&model.VariableValue{Kind: model.VarString, Text: "s"}); got != "s" {
		t.Errorf("string = %#v", got)
	}
	obj, ok := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: `{"a":1}`}).(map[string]any)
	if !ok || obj["a"] != json.Number("1") {
		t.Errorf("json = %#v, want a nested object", obj)
	}
	if got := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: "{bad"}); got != nil {
		t.Errorf("bad json = %#v, want nil", got)
	}
	if got := varToAny(&model.VariableValue{Kind: model.VarNull}); got != nil {
		t.Errorf("null = %#v, want nil", got)
	}
}

func TestBindVars(t *testing.T) {
	vars := map[string]model.VariableValue{
		"s": {Name: "s", Kind: model.VarString, Text: "x"},
		"n": {Name: "n", Kind: model.VarNumber, Text: "1"},
	}
	m := bindVars(7, vars, []string{builtinProcessInstanceKey, "s", "n", "missing"})
	if len(m) != 3 { // the builtin + s + n; missing left unbound
		t.Fatalf("bound %d names, want 3", len(m))
	}
	if m[builtinProcessInstanceKey] != expr.String("7") {
		t.Errorf("processInstanceKey bound to %v, want the scope key as a string", m[builtinProcessInstanceKey])
	}
	if bindVars(1, vars, nil) != nil {
		t.Error("bindVars(no names) should be nil")
	}
}
