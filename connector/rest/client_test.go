package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

func TestVarToAny(t *testing.T) {
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

// TestVarToAnyJSON checks that a structured variable is re-parsed into a nested
// object/array (with exact numbers), and that unparseable stored JSON degrades to
// nil.
func TestVarToAnyJSON(t *testing.T) {
	got := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: `{"id":7,"tags":["a","b"]}`})
	want := map[string]any{"id": json.Number("7"), "tags": []any{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("varToAny(json) = %#v, want %#v", got, want)
	}
	if got := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: "{bad"}); got != nil {
		t.Errorf("varToAny(bad json) = %#v, want nil", got)
	}
}

func TestMethodHasBody(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "PATCH"} {
		if !methodHasBody(m) {
			t.Errorf("methodHasBody(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"GET", "DELETE", "HEAD"} {
		if methodHasBody(m) {
			t.Errorf("methodHasBody(%q) = true, want false", m)
		}
	}
}

// TestResponseVariable checks the response-to-variable mapping: a scalar stays a
// scalar and an object becomes a structured VarJSON.
func TestResponseVariable(t *testing.T) {
	if v := responseVariable("r", "hello"); v.Kind != model.VarString || v.Text != "hello" {
		t.Errorf("scalar response = %+v, want a string var", v)
	}
	if v := responseVariable("r", true); v.Kind != model.VarBool || !v.Bool {
		t.Errorf("bool response = %+v, want a true bool var", v)
	}
	if v := responseVariable("r", json.Number("42")); v.Kind != model.VarNumber || v.Text != "42" {
		t.Errorf("number response = %+v, want the number 42", v)
	}
	v := responseVariable("r", map[string]any{"id": json.Number("7")})
	if v.Kind != model.VarJSON {
		t.Errorf("object response kind = %v, want VarJSON", v.Kind)
	}
	if v := responseVariable("r", nil); v.Kind != model.VarNull {
		t.Errorf("nil response kind = %v, want VarNull", v.Kind)
	}
}

// TestHTTPClientDoPost checks that a POST carries the JSON body and the
// idempotency header, and that a JSON response is decoded.
func TestHTTPClientDoPost(t *testing.T) {
	var gotMethod, gotPath, gotIdem, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotIdem = r.Header.Get("Idempotency-Key")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()

	c := NewHTTPClient()
	resp, err := c.Do(context.Background(), Request{
		Method:         "POST",
		URL:            srv.URL + "/customers",
		Body:           map[string]any{"name": "Ada"},
		IdempotencyKey: "99",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/customers" {
		t.Errorf("method/path = %s %s, want POST /customers", gotMethod, gotPath)
	}
	if gotIdem != "99" {
		t.Errorf("Idempotency-Key = %q, want 99", gotIdem)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotBody["name"] != "Ada" {
		t.Errorf("body name = %v, want Ada", gotBody["name"])
	}
	if resp.Status != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.Status)
	}
	obj, ok := resp.Body.(map[string]any)
	if !ok || obj["id"] != json.Number("42") {
		t.Errorf("response body = %#v, want a map with id 42", resp.Body)
	}
}

// TestHTTPClientDoGet checks that a GET sends no body and no Content-Type, and
// that a non-JSON body is surfaced as raw text.
func TestHTTPClientDoGet(t *testing.T) {
	var hadBody bool
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		hadBody = len(raw) > 0
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte("plain text"))
	}))
	defer srv.Close()

	c := NewHTTPClient()
	resp, err := c.Do(context.Background(), Request{Method: "GET", URL: srv.URL + "/ping"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if hadBody {
		t.Error("GET sent a request body, want none")
	}
	if gotCT != "" {
		t.Errorf("GET Content-Type = %q, want empty", gotCT)
	}
	if resp.Body != "plain text" {
		t.Errorf("response body = %#v, want the raw text", resp.Body)
	}
}

// TestHTTPClientEmptyResponse checks that an empty 2xx body decodes to nil.
func TestHTTPClientEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := NewHTTPClient()
	resp, err := c.Do(context.Background(), Request{Method: "DELETE", URL: srv.URL + "/x"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Body != nil {
		t.Errorf("empty response body = %#v, want nil", resp.Body)
	}
}

// TestHTTPClientNon2xx surfaces a non-2xx response as an error, so the job stays
// pending and is retried (at-least-once).
func TestHTTPClientNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "GET", URL: srv.URL + "/x"}); err == nil {
		t.Fatal("Do on HTTP 500: err = nil, want error")
	}
}

// TestHTTPClientUnreachable surfaces a transport error as an error, so the job
// stays pending and retries.
func TestHTTPClientUnreachable(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "GET", URL: "http://127.0.0.1:1/x"}); err == nil {
		t.Fatal("Do to an unreachable endpoint: err = nil, want error")
	}
}

// TestHTTPClientEncodeError covers the body-encode failure branch: a value JSON
// cannot marshal leaves the request unsent.
func TestHTTPClientEncodeError(t *testing.T) {
	c := NewHTTPClient()
	_, err := c.Do(context.Background(), Request{Method: "POST", URL: "http://example.invalid/x", Body: map[string]any{"bad": make(chan int)}})
	if err == nil {
		t.Fatal("Do with an unmarshalable body: err = nil, want error")
	}
}

// TestHTTPClientBuildError covers the request-build failure branch: an invalid
// method token cannot form a request.
func TestHTTPClientBuildError(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "BAD METHOD", URL: "http://example.invalid/x"}); err == nil {
		t.Fatal("Do with an invalid method: err = nil, want error")
	}
}

// TestWithQuery merges connector query parameters with any already in the URL and
// encodes deterministically; an empty map leaves the URL untouched.
func TestWithQuery(t *testing.T) {
	if got, err := withQuery("https://x/y?a=1", nil); err != nil || got != "https://x/y?a=1" {
		t.Errorf("withQuery(no params) = %q,%v, want the URL unchanged", got, err)
	}
	got, err := withQuery("https://x/y?a=1", map[string]string{"b": "2", "c": "3"})
	if err != nil {
		t.Fatalf("withQuery: %v", err)
	}
	if got != "https://x/y?a=1&b=2&c=3" {
		t.Errorf("withQuery = %q, want a=1&b=2&c=3 (sorted, merged)", got)
	}
	if _, err := withQuery("://bad", map[string]string{"b": "2"}); err == nil {
		t.Error("withQuery on an unparseable URL: err = nil, want error")
	}
}

// TestHTTPClientHeadersAndQuery checks the client sends model headers and query,
// and that a model header overrides the default Accept.
func TestHTTPClientHeadersAndQuery(t *testing.T) {
	var gotAccept, gotCustom, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotCustom = r.Header.Get("X-Custom")
		gotQuery = r.URL.Query().Get("page")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewHTTPClient()
	_, err := c.Do(context.Background(), Request{
		Method:  "GET",
		URL:     srv.URL + "/todos",
		Headers: map[string]string{"X-Custom": "hi", "Accept": "text/plain"},
		Query:   map[string]string{"page": "2"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCustom != "hi" {
		t.Errorf("X-Custom = %q, want hi", gotCustom)
	}
	if gotAccept != "text/plain" {
		t.Errorf("Accept = %q, want the model override text/plain", gotAccept)
	}
	if gotQuery != "2" {
		t.Errorf("query page = %q, want 2", gotQuery)
	}
}

// TestResolveValue covers literal pass-through, FEEL string/number coercion over
// variables, and null-propagation (an absent variable yields the empty string).
func TestResolveValue(t *testing.T) {
	vars := map[string]model.VariableValue{
		"customerId": {Name: "customerId", Kind: model.VarString, Text: "c-7"},
		"qty":        {Name: "qty", Kind: model.VarNumber, Text: "42"},
	}
	// Literal: returned verbatim, no evaluation.
	if got := resolveValue(compiler.RestExpr{Literal: "https://x/y"}, 1, vars); got != "https://x/y" {
		t.Errorf("literal = %q, want the literal", got)
	}
	// FEEL string concatenation over a variable.
	e, err := expr.CompileAuto(`"https://api/" + customerId`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := resolveValue(compiler.RestExpr{Expr: e}, 1, vars); got != "https://api/c-7" {
		t.Errorf("feel string = %q, want https://api/c-7", got)
	}
	// A FEEL number coerces to its decimal string form.
	en, _ := expr.CompileAuto(`qty`)
	if got := resolveValue(compiler.RestExpr{Expr: en}, 1, vars); got != "42" {
		t.Errorf("feel number = %q, want 42", got)
	}
	// An absent variable evaluates to FEEL null → empty string (null-propagating).
	em, _ := expr.CompileAuto(`missing`)
	if got := resolveValue(compiler.RestExpr{Expr: em}, 1, vars); got != "" {
		t.Errorf("feel null = %q, want empty", got)
	}
}

// TestResolveKVs evaluates a list of named values (literal and FEEL) into a map.
func TestResolveKVs(t *testing.T) {
	if m := resolveKVs(nil, 1, nil); m != nil {
		t.Errorf("resolveKVs(nil) = %v, want nil", m)
	}
	e, _ := expr.CompileAuto(`"1"`)
	m := resolveKVs([]compiler.RestKV{
		{Name: "X", Val: compiler.RestExpr{Literal: "lit"}},
		{Name: "page", Val: compiler.RestExpr{Expr: e}},
	}, 1, nil)
	if m["X"] != "lit" || m["page"] != "1" {
		t.Errorf("resolveKVs = %v, want {X:lit, page:1}", m)
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
	if len(m) != 6 { // the builtin + five present vars; "missing" left unbound
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
	// No auth encoded → no change, nil map stays nil.
	if h, err := applyAuth(nil, "", noResolver); err != nil || h != nil {
		t.Errorf("applyAuth(none) = %v,%v, want nil,nil", h, err)
	}
	// Bad JSON → error.
	if _, err := applyAuth(nil, "{bad", noResolver); err == nil {
		t.Error("applyAuth(bad json): err = nil, want error")
	}
	// Empty type → no-op.
	if h, err := applyAuth(nil, `{"type":""}`, noResolver); err != nil || h != nil {
		t.Errorf("applyAuth(empty type) = %v,%v, want nil,nil", h, err)
	}
	// Missing secret → error.
	if _, err := applyAuth(nil, `{"type":"bearer","secretRef":"X"}`, noResolver); err == nil {
		t.Error("applyAuth(missing secret): err = nil, want error")
	}
	// Resolves and allocates a header map even when none was passed.
	h, err := applyAuth(nil, `{"type":"bearer","secretRef":"X"}`, func(string) string { return "tok" })
	if err != nil || h["Authorization"] != "Bearer tok" {
		t.Errorf("applyAuth(bearer) = %v,%v, want Authorization header", h, err)
	}
	// An explicit model header of the same name is not clobbered.
	h2, err := applyAuth(map[string]string{"Authorization": "keep"}, `{"type":"bearer","secretRef":"X"}`, func(string) string { return "tok" })
	if err != nil || h2["Authorization"] != "keep" {
		t.Errorf("applyAuth over an existing header = %v, want it preserved", h2)
	}
}

func noResolver(string) string { return "" }

// compilerAuth is a small ctor to keep the auth-header cases readable.
func compilerAuth(typ, user, apiKeyName string) compiler.RestAuth {
	return compiler.RestAuth{Type: typ, Username: user, ApiKeyName: apiKeyName}
}

// TestHTTPClientQueryError covers Do's query-append failure branch: an unparseable
// URL with query parameters errors before any call.
func TestHTTPClientQueryError(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{Method: "GET", URL: "://bad", Query: map[string]string{"a": "1"}}); err == nil {
		t.Fatal("Do with an unparseable URL + query: err = nil, want error")
	}
}

// TestHTTPClientModelContentType covers the branch where a model header sets
// Content-Type on a body request, so the default is not applied.
func TestHTTPClientModelContentType(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewHTTPClient()
	if _, err := c.Do(context.Background(), Request{
		Method: "POST", URL: srv.URL + "/x",
		Headers: map[string]string{"Content-Type": "application/vnd.custom+json"},
		Body:    map[string]any{"a": 1},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCT != "application/vnd.custom+json" {
		t.Errorf("Content-Type = %q, want the model override", gotCT)
	}
}
