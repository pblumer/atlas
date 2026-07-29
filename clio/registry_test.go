package clio

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

// TestHTTPClientWriteEvent checks the clio v1 wire format: a JSON POST to
// /api/v1/write-events carrying an events array of CloudEvents candidates — each
// with a source, an absolute subject, and a type — plus the idempotency and auth
// headers. A task subject without a leading slash is made absolute.
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
		Source:         "checkout",
		Subject:        "orders/new",
		Type:           "OrderPlaced",
		Data:           map[string]any{"orderId": "c-1"},
		IdempotencyKey: "99",
	})
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if gotPath != "/api/v1/write-events" {
		t.Errorf("path = %q, want /api/v1/write-events", gotPath)
	}
	if gotIdem != "99" {
		t.Errorf("Idempotency-Key = %q, want 99", gotIdem)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want Bearer s3cr3t", gotAuth)
	}
	events, _ := gotBody["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("body events = %#v, want a 1-element array", gotBody["events"])
	}
	ev, _ := events[0].(map[string]any)
	if ev["source"] != "checkout" || ev["subject"] != "/orders/new" || ev["type"] != "OrderPlaced" {
		t.Errorf("event source/subject/type = %v/%v/%v, want checkout//orders/new/OrderPlaced", ev["source"], ev["subject"], ev["type"])
	}
}

// TestHTTPClientWriteEventDefaultSource checks that a write with no source falls
// back to DefaultEventSource, since clio rejects an empty source.
func TestHTTPClientWriteEventDefaultSource(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	if err := c.WriteEvent(context.Background(), Event{Subject: "/s", Type: "T"}); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	events, _ := gotBody["events"].([]any)
	ev, _ := events[0].(map[string]any)
	if ev["source"] != DefaultEventSource {
		t.Errorf("default source = %v, want %q", ev["source"], DefaultEventSource)
	}
}

// TestMintKey checks the clio admin key-mint wire format: a POST to /api/v1/keys
// with the admin bearer and {name, scopes}, returning the once-shown secret.
func TestMintKey(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"kid":"kid_x","secret":"kid_x.s3cr3t"}`))
	}))
	defer srv.Close()

	got, err := MintKey(context.Background(), srv.URL, "admintok", KeyRequest{Name: "atlas-events", Scopes: []string{"read:/employees/*"}, ExpiresAt: "2027-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if got != "kid_x.s3cr3t" {
		t.Errorf("MintKey secret = %q, want kid_x.s3cr3t", got)
	}
	if gotPath != "/api/v1/keys" || gotAuth != "Bearer admintok" {
		t.Errorf("path/auth = %q / %q", gotPath, gotAuth)
	}
	if gotBody["name"] != "atlas-events" || gotBody["expiresAt"] != "2027-01-01T00:00:00Z" {
		t.Errorf("body name/expiresAt = %#v / %#v", gotBody["name"], gotBody["expiresAt"])
	}
	scopes, _ := gotBody["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "read:/employees/*" {
		t.Errorf("body scopes = %#v, want [read:/employees/*]", gotBody["scopes"])
	}
}

// TestMintKeyErrors covers MintKey's failure branches: a missing admin token, a
// non-2xx response, and a response with no secret.
func TestMintKeyErrors(t *testing.T) {
	if _, err := MintKey(context.Background(), "http://x", "", KeyRequest{Name: "n", Scopes: []string{"read:/a"}}); err == nil {
		t.Error("MintKey with no admin token: want error")
	}
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbidden.Close()
	if _, err := MintKey(context.Background(), forbidden.URL, "t", KeyRequest{Name: "n", Scopes: []string{"read:/a"}}); err == nil {
		t.Error("MintKey on HTTP 403: want error")
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"kid":"k"}`))
	}))
	defer empty.Close()
	if _, err := MintKey(context.Background(), empty.URL, "t", KeyRequest{Name: "n", Scopes: []string{"read:/a"}}); err == nil {
		t.Error("MintKey with no secret in response: want error")
	}
	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer badJSON.Close()
	if _, err := MintKey(context.Background(), badJSON.URL, "t", KeyRequest{Name: "n", Scopes: []string{"read:/a"}}); err == nil {
		t.Error("MintKey on a malformed response: want a decode error")
	}
	if _, err := MintKey(context.Background(), "http://127.0.0.1:1", "t", KeyRequest{Name: "n", Scopes: []string{"read:/a"}}); err == nil {
		t.Error("MintKey to an unreachable endpoint: want a transport error")
	}
	if _, err := MintKey(context.Background(), "http://\x7f-bad", "t", KeyRequest{Name: "n", Scopes: []string{"read:/a"}}); err == nil {
		t.Error("MintKey with a bad endpoint: want a build error")
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

// TestHTTPClientGetState checks the clio v1 get_state wire format: a GET to
// /api/v1/state/<subject> with the subject in the path, decoded from the state
// envelope's `state` object. The reduce spec is server-side, so it is not sent.
func TestHTTPClientGetState(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"subject":"/orders/42","state":{"total":42}}`))
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL, Token: "t"})
	state, err := c.GetState(context.Background(), "orders/42", "orderTotals")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if gotPath != "/api/v1/state/orders/42" {
		t.Errorf("path = %q, want /api/v1/state/orders/42", gotPath)
	}
	if gotAuth != "Bearer t" {
		t.Errorf("Authorization = %q, want Bearer t", gotAuth)
	}
	if state["total"] != json.Number("42") {
		t.Errorf("state total = %#v, want json.Number 42", state["total"])
	}
}

// TestHTTPClientQuery checks the clio v1 run_query wire format: a POST to
// /api/v1/run-query with {subject, where}, collecting the NDJSON events.
func TestHTTPClientQuery(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte("{\"id\":\"1\",\"type\":\"T\"}\n{\"id\":\"2\",\"type\":\"T\"}\n"))
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	out, err := c.Query(context.Background(), "orders", `event.type == "T"`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotPath != "/api/v1/run-query" {
		t.Errorf("path = %q, want /api/v1/run-query", gotPath)
	}
	if gotBody["subject"] != "/orders" || gotBody["where"] != `event.type == "T"` {
		t.Errorf("query body subject/where = %#v/%#v", gotBody["subject"], gotBody["where"])
	}
	rows, ok := out.([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("query result = %#v, want a 2-element array", out)
	}
}

// TestHTTPClientReadEvents checks the clio v1 read-events wire format: a POST to
// /api/v1/read-events with a JSON body, the NDJSON parse, and the exclusive-cursor
// translation — the boundary event equal to AfterID is dropped.
func TestHTTPClientReadEvents(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"id":"1","subject":"/orders/new","type":"T","data":{"a":1}}
{"id":"2","subject":"/orders/new","type":"T","data":{"a":2}}
`))
	}))
	defer srv.Close()
	c := NewHTTPClient(Connector{Endpoint: srv.URL})
	events, err := c.ReadEvents(context.Background(), ReadEventsRequest{Subject: "orders/new", AfterID: "1", Recursive: true, Limit: 10})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if gotPath != "/api/v1/read-events" {
		t.Errorf("path = %q, want /api/v1/read-events", gotPath)
	}
	if gotBody["subject"] != "/orders/new" || gotBody["recursive"] != true || gotBody["lowerBound"] != "1" {
		t.Errorf("read body = %#v", gotBody)
	}
	if len(events) != 1 || events[0].ID != "2" {
		t.Fatalf("events = %+v, want only id 2 (id 1 is the excluded cursor boundary)", events)
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
	if _, err := c.Query(context.Background(), "s", "q"); err == nil {
		t.Fatal("Query on HTTP 500: err = nil, want error")
	}
}

// TestHTTPClientBuildRequestErrors covers the request-construction error branch of
// every operation: an endpoint containing a control character makes
// http.NewRequestWithContext fail before any network call.
func TestHTTPClientBuildRequestErrors(t *testing.T) {
	c := NewHTTPClient(Connector{Endpoint: "http://\x7f-bad"})
	if err := c.WriteEvent(context.Background(), Event{Subject: "s"}); err == nil {
		t.Error("WriteEvent with a bad endpoint: want a build error")
	}
	if _, err := c.GetState(context.Background(), "s", ""); err == nil {
		t.Error("GetState with a bad endpoint: want a build error")
	}
	if _, err := c.Query(context.Background(), "s", "q"); err == nil {
		t.Error("Query with a bad endpoint: want a build error")
	}
	if _, err := c.ReadEvents(context.Background(), ReadEventsRequest{Subject: "s"}); err == nil {
		t.Error("ReadEvents with a bad endpoint: want a build error")
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
	if _, err := c.Query(context.Background(), "s", "q"); err == nil {
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
	if _, err := c.Query(context.Background(), "s", "q"); err == nil {
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

func (f *fakeClient) Query(context.Context, string, string) (any, error) { return nil, f.err }

func (f *fakeClient) ReadEvents(context.Context, ReadEventsRequest) ([]InboundEvent, error) {
	return nil, f.err
}
