package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestGlobalAuditErrors drives the 500 branches of the global admin audit handler
// (ADR-0184): a broken grant-audit store fails the event load, and a broken projects
// store fails the name-resolution load. Auth is off, so requireAdmin passes and the
// store error is what surfaces.
func TestGlobalAuditErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	get := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil))
		return rec.Code
	}

	realAudit := srv.grantAudit
	srv.grantAudit = brokenStore(newGrantAuditStore(filepath.Join(t.TempDir(), "gone-audit")))
	if got := get(); got != http.StatusInternalServerError {
		t.Fatalf("broken audit store = %d, want 500", got)
	}
	srv.grantAudit = realAudit

	srv.projects = brokenStore(newProjectStore(filepath.Join(t.TempDir(), "gone-projects")))
	if got := get(); got != http.StatusInternalServerError {
		t.Fatalf("broken projects store = %d, want 500", got)
	}
}
