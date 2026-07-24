package api_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestInternalTokenAuthenticatesRequests exercises the real New() path: under
// --auth an internal token is minted, resolves to a non-admin service principal,
// and lets an unauthenticated-by-cookie request through the gated API (ADR-0049).
func TestInternalTokenAuthenticatesRequests(t *testing.T) {
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")
	dir := t.TempDir()
	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir, api.WithAuth())
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = wl.Close()
	})

	token := srv.InternalToken()
	if token == "" {
		t.Fatal("expected a non-empty internal token under --auth")
	}

	do := func(method, path, bearer string) int {
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	// No credential → gated.
	if code := do("GET", "/api/v1/processes", ""); code != http.StatusUnauthorized {
		t.Fatalf("no credential: want 401, got %d", code)
	}
	// The internal token authenticates the gated API.
	if code := do("GET", "/api/v1/processes", token); code != http.StatusOK {
		t.Fatalf("internal token: want 200, got %d", code)
	}
	// A wrong token does not.
	if code := do("GET", "/api/v1/processes", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", code)
	}
	// The service principal is not an admin: user management stays forbidden.
	if code := do("GET", "/api/v1/users", token); code != http.StatusForbidden {
		t.Fatalf("service principal on admin route: want 403, got %d", code)
	}
}
