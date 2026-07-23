package dmn_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestServiceResolverFetches proves the happy path and that the bearer token and
// handle→URL mapping are correct: the handle "dish" is fetched as "/dish.dmn" with
// the Authorization header set.
func TestServiceResolverFetches(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("<definitions/>"))
	}))
	defer srv.Close()

	r := dmn.ServiceResolver{BaseURL: srv.URL + "/models", Token: "s3cret"}
	body, err := r.Resolve(context.Background(), "dish")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(body) != "<definitions/>" {
		t.Errorf("body = %q, want the model xml", body)
	}
	if gotPath != "/models/dish.dmn" {
		t.Errorf("requested path = %q, want /models/dish.dmn", gotPath)
	}
	if gotAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want the bearer token", gotAuth)
	}
}

// TestServiceResolverNotFound maps a 404 to ErrNotFound so a caller can tell an
// unresolved reference from an infrastructure failure.
func TestServiceResolverNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := dmn.ServiceResolver{BaseURL: srv.URL}
	if _, err := r.Resolve(context.Background(), "missing"); !errors.Is(err, dmn.ErrNotFound) {
		t.Fatalf("Resolve of missing model = %v, want ErrNotFound", err)
	}
}

// TestServiceResolverServerError treats any other non-2xx as an infrastructure
// error, distinct from ErrNotFound.
func TestServiceResolverServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := dmn.ServiceResolver{BaseURL: srv.URL}
	_, err := r.Resolve(context.Background(), "dish")
	if err == nil || errors.Is(err, dmn.ErrNotFound) {
		t.Fatalf("Resolve on 500 = %v, want a non-ErrNotFound error", err)
	}
}

// TestServiceResolverUnsafeRef rejects a traversal handle before any request, the
// same contract DirResolver enforces.
func TestServiceResolverUnsafeRef(t *testing.T) {
	r := dmn.ServiceResolver{BaseURL: "http://example.invalid"}
	if _, err := r.Resolve(context.Background(), "../etc/passwd"); !errors.Is(err, dmn.ErrNotFound) {
		t.Fatalf("Resolve of unsafe ref = %v, want ErrNotFound", err)
	}
}

// TestServiceResolverBadURL surfaces an unparseable base URL as a build error, not
// ErrNotFound.
func TestServiceResolverBadURL(t *testing.T) {
	r := dmn.ServiceResolver{BaseURL: "http://["}
	_, err := r.Resolve(context.Background(), "dish")
	if err == nil || errors.Is(err, dmn.ErrNotFound) {
		t.Fatalf("Resolve with a bad base url = %v, want a non-ErrNotFound error", err)
	}
}

// TestServiceResolverTransportError surfaces a connection failure (server closed)
// as an infrastructure error.
func TestServiceResolverTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	r := dmn.ServiceResolver{BaseURL: url}
	_, err := r.Resolve(context.Background(), "dish")
	if err == nil || errors.Is(err, dmn.ErrNotFound) {
		t.Fatalf("Resolve against a closed server = %v, want a transport error", err)
	}
}
