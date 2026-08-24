package scim

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// TestMethodForOp pins each SCIM operation to its HTTP method and body-carrying
// flag: create/replace/patch send a payload, get/delete/search do not.
func TestMethodForOp(t *testing.T) {
	cases := []struct {
		op     string
		method string
		body   bool
	}{
		{"create", "POST", true},
		{"replace", "PUT", true},
		{"patch", "PATCH", true},
		{"get", "GET", false},
		{"delete", "DELETE", false},
		{"search", "GET", false},
	}
	for _, c := range cases {
		if m, b := methodForOp(c.op); m != c.method || b != c.body {
			t.Errorf("methodForOp(%q) = %q,%v, want %q,%v", c.op, m, b, c.method, c.body)
		}
	}
}

// TestResourceURL assembles base + resource, appends /{id} (path-escaped) for a
// single-resource operation, and errors when a required part is missing.
func TestResourceURL(t *testing.T) {
	base := "https://idp.example.com/scim/v2/"
	// create/search operate on the collection, no id.
	if got, err := resourceURL("create", base, "Users", ""); err != nil || got != "https://idp.example.com/scim/v2/Users" {
		t.Errorf("create url = %q,%v, want the collection URL", got, err)
	}
	if got, err := resourceURL("search", base, "/Groups/", ""); err != nil || got != "https://idp.example.com/scim/v2/Groups" {
		t.Errorf("search url = %q,%v, want the trimmed collection URL", got, err)
	}
	// get/replace/patch/delete append the escaped id.
	got, err := resourceURL("get", base, "Users", "user 7/x")
	if err != nil || got != "https://idp.example.com/scim/v2/Users/user%207%2Fx" {
		t.Errorf("get url = %q,%v, want the escaped resource URL", got, err)
	}
	// Missing id on a single-resource op is an error.
	if _, err := resourceURL("delete", base, "Users", "  "); err == nil {
		t.Error("resourceURL(delete, no id): err = nil, want error")
	}
	// Missing base/resource is an error (guards a stale job).
	if _, err := resourceURL("create", "", "Users", ""); err == nil {
		t.Error("resourceURL(no base): err = nil, want error")
	}
	if _, err := resourceURL("create", base, "", ""); err == nil {
		t.Error("resourceURL(no resource): err = nil, want error")
	}
}

// TestRequestBody covers the payload source: the whole scope by default, a named
// body variable when set (which must be a JSON object), and its error cases.
func TestRequestBody(t *testing.T) {
	scope := map[string]model.VariableValue{
		"userName": {Name: "userName", Kind: model.VarString, Text: "ada"},
		"resource": {Name: "resource", Kind: model.VarJSON, Text: `{"userName":"ada","active":true}`},
		"bad":      {Name: "bad", Kind: model.VarString, Text: "not-an-object"},
	}
	// Default: the whole scope becomes the payload.
	whole, err := requestBody("", scope)
	if err != nil || whole["userName"] != "ada" {
		t.Errorf("requestBody(whole scope) = %#v,%v, want the scope map", whole, err)
	}
	// Named body variable: its JSON object is sent verbatim.
	obj, err := requestBody("resource", scope)
	if err != nil || obj["userName"] != "ada" || obj["active"] != true {
		t.Errorf("requestBody(named) = %#v,%v, want the resource object", obj, err)
	}
	// A named body variable that is not set is an error.
	if _, err := requestBody("missing", scope); err == nil {
		t.Error("requestBody(missing var): err = nil, want error")
	}
	// A named body variable that is not a JSON object is an error.
	if _, err := requestBody("bad", scope); err == nil {
		t.Error("requestBody(non-object var): err = nil, want error")
	}
}

// TestScimErrorDetail extracts the SCIM error detail (with scimType when present),
// and returns "" for a body that is not a recognizable SCIM error.
func TestScimErrorDetail(t *testing.T) {
	withType := []byte(`{"schemas":["urn:ietf:params:scim:api:messages:2.0:Error"],"scimType":"uniqueness","detail":"userName already exists","status":"409"}`)
	if got := scimErrorDetail(withType); got != ": uniqueness: userName already exists" {
		t.Errorf("detail = %q, want the scimType + detail", got)
	}
	plain := []byte(`{"detail":"not found"}`)
	if got := scimErrorDetail(plain); got != ": not found" {
		t.Errorf("detail = %q, want the plain detail", got)
	}
	if got := scimErrorDetail([]byte(`not json`)); got != "" {
		t.Errorf("detail(non-json) = %q, want empty", got)
	}
	if got := scimErrorDetail([]byte(`{"status":"500"}`)); got != "" {
		t.Errorf("detail(no detail field) = %q, want empty", got)
	}
}

// TestVarToAnyJSON checks a structured variable re-parses into a nested object with
// exact numbers, and unparseable stored JSON degrades to nil.
func TestVarToAnyJSON(t *testing.T) {
	got := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: `{"id":7,"emails":["a@x"]}`})
	obj, ok := got.(map[string]any)
	if !ok || obj["id"] != json.Number("7") {
		t.Errorf("varToAny(json) = %#v, want a nested object with id 7", got)
	}
	if got := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: "{bad"}); got != nil {
		t.Errorf("varToAny(bad json) = %#v, want nil", got)
	}
}

// TestResponseVariable checks the response-to-variable mapping: a scalar stays a
// scalar and an object becomes a structured VarJSON.
func TestResponseVariable(t *testing.T) {
	if v := responseVariable("r", "id-1"); v.Kind != model.VarString || v.Text != "id-1" {
		t.Errorf("scalar response = %+v, want a string var", v)
	}
	v := responseVariable("r", map[string]any{"id": json.Number("7")})
	if v.Kind != model.VarJSON {
		t.Errorf("object response kind = %v, want VarJSON", v.Kind)
	}
	if v := responseVariable("r", nil); v.Kind != model.VarNull {
		t.Errorf("nil response kind = %v, want VarNull", v.Kind)
	}
}

// TestResolveValue covers literal pass-through, FEEL string coercion over variables,
// and null-propagation (an absent variable yields the empty string).
func TestResolveValue(t *testing.T) {
	vars := map[string]model.VariableValue{
		"id": {Name: "id", Kind: model.VarString, Text: "u-7"},
	}
	if got := resolveValue(compiler.RestExpr{Literal: "Users"}, 1, vars); got != "Users" {
		t.Errorf("literal = %q, want the literal", got)
	}
	e, err := expr.CompileAuto(`id`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := resolveValue(compiler.RestExpr{Expr: e}, 1, vars); got != "u-7" {
		t.Errorf("feel = %q, want u-7", got)
	}
	em, _ := expr.CompileAuto(`missing`)
	if got := resolveValue(compiler.RestExpr{Expr: em}, 1, vars); got != "" {
		t.Errorf("feel null = %q, want empty", got)
	}
}

// TestBindVars binds the processInstanceKey builtin and scope variables of every
// kind (exercising toExprKind), and leaves an unknown name unbound.
func TestBindVars(t *testing.T) {
	vars := map[string]model.VariableValue{
		"s": {Name: "s", Kind: model.VarString, Text: "x"},
		"n": {Name: "n", Kind: model.VarNumber, Text: "1"},
		"b": {Name: "b", Kind: model.VarBool, Bool: true},
		"j": {Name: "j", Kind: model.VarJSON, Text: `{"a":1}`},
		"z": {Name: "z", Kind: model.VarNull},
	}
	m := bindVars(7, vars, []string{builtinProcessInstanceKey, "s", "n", "b", "j", "z", "missing"})
	if len(m) != 6 {
		t.Fatalf("bound %d names, want 6 (missing left unbound)", len(m))
	}
	if bindVars(1, vars, nil) != nil {
		t.Error("bindVars(no names) should be nil")
	}
}

// TestToExprKind covers the stored-kind → expr-kind mapping directly.
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

func TestAuthHeader(t *testing.T) {
	if n, v := authHeader(compilerAuth("bearer", "", ""), "tok"); n != "Authorization" || v != "Bearer tok" {
		t.Errorf("bearer = %q,%q", n, v)
	}
	if n, v := authHeader(compilerAuth("basic", "u", ""), "p"); n != "Authorization" || v != "Basic dTpw" {
		t.Errorf("basic = %q,%q, want base64(u:p)=dTpw", n, v)
	}
	if n, v := authHeader(compilerAuth("apikey", "", "X-Key"), "k"); n != "X-Key" || v != "k" {
		t.Errorf("apikey = %q,%q", n, v)
	}
	if n, _ := authHeader(compilerAuth("nope", "", ""), "x"); n != "" {
		t.Errorf("unknown scheme name = %q, want empty", n)
	}
}

func TestApplyAuth(t *testing.T) {
	if h, err := applyAuth(nil, "", noResolver); err != nil || h != nil {
		t.Errorf("applyAuth(none) = %v,%v, want nil,nil", h, err)
	}
	if _, err := applyAuth(nil, "{bad", noResolver); err == nil {
		t.Error("applyAuth(bad json): err = nil, want error")
	}
	if h, err := applyAuth(nil, `{"type":""}`, noResolver); err != nil || h != nil {
		t.Errorf("applyAuth(empty type) = %v,%v, want nil,nil", h, err)
	}
	if _, err := applyAuth(nil, `{"type":"bearer","secretRef":"X"}`, noResolver); err == nil {
		t.Error("applyAuth(missing secret): err = nil, want error")
	}
	h, err := applyAuth(nil, `{"type":"bearer","secretRef":"X"}`, func(string) string { return "tok" })
	if err != nil || h["Authorization"] != "Bearer tok" {
		t.Errorf("applyAuth(bearer) = %v,%v, want Authorization header", h, err)
	}
	if _, err := applyAuth(nil, `{"type":"weird","secretRef":"X"}`, func(string) string { return "tok" }); err == nil {
		t.Error("applyAuth(unknown scheme): err = nil, want error")
	}
}

func noResolver(string) string { return "" }

func compilerAuth(typ, user, apiKeyName string) compiler.RestAuth {
	return compiler.RestAuth{Type: typ, Username: user, ApiKeyName: apiKeyName}
}

// TestWithQuery merges a search filter into the URL and errors on an unparseable one.
func TestWithQuery(t *testing.T) {
	if got, err := withQuery("https://x/Users", nil); err != nil || got != "https://x/Users" {
		t.Errorf("withQuery(no params) = %q,%v, want unchanged", got, err)
	}
	got, err := withQuery("https://x/Users", map[string]string{"filter": `userName eq "ada"`})
	if err != nil || !strings.Contains(got, "filter=") {
		t.Errorf("withQuery = %q,%v, want a filter query", got, err)
	}
	if _, err := withQuery("://bad", map[string]string{"filter": "x"}); err == nil {
		t.Error("withQuery(unparseable): err = nil, want error")
	}
}

// TestHTTPClientDoCreate checks a create carries the scim+json body and the
// idempotency header, and decodes the JSON resource response.
func TestHTTPClientDoCreate(t *testing.T) {
	var gotMethod, gotIdem, gotCT, gotAccept string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotIdem = r.Header.Get("Idempotency-Key")
		gotCT = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"u-42","userName":"ada"}`))
	}))
	defer srv.Close()

	c := NewHTTPClient()
	resp, err := c.Do(context.Background(), Request{
		Method:         "POST",
		URL:            srv.URL + "/Users",
		Body:           map[string]any{"userName": "ada"},
		IdempotencyKey: "99",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotMethod != "POST" || gotIdem != "99" {
		t.Errorf("method/idem = %s/%s, want POST/99", gotMethod, gotIdem)
	}
	if gotCT != scimContentType || gotAccept != scimContentType {
		t.Errorf("content-type/accept = %q/%q, want %q", gotCT, gotAccept, scimContentType)
	}
	if gotBody["userName"] != "ada" {
		t.Errorf("body userName = %v, want ada", gotBody["userName"])
	}
	obj, ok := resp.Body.(map[string]any)
	if !ok || obj["id"] != "u-42" {
		t.Errorf("response body = %#v, want a map with id u-42", resp.Body)
	}
}

// TestHTTPClientDeleteEmpty checks a 204 delete sends no body and decodes to nil.
func TestHTTPClientDeleteEmpty(t *testing.T) {
	var hadBody bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		hadBody = len(raw) > 0
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := NewHTTPClient()
	resp, err := c.Do(context.Background(), Request{Method: "DELETE", URL: srv.URL + "/Users/u-1"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if hadBody {
		t.Error("DELETE sent a body, want none")
	}
	if resp.Body != nil {
		t.Errorf("empty response body = %#v, want nil", resp.Body)
	}
}

// TestHTTPClientNon2xxCarriesDetail surfaces a SCIM error response as an error whose
// message includes the provider's detail, so the parked incident is legible.
func TestHTTPClientNon2xxCarriesDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"scimType":"uniqueness","detail":"userName already exists"}`))
	}))
	defer srv.Close()
	c := NewHTTPClient()
	_, err := c.Do(context.Background(), Request{Method: "POST", URL: srv.URL + "/Users", Body: map[string]any{"a": 1}})
	if err == nil || !strings.Contains(err.Error(), "userName already exists") {
		t.Fatalf("Do on 409 err = %v, want an error carrying the SCIM detail", err)
	}
}

// TestHTTPClientSearchFilter checks a search sends its filter as a query parameter.
func TestHTTPClientSearchFilter(t *testing.T) {
	var gotFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFilter = r.URL.Query().Get("filter")
		_, _ = w.Write([]byte(`{"totalResults":0,"Resources":[]}`))
	}))
	defer srv.Close()
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{
		Method: "GET", URL: srv.URL + "/Users",
		Query: map[string]string{"filter": `userName eq "ada"`},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotFilter != `userName eq "ada"` {
		t.Errorf("filter = %q, want the SCIM filter", gotFilter)
	}
}

// TestHTTPClientEncodeError covers the body-encode failure branch.
func TestHTTPClientEncodeError(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "POST", URL: "http://example.invalid/Users", Body: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("Do with an unmarshalable body: err = nil, want error")
	}
}

// TestHTTPClientUnreachable surfaces a transport error, so the job retries.
func TestHTTPClientUnreachable(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "GET", URL: "http://127.0.0.1:1/Users"}); err == nil {
		t.Fatal("Do to an unreachable endpoint: err = nil, want error")
	}
}

// TestVarToAnyScalars covers every stored-kind arm of varToAny.
func TestVarToAnyScalars(t *testing.T) {
	cases := []struct {
		v    model.VariableValue
		want any
	}{
		{model.VariableValue{Kind: model.VarString, Text: "hi"}, "hi"},
		{model.VariableValue{Kind: model.VarBool, Bool: true}, true},
		{model.VariableValue{Kind: model.VarNumber, Text: "42"}, json.Number("42")},
		{model.VariableValue{Kind: model.VarNull}, nil},
	}
	for _, c := range cases {
		if got := varToAny(&c.v); got != c.want {
			t.Errorf("varToAny(%+v) = %#v, want %#v", c.v, got, c.want)
		}
	}
}

// TestToVarKind covers the expr-kind → stored-kind mapping directly, including the
// null default.
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

// TestResponseVariableScalars covers the bool and number response arms (exercising
// toVarKind through responseVariable).
func TestResponseVariableScalars(t *testing.T) {
	if v := responseVariable("r", true); v.Kind != model.VarBool || !v.Bool {
		t.Errorf("bool response = %+v, want a true bool var", v)
	}
	if v := responseVariable("r", json.Number("42")); v.Kind != model.VarNumber || v.Text != "42" {
		t.Errorf("number response = %+v, want the number 42", v)
	}
}

// TestHTTPClientQueryError covers Do's query-append failure branch: an unparseable
// URL with a filter errors before any call.
func TestHTTPClientQueryError(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "GET", URL: "://bad", Query: map[string]string{"filter": "x"}}); err == nil {
		t.Fatal("Do with an unparseable URL + query: err = nil, want error")
	}
}

// TestHTTPClientBuildError covers the request-build failure branch: an invalid method
// token cannot form a request.
func TestHTTPClientBuildError(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "BAD METHOD", URL: "http://example.invalid/Users"}); err == nil {
		t.Fatal("Do with an invalid method: err = nil, want error")
	}
}

// TestHTTPClientModelContentType covers the branch where a model header sets
// Content-Type on a body request, so the scim+json default is not applied.
func TestHTTPClientModelContentType(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{
		Method: "PATCH", URL: srv.URL + "/Users/1",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    map[string]any{"a": 1},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want the model override", gotCT)
	}
}

// TestDecodeBody covers empty→nil, JSON→value, and non-JSON→raw text.
func TestDecodeBody(t *testing.T) {
	if got := decodeBody(nil); got != nil {
		t.Errorf("decodeBody(empty) = %#v, want nil", got)
	}
	if got := decodeBody([]byte(`plain`)); got != "plain" {
		t.Errorf("decodeBody(non-json) = %#v, want raw text", got)
	}
	obj, ok := decodeBody([]byte(`{"id":"x"}`)).(map[string]any)
	if !ok || obj["id"] != "x" {
		t.Errorf("decodeBody(json) = %#v, want a map", obj)
	}
}
