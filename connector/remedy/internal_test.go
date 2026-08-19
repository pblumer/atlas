package remedy

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// TestToExprKind asserts the stored-kind → expr-kind mapping over every variable
// kind, including the null/default fallthrough, so a FEEL binding sees the right type.
func TestToExprKind(t *testing.T) {
	cases := map[model.VarKind]expr.ValueKind{
		model.VarBool:     expr.KindBool,
		model.VarNumber:   expr.KindNumber,
		model.VarString:   expr.KindString,
		model.VarJSON:     expr.KindJSON,
		model.VarNull:     expr.KindNull,
		model.VarKind(99): expr.KindNull, // unknown kind falls through to null
	}
	for in, want := range cases {
		if got := toExprKind(in); got != want {
			t.Errorf("toExprKind(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestResolveValueBranches covers both arms of resolveValue: a literal is returned
// verbatim, a compiled FEEL expression is evaluated over the bound scope variables,
// and an unbound reference (FEEL null) coerces to the empty string.
func TestResolveValueBranches(t *testing.T) {
	if got := resolveValue(compiler.RestExpr{Literal: "HPD:Help Desk"}, 1, nil); got != "HPD:Help Desk" {
		t.Errorf("literal resolveValue = %q, want the literal", got)
	}
	e, err := expr.CompileAuto(`"form:" + which`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	vars := map[string]model.VariableValue{"which": {Name: "which", Kind: model.VarString, Text: "incidents"}}
	if got := resolveValue(compiler.RestExpr{Expr: e}, 7, vars); got != "form:incidents" {
		t.Errorf("expr resolveValue = %q, want the evaluated string", got)
	}
	missing, err := expr.CompileAuto(`nope`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := resolveValue(compiler.RestExpr{Expr: missing}, 7, nil); got != "" {
		t.Errorf("unbound resolveValue = %q, want empty", got)
	}
}

// TestBindVarsEmpty covers bindVars' early return for an expression that reads no
// variables (a constant), which needs no binding.
func TestBindVarsEmpty(t *testing.T) {
	if got := bindVars(1, nil, nil); got != nil {
		t.Errorf("bindVars with no names = %v, want nil", got)
	}
}

// TestEntryIDFromLocation covers extracting the created entry's id from an AR System
// Location header, plus the empty and bare-id fallbacks.
func TestEntryIDFromLocation(t *testing.T) {
	cases := map[string]string{
		"https://h:8008/api/arsys/v1/entry/HPD:IncidentInterface_Create/INC000000000101": "INC000000000101",
		"":            "",
		"INC42":       "INC42",
		"/entry/F/ID": "ID",
		"%zz":         "%zz", // an unparseable Location falls back to the raw last segment
	}
	for loc, want := range cases {
		if got := entryIDFromLocation(loc); got != want {
			t.Errorf("entryIDFromLocation(%q) = %q, want %q", loc, got, want)
		}
	}
}

// TestURLJoin covers the base-URL join tolerating a trailing slash.
func TestURLJoin(t *testing.T) {
	c := NewHTTPClient(Connector{BaseURL: "https://h:8008/"})
	if got := c.url("/api/jwt/login"); got != "https://h:8008/api/jwt/login" {
		t.Errorf("url = %q, want the trailing slash trimmed", got)
	}
}
