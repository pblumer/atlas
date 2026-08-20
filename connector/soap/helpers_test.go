package soap

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// TestAuthHeader covers every authentication scheme's header, including the unknown
// scheme that yields an empty name (which applyAuth turns into an error).
func TestAuthHeader(t *testing.T) {
	cases := []struct {
		a          compiler.RestAuth
		secret     string
		wantName   string
		wantValue  string
		wantNoName bool
	}{
		{compiler.RestAuth{Type: "basic", Username: "u"}, "p", "Authorization", "Basic dTpw", false},
		{compiler.RestAuth{Type: "bearer"}, "tok", "Authorization", "Bearer tok", false},
		{compiler.RestAuth{Type: "apikey", ApiKeyName: "X-Key"}, "k", "X-Key", "k", false},
		{compiler.RestAuth{Type: "weird"}, "x", "", "", true},
	}
	for _, c := range cases {
		name, value := authHeader(c.a, c.secret)
		if c.wantNoName {
			if name != "" {
				t.Errorf("authHeader(%q) name = %q, want empty", c.a.Type, name)
			}
			continue
		}
		if name != c.wantName || value != c.wantValue {
			t.Errorf("authHeader(%q) = %q/%q, want %q/%q", c.a.Type, name, value, c.wantName, c.wantValue)
		}
	}
}

// TestApplyAuthBranches covers the remaining applyAuth paths: no auth, an empty type, a
// malformed encoding, an unsupported scheme, and a resolved bearer that does not clobber
// an existing header.
func TestApplyAuthBranches(t *testing.T) {
	if h, err := applyAuth(nil, "", func(string) string { return "" }); h != nil || err != nil {
		t.Errorf("no-auth applyAuth = %v/%v, want nil/nil", h, err)
	}
	if _, err := applyAuth(nil, `{"type":""}`, nil); err != nil {
		t.Errorf("empty-type applyAuth err = %v, want nil", err)
	}
	if _, err := applyAuth(nil, `{not json`, nil); err == nil {
		t.Error("malformed applyAuth err = nil, want a decode error")
	}
	// A scheme authHeader does not know fails after the secret resolves.
	if _, err := applyAuth(nil, `{"type":"weird","secretRef":"R"}`, func(string) string { return "v" }); err == nil {
		t.Error("unsupported-scheme applyAuth err = nil, want an error")
	}
	// An explicit header of the same name is not overwritten.
	h, err := applyAuth(map[string]string{"Authorization": "keep"}, `{"type":"bearer","secretRef":"R"}`, func(string) string { return "v" })
	if err != nil || h["Authorization"] != "keep" {
		t.Errorf("applyAuth over an existing header = %v/%v, want it kept", h, err)
	}
}

// TestExprKindMappings covers both direction switch tables across every kind, including
// the null defaults.
func TestExprKindMappings(t *testing.T) {
	fwd := map[model.VarKind]expr.ValueKind{
		model.VarBool:   expr.KindBool,
		model.VarNumber: expr.KindNumber,
		model.VarString: expr.KindString,
		model.VarJSON:   expr.KindJSON,
		model.VarNull:   expr.KindNull,
	}
	for in, want := range fwd {
		if got := toExprKind(in); got != want {
			t.Errorf("toExprKind(%v) = %v, want %v", in, got, want)
		}
	}
	back := map[expr.ValueKind]model.VarKind{
		expr.KindBool:   model.VarBool,
		expr.KindNumber: model.VarNumber,
		expr.KindString: model.VarString,
		expr.KindJSON:   model.VarJSON,
		expr.KindNull:   model.VarNull,
	}
	for in, want := range back {
		if got := toVarKind(in); got != want {
			t.Errorf("toVarKind(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestBindVars covers the binding branches: no names is nil, the reserved
// processInstanceKey binds the scope, a present variable binds its value, and an absent
// one is left unbound.
func TestBindVars(t *testing.T) {
	if bindVars(1, nil, nil) != nil {
		t.Error("bindVars with no names, want nil")
	}
	vars := map[string]model.VariableValue{"here": {Name: "here", Kind: model.VarString, Text: "x"}}
	m := bindVars(42, vars, []string{builtinProcessInstanceKey, "here", "absent"})
	if m[builtinProcessInstanceKey] != expr.String("42") {
		t.Errorf("processInstanceKey binding = %v, want 42", m[builtinProcessInstanceKey])
	}
	if _, ok := m["here"]; !ok {
		t.Error("present variable was not bound")
	}
	if _, ok := m["absent"]; ok {
		t.Error("absent variable was bound, want it left unbound (FEEL null)")
	}
}

// TestResolveValueFeelError proves a FEEL expression that fails to evaluate resolves to
// the empty string (the null-propagating contract), not an error.
func TestResolveValueFeelError(t *testing.T) {
	e, err := expr.CompileAuto(`1 / 0`)
	if err != nil {
		t.Skipf("division is a compile error here, not a runtime one: %v", err)
	}
	if got := resolveValue(compiler.RestExpr{Expr: e}, 1, nil); got != "" {
		t.Errorf("resolveValue of a failing FEEL expr = %q, want empty", got)
	}
}

// TestResponseVariableClassifies proves the response value round-trips through the expr
// canonicalization: a nested map becomes a VarJSON, a leaf string a VarString, nil a null.
func TestResponseVariableClassifies(t *testing.T) {
	if v := responseVariable("r", map[string]any{"a": "b"}); v.Kind != model.VarJSON {
		t.Errorf("map response kind = %v, want VarJSON", v.Kind)
	}
	if v := responseVariable("r", "text"); v.Kind != model.VarString || v.Text != "text" {
		t.Errorf("string response = %+v, want a VarString text", v)
	}
	if v := responseVariable("r", nil); v.Kind != model.VarNull {
		t.Errorf("nil response kind = %v, want VarNull", v.Kind)
	}
}

// TestAddChildPromotesToArray covers the three arms: first value, promotion to array on
// the second, and append on the third.
func TestAddChildPromotesToArray(t *testing.T) {
	m := map[string]any{}
	addChild(m, "x", "1")
	if m["x"] != "1" {
		t.Fatalf("first add = %v, want the scalar", m["x"])
	}
	addChild(m, "x", "2")
	addChild(m, "x", "3")
	arr, ok := m["x"].([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("after three adds = %#v, want a 3-element array", m["x"])
	}
}

// TestFaultMessageBranches covers each fault shape: 1.1 code+string, 1.1 code only, 1.2
// reason only, and the non-map fallbacks.
func TestFaultMessageBranches(t *testing.T) {
	cases := []struct {
		val  any
		want string
	}{
		{map[string]any{"faultcode": "Client", "faultstring": "bad"}, "Client: bad"},
		{map[string]any{"faultcode": "Server"}, "Server"},
		{map[string]any{"Reason": map[string]any{"Text": "why"}}, "why"},
		{map[string]any{}, "unknown fault"},
		{123, "unknown fault"},
	}
	for _, c := range cases {
		if got := faultMessage(c.val); got != c.want {
			t.Errorf("faultMessage(%#v) = %q, want %q", c.val, got, c.want)
		}
	}
}

// TestSnippetTruncates proves a long non-envelope body is trimmed with an ellipsis.
func TestSnippetTruncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := snippet([]byte(long))
	if len(got) <= 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("snippet length = %d, want a truncated string ending in …", len(got))
	}
}

// TestParseResponseTruncated proves a truncated envelope (the Body never closes) is a
// parse error, so the job fails rather than acting on a half-read response.
func TestParseResponseTruncated(t *testing.T) {
	raw := []byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><Open>`)
	if _, _, err := parseResponse(raw); err == nil {
		t.Error("parseResponse of a truncated envelope = nil error, want a failure")
	}
}
