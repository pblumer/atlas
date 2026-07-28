package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestListPrincipalsStoreError covers the 500 branch: when the user store cannot
// be read, the directory surfaces the error rather than a partial list.
func TestListPrincipalsStoreError(t *testing.T) {
	srv := newServerForErrors(t)
	srv.users = &userStore{dir: filepath.Join(t.TempDir(), "gone")} // loadAll fails: no dir

	req := httptest.NewRequest(http.MethodGet, "/api/v1/principals", nil)
	rec := httptest.NewRecorder()
	srv.handleListPrincipals(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("principals over broken store = %d, want 500", rec.Code)
	}
}
