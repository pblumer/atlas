package temis_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/connector/temis"
)

// TestRegistry covers name→client binding and the unregistered-name miss.
func TestRegistry(t *testing.T) {
	reg := temis.NewRegistry()
	c := temis.NewHTTPClient(temis.Connector{Endpoint: "http://x"})
	reg.Register("risk", c)
	if got, ok := reg.Client("risk"); !ok || got != c {
		t.Fatalf("Client(risk) = %v, %v, want the registered client", got, ok)
	}
	if _, ok := reg.Client("nope"); ok {
		t.Fatal("Client(nope) = ok, want a miss")
	}
}

// TestRegistryReplace covers swapping the whole connector set at once (ADR-0041):
// a fresh map replaces the previous registrations, and a nil map clears them.
func TestRegistryReplace(t *testing.T) {
	reg := temis.NewRegistry()
	reg.Register("old", temis.NewHTTPClient(temis.Connector{Endpoint: "http://old"}))

	next := temis.NewHTTPClient(temis.Connector{Endpoint: "http://new"})
	reg.Replace(map[string]temis.Client{"new": next})
	if _, ok := reg.Client("old"); ok {
		t.Error("after Replace: old connector still registered, want it swapped out")
	}
	if got, ok := reg.Client("new"); !ok || got != next {
		t.Error("after Replace: new connector not registered")
	}

	reg.Replace(nil) // nil clears
	if _, ok := reg.Client("new"); ok {
		t.Error("after Replace(nil): registry not cleared")
	}
}

// TestHTTPClientEvaluates proves the request shape (decision id + inputs + bearer
// token) and that the {"outputs":…} envelope is decoded back.
func TestHTTPClientEvaluates(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"outputs":{"Risk":"high"}}`))
	}))
	defer srv.Close()

	c := temis.NewHTTPClient(temis.Connector{Endpoint: srv.URL, Token: "s3cret"})
	out, err := c.Evaluate(context.Background(), "Risk", map[string]any{"Amount": 150})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if out["Risk"] != "high" {
		t.Errorf("outputs = %#v, want Risk=high", out)
	}
	if gotPath != "/evaluate" {
		t.Errorf("path = %q, want /evaluate", gotPath)
	}
	if gotAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want the bearer token", gotAuth)
	}
	if gotBody["decisionId"] != "Risk" {
		t.Errorf("request decisionId = %v, want Risk", gotBody["decisionId"])
	}
	if in, _ := gotBody["inputs"].(map[string]any); in["Amount"] == nil {
		t.Errorf("request inputs = %v, want an Amount", gotBody["inputs"])
	}
}

// TestHTTPClientServerError turns a non-2xx into an error, leaving the job pending.
func TestHTTPClientServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := temis.NewHTTPClient(temis.Connector{Endpoint: srv.URL})
	if _, err := c.Evaluate(context.Background(), "Risk", nil); err == nil {
		t.Fatal("Evaluate on 502: got nil error, want an error")
	}
}

// TestHTTPClientBadResponse surfaces an undecodable body as an error.
func TestHTTPClientBadResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := temis.NewHTTPClient(temis.Connector{Endpoint: srv.URL})
	if _, err := c.Evaluate(context.Background(), "Risk", nil); err == nil {
		t.Fatal("Evaluate on a bad body: got nil error, want an error")
	}
}

// TestHTTPClientEncodeError surfaces an input value with no JSON image (a channel)
// as an encode error rather than a panic.
func TestHTTPClientEncodeError(t *testing.T) {
	c := temis.NewHTTPClient(temis.Connector{Endpoint: "http://x"})
	if _, err := c.Evaluate(context.Background(), "Risk", map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("Evaluate with an unencodable input: got nil error, want an error")
	}
}

// TestHTTPClientBadEndpoint surfaces a malformed endpoint as a request-build error.
func TestHTTPClientBadEndpoint(t *testing.T) {
	c := temis.NewHTTPClient(temis.Connector{Endpoint: "http://\x7f"})
	if _, err := c.Evaluate(context.Background(), "Risk", nil); err == nil {
		t.Fatal("Evaluate with a malformed endpoint: got nil error, want an error")
	}
}

// TestHTTPClientTransportError surfaces a connection failure (server closed).
func TestHTTPClientTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := temis.NewHTTPClient(temis.Connector{Endpoint: url})
	if _, err := c.Evaluate(context.Background(), "Risk", nil); err == nil {
		t.Fatal("Evaluate against a closed server: got nil error, want a transport error")
	}
}
