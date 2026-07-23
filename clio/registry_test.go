package clio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/model"
)

func TestRegistryRegisterAndResolve(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Client("missing"); ok {
		t.Fatal("Client on empty registry: ok = true, want false")
	}
	a, b := &fakeClient{}, &fakeClient{}
	reg.Register("clio", a)
	if got, ok := reg.Client("clio"); !ok || got != a {
		t.Fatalf("Client(clio) = %v,%v, want the registered client", got, ok)
	}
	// Re-registering the same name replaces the binding (last write wins).
	reg.Register("clio", b)
	if got, _ := reg.Client("clio"); got != b {
		t.Errorf("after re-register, Client(clio) = %v, want the newer client", got)
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

// TestHTTPClientWriteEvent checks the provisional wire format: a JSON POST to
// /api/events carrying subject/type/data, with the idempotency and auth headers.
func TestHTTPClientWriteEvent(t *testing.T) {
	var gotPath, gotIdem, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotIdem = r.Header.Get("Idempotency-Key")
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewHTTPClient(Connector{Endpoint: srv.URL, Token: "s3cr3t"})
	err := c.WriteEvent(context.Background(), Event{
		Subject:        "orders/new",
		Type:           "OrderPlaced",
		Data:           map[string]any{"orderId": "c-1"},
		IdempotencyKey: "99",
	})
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if gotPath != "/api/events" {
		t.Errorf("path = %q, want /api/events", gotPath)
	}
	if gotIdem != "99" {
		t.Errorf("Idempotency-Key = %q, want 99", gotIdem)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want Bearer s3cr3t", gotAuth)
	}
	if gotBody["subject"] != "orders/new" || gotBody["type"] != "OrderPlaced" {
		t.Errorf("body subject/type = %v/%v, want orders/new/OrderPlaced", gotBody["subject"], gotBody["type"])
	}
}

// TestHTTPClientNon2xx surfaces a non-2xx clio response as an error, so the job
// stays pending and is retried (at-least-once).
func TestHTTPClientNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	if err := c.WriteEvent(context.Background(), Event{Subject: "s"}); err == nil {
		t.Fatal("WriteEvent on HTTP 500: err = nil, want error")
	}
}

// TestHTTPClientUnreachable surfaces a transport error (clio unreachable) as an
// error, so the job stays pending and retries.
func TestHTTPClientUnreachable(t *testing.T) {
	c := NewHTTPClient(Connector{Endpoint: "http://127.0.0.1:1"}) // nothing listens on port 1
	if err := c.WriteEvent(context.Background(), Event{Subject: "s"}); err == nil {
		t.Fatal("WriteEvent to an unreachable endpoint: err = nil, want error")
	}
}

// fakeClient records the events written through it, for the worker tests.
type fakeClient struct {
	events []Event
	err    error
}

func (f *fakeClient) WriteEvent(_ context.Context, e Event) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, e)
	return nil
}
