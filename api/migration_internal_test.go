package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMigrationEndpointsAreAdminOnly covers the gate in front of all three: rebinding a
// running instance to another version of its process is a change to live process state
// that the engine would not have made on its own, so it is admin-shaped the way the
// variable override is (ADR-0098/0162). With auth off — the single-binary default —
// every one of them stays open.
func TestMigrationEndpointsAreAdminOnly(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	paths := []string{
		"/api/v1/instances/1/migrate",
		"/api/v1/instances/1/migrate/plan",
		"/api/v1/processes/1/migrate-instances",
	}
	handler := map[string]http.HandlerFunc{
		"/api/v1/instances/1/migrate":           srv.handleMigrateInstance,
		"/api/v1/instances/1/migrate/plan":      srv.handleMigrationPlan,
		"/api/v1/processes/1/migrate-instances": srv.handleMigrateInstancesOfProcess,
	}
	// Called directly with a signed-in *non-admin*, so the handler's own gate is what
	// refuses — not the middleware in front of it, which would refuse an anonymous
	// caller before the handler ever ran.
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"targetProcessDefKey":2,"reason":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{Username: "op", Roles: []string{"user"}}))
		rec := httptest.NewRecorder()
		handler[path](rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s as a non-admin = %d, want 403", path, rec.Code)
		}
	}

	// And end to end, an anonymous caller never reaches them at all.
	h := srv.Handler()
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"targetProcessDefKey":2,"reason":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymously = %d, want it refused", path, rec.Code)
		}
	}
}
