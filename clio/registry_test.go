package clio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

// TestVarToAnyJSON checks that a structured variable is re-parsed into a nested
// object/array (with exact numbers) rather than left as a JSON-in-a-string blob,
// and that unparseable stored JSON degrades to nil.
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

// TestRegistryReplace swaps the whole set of registered connectors at once.
func TestRegistryReplace(t *testing.T) {
	reg := NewRegistry()
	reg.Register("stale", &fakeClient{})
	a := &fakeClient{}
	reg.Replace(map[string]Client{"fresh": a})
	if _, ok := reg.Client("stale"); ok {
		t.Error("after Replace, stale connector still resolves")
	}
	if got, ok := reg.Client("fresh"); !ok || got != a {
		t.Errorf("Client(fresh) = %v,%v, want the replacement", got, ok)
	}
	reg.Replace(nil) // a nil map clears the registry
	if _, ok := reg.Client("fresh"); ok {
		t.Error("after Replace(nil), a connector still resolves")
	}
}

// TestHTTPClientGetState checks the provisional get_state wire format: a GET to
// /api/state carrying subject and reduceSpec, decoded into a state object.
func TestHTTPClientGetState(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"total":42}`))
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL, Token: "t"})
	state, err := c.GetState(context.Background(), "orders/42", "orderTotals")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if gotPath != "/api/state" || !strings.Contains(gotQuery, "subject=orders%2F42") || !strings.Contains(gotQuery, "reduceSpec=orderTotals") {
		t.Errorf("path/query = %q?%q", gotPath, gotQuery)
	}
	if gotAuth != "Bearer t" {
		t.Errorf("Authorization = %q, want Bearer t", gotAuth)
	}
	if state["total"] != json.Number("42") {
		t.Errorf("state total = %#v, want json.Number 42", state["total"])
	}
}

// TestHTTPClientQuery checks the provisional run_query wire format.
func TestHTTPClientQuery(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`[{"id":1}]`))
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	out, err := c.Query(context.Background(), "select * from x")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotBody["query"] != "select * from x" {
		t.Errorf("query body = %#v", gotBody["query"])
	}
	rows, ok := out.([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("query result = %#v, want a 1-element array", out)
	}
}

// TestHTTPClientReadEvents checks the NDJSON parse and the exclusive-cursor
// translation: the boundary event equal to AfterID is dropped.
func TestHTTPClientReadEvents(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"id":"e1","seq":1,"subject":"orders/new","type":"T","data":{"a":1}}
{"id":"e2","seq":2,"subject":"orders/new","type":"T","data":{"a":2}}
`))
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	events, err := c.ReadEvents(context.Background(), ReadEventsRequest{Subject: "orders/new", AfterID: "e1", Recursive: true, Limit: 10})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if !strings.Contains(gotQuery, "lowerBound=e1") || !strings.Contains(gotQuery, "recursive=true") || !strings.Contains(gotQuery, "limit=10") {
		t.Errorf("query = %q", gotQuery)
	}
	if len(events) != 1 || events[0].ID != "e2" {
		t.Fatalf("events = %+v, want only e2 (e1 is the excluded cursor boundary)", events)
	}
}

// TestHTTPClientGetStateError surfaces a non-2xx get_state as an error.
func TestHTTPClientGetStateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	if _, err := c.GetState(context.Background(), "s", ""); err == nil {
		t.Fatal("GetState on HTTP 500: err = nil, want error")
	}
}

// TestHTTPClientQueryError surfaces a non-2xx run_query as an error.
func TestHTTPClientQueryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	if _, err := c.Query(context.Background(), "q"); err == nil {
		t.Fatal("Query on HTTP 500: err = nil, want error")
	}
}

// TestHTTPClientDecodeErrors surfaces malformed clio response bodies as errors on
// every reading operation (the JSON/NDJSON decode-failure branches).
func TestHTTPClientDecodeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	if _, err := c.GetState(context.Background(), "s", ""); err == nil {
		t.Error("GetState on a malformed body: want error")
	}
	if _, err := c.Query(context.Background(), "q"); err == nil {
		t.Error("Query on a malformed body: want error")
	}
	if _, err := c.ReadEvents(context.Background(), ReadEventsRequest{Subject: "s"}); err == nil {
		t.Error("ReadEvents on a malformed NDJSON line: want error")
	}
}

// TestHTTPClientReadEventsError surfaces a non-2xx read as an error.
func TestHTTPClientReadEventsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	if _, err := c.ReadEvents(context.Background(), ReadEventsRequest{Subject: "s"}); err == nil {
		t.Fatal("ReadEvents on HTTP 502: err = nil, want error")
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
// error on every operation, so the job stays pending and retries.
func TestHTTPClientUnreachable(t *testing.T) {
	c := NewHTTPClient(Connector{Endpoint: "http://127.0.0.1:1"}) // nothing listens on port 1
	if err := c.WriteEvent(context.Background(), Event{Subject: "s"}); err == nil {
		t.Fatal("WriteEvent to an unreachable endpoint: err = nil, want error")
	}
	if _, err := c.GetState(context.Background(), "s", ""); err == nil {
		t.Fatal("GetState to an unreachable endpoint: err = nil, want error")
	}
	if _, err := c.Query(context.Background(), "q"); err == nil {
		t.Fatal("Query to an unreachable endpoint: err = nil, want error")
	}
	if _, err := c.ReadEvents(context.Background(), ReadEventsRequest{Subject: "s"}); err == nil {
		t.Fatal("ReadEvents to an unreachable endpoint: err = nil, want error")
	}
}

// TestResultVariableKinds checks the result canonicalization maps each JSON value
// to the matching stored variable kind (covers toVarKind's arms).
func TestResultVariableKinds(t *testing.T) {
	cases := []struct {
		value any
		want  model.VarKind
	}{
		{"hi", model.VarString},
		{true, model.VarBool},
		{json.Number("42"), model.VarNumber},
		{map[string]any{"a": 1}, model.VarJSON},
		{nil, model.VarNull},
	}
	for _, c := range cases {
		if got := resultVariable("r", c.value).Kind; got != c.want {
			t.Errorf("resultVariable(%#v).Kind = %v, want %v", c.value, got, c.want)
		}
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

func (f *fakeClient) GetState(context.Context, string, string) (map[string]any, error) {
	return nil, f.err
}

func (f *fakeClient) Query(context.Context, string) (any, error) { return nil, f.err }

func (f *fakeClient) ReadEvents(context.Context, ReadEventsRequest) ([]InboundEvent, error) {
	return nil, f.err
}
