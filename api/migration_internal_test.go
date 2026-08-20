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
	h := srv.Handler()
	for _, path := range []string{
		"/api/v1/instances/1/migrate",
		"/api/v1/instances/1/migrate/plan",
		"/api/v1/processes/1/migrate-instances",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"targetProcessDefKey":2,"reason":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without an admin principal = %d, want it refused", path, rec.Code)
		}
	}
}
