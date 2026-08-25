package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// evalAttrs compiles an inline attributes template and evaluates it against the given
// variables, returning the JSON object the worker would send as the request body.
func evalAttrs(t *testing.T, raw string, vars map[string]expr.Value) map[string]any {
	t.Helper()
	re, err := entraAttributesExpr("t", raw)
	if err != nil {
		t.Fatalf("entraAttributesExpr: %v", err)
	}
	if re.Expr == nil {
		t.Fatalf("attributes compiled to no expression")
	}
	v, err := re.Expr.Eval(vars)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	kind, _, text := expr.Classify(v)
	if kind != expr.KindJSON {
		t.Fatalf("attributes evaluated to kind %v (%q), want a JSON object", kind, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("attributes JSON %q did not parse: %v", text, err)
	}
	return out
}

// A mixed template: literal booleans, FEEL leaves over the instance's variables, and a
// nested passwordProfile — the shape a create-user body actually takes.
func TestEntraInlineAttributesEvaluateFeelLeaves(t *testing.T) {
	raw := `{
		"accountEnabled": true,
		"displayName": "=vorname + \" \" + nachname",
		"mailNickname": "=nick",
		"passwordProfile": { "password": "=pw", "forceChangePasswordNextSignIn": true }
	}`
	got := evalAttrs(t, raw, map[string]expr.Value{
		"vorname":  expr.String("Arno"),
		"nachname": expr.String("Muster"),
		"nick":     expr.String("arno"),
		"pw":       expr.String("Temp123!"),
	})
	if got["accountEnabled"] != true || got["displayName"] != "Arno Muster" || got["mailNickname"] != "arno" {
		t.Errorf("top-level attributes = %#v", got)
	}
	pp, ok := got["passwordProfile"].(map[string]any)
	if !ok || pp["password"] != "Temp123!" || pp["forceChangePasswordNextSignIn"] != true {
		t.Errorf("passwordProfile = %#v", got["passwordProfile"])
	}
}

// A template with no FEEL leaf is a constant body: it still compiles and evaluates to
// exactly the object authored, so a fixed group can be created without any variable.
func TestEntraInlineAttributesLiteralOnly(t *testing.T) {
	got := evalAttrs(t, `{"displayName":"Projekt X","securityEnabled":true,"groupTypes":["Unified"]}`, nil)
	if got["displayName"] != "Projekt X" || got["securityEnabled"] != true {
		t.Errorf("attributes = %#v", got)
	}
	types, ok := got["groupTypes"].([]any)
	if !ok || len(types) != 1 || types[0] != "Unified" {
		t.Errorf("groupTypes = %#v", got["groupTypes"])
	}
}

// The failure paths a modeler can hit are named at deploy, not left to a Graph 400.
func TestEntraInlineAttributesRejectsBadTemplates(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"invalid json", `{"displayName": }`, "invalid attributes JSON"},
		{"not an object", `["a","b"]`, "must be a JSON object"},
		{"empty feel leaf", `{"displayName":"="}`, "empty =expression"},
		{"malformed feel", `{"displayName":"=a +"}`, "did not compile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := entraAttributesExpr("t", tc.raw)
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	// An empty template is not an error: it means "use the attributesVariable instead".
	if re, err := entraAttributesExpr("t", "  "); err != nil || re.Expr != nil {
		t.Errorf("empty template = (%+v, %v), want the zero RestExpr and no error", re, err)
	}
}
