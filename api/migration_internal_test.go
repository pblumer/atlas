package api

import (
	"net/http"
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
	// A signed-in non-admin is refused at the boundary, which is where the role is
	// decided (routeroles.go). Migrating a population of live instances onto another
	// definition is the heaviest runtime act there is, and it stays an
	// administrator's even though an operator may cancel and repair.
	for _, path := range paths {
		if got := boundaryStatus(t, []string{RoleModeler, RoleOperator, RoleUser}, http.MethodPost, path); got != http.StatusForbidden {
			t.Errorf("%s as a non-admin = %d, want 403", path, got)
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
