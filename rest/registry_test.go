package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
)

func TestRegistryRegisterAndResolve(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Client("missing"); ok {
		t.Fatal("Client on empty registry: ok = true, want false")
	}
	a, b := &fakeClient{}, &fakeClient{}
	reg.Register("crm", a)
	if got, ok := reg.Client("crm"); !ok || got != a {
		t.Fatalf("Client(crm) = %v,%v, want the registered client", got, ok)
	}
	// Re-registering the same name replaces the binding (last write wins).
	reg.Register("crm", b)
	if got, _ := reg.Client("crm"); got != b {
		t.Errorf("after re-register, Client(crm) = %v, want the newer client", got)
	}
}

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

// TestHTTPClientDoPost checks that a POST carries the JSON body and the
// idempotency and auth headers, and that a JSON response is decoded.
func TestHTTPClientDoPost(t *testing.T) {
	var gotMethod, gotPath, gotIdem, gotAuth, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotIdem = r.Header.Get("Idempotency-Key")
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(Connector{Endpoint: srv.URL, Token: "s3cr3t"})
	resp, err := c.Do(context.Background(), Request{
		Method:         "POST",
		Path:           "/customers",
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
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want Bearer s3cr3t", gotAuth)
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

	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	resp, err := c.Do(context.Background(), Request{Method: "GET", Path: "/ping"})
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
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	resp, err := c.Do(context.Background(), Request{Method: "DELETE", Path: "/x"})
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
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x"}); err == nil {
		t.Fatal("Do on HTTP 500: err = nil, want error")
	}
}

// TestHTTPClientUnreachable surfaces a transport error as an error, so the job
// stays pending and retries.
func TestHTTPClientUnreachable(t *testing.T) {
	c := NewHTTPClient(Connector{Endpoint: "http://127.0.0.1:1"}) // nothing listens on port 1
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/x"}); err == nil {
		t.Fatal("Do to an unreachable endpoint: err = nil, want error")
	}
}

// TestHTTPClientEncodeError covers the body-encode failure branch: a value JSON
// cannot marshal leaves the request unsent.
func TestHTTPClientEncodeError(t *testing.T) {
	c := NewHTTPClient(Connector{Endpoint: "http://example.invalid"})
	_, err := c.Do(context.Background(), Request{Method: "POST", Path: "/x", Body: map[string]any{"bad": make(chan int)}})
	if err == nil {
		t.Fatal("Do with an unmarshalable body: err = nil, want error")
	}
}

// TestHTTPClientBuildError covers the request-build failure branch: an invalid
// method token cannot form a request.
func TestHTTPClientBuildError(t *testing.T) {
	c := NewHTTPClient(Connector{Endpoint: "http://example.invalid"})
	if _, err := c.Do(context.Background(), Request{Method: "BAD METHOD", Path: "/x"}); err == nil {
		t.Fatal("Do with an invalid method: err = nil, want error")
	}
}

// fakeClient records the requests made through it, for the worker tests.
type fakeClient struct {
	requests []Request
	resp     Response
	err      error
}

func (f *fakeClient) Do(_ context.Context, r Request) (Response, error) {
	if f.err != nil {
		return Response{}, f.err
	}
	f.requests = append(f.requests, r)
	return f.resp, nil
}
