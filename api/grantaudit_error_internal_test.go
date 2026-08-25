package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrantAuditStoreErrors drives the 500 branches that a normal flow can't reach:
// a broken grant-audit store makes every handler that records or reads a grant
// surface a storage failure. Auth is off, so the owner check passes and the audit
// store error is what surfaces.
func TestGrantAuditStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// A project to act on, and one with a member so the unshare path records.
	if err := srv.projects.Save(project{ID: "p1", Name: "P", Visibility: VisibilityShared,
		Members: []projectMember{{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_a"}, Role: ScopeRoleEditor}}}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// Break the audit store: its directory is gone, so every read/write on it fails.
	srv.grantAudit = brokenStore(newGrantAuditStore(filepath.Join(t.TempDir(), "gone")))

	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"list", "GET", "/api/v1/applications/p1/audit", ""},
		{"share", "PUT", "/api/v1/projects/p1/members/usr_b", `{"role":"editor"}`},
		{"unshare", "DELETE", "/api/v1/projects/p1/members/usr_a", ""},
		{"visibility", "PATCH", "/api/v1/projects/p1", `{"visibility":"private"}`},
		{"transfer", "PATCH", "/api/v1/projects/p1", `{"ownerId":"usr_new"}`},
		{"delete", "DELETE", "/api/v1/projects/p1", ""},
	} {
		if got := do(tc.method, tc.path, tc.body); got != http.StatusInternalServerError {
			t.Fatalf("%s (%s %s) = %d, want 500", tc.name, tc.method, tc.path, got)
		}
	}
}

// TestGrantAuditStoreHelpers covers the store helpers and the record helper directly,
// including their error returns on a broken store.
func TestGrantAuditStoreHelpers(t *testing.T) {
	srv := newServerForErrors(t)

	// On a healthy store: record two events, list them back for the app, and the
	// unrelated app sees none.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, a := range []string{GrantActionShare, GrantActionTransfer} {
		if err := srv.recordGrantAudit(req, grantAudit{ApplicationID: "app1", Action: a}); err != nil {
			t.Fatalf("record %s: %v", a, err)
		}
	}
	if got, err := srv.grantAudit.forApplication("app1"); err != nil || len(got) != 2 {
		t.Fatalf("forApplication(app1) = %d,%v, want 2,nil", len(got), err)
	}
	if got, err := srv.grantAudit.forApplication("other"); err != nil || len(got) != 0 {
		t.Fatalf("forApplication(other) = %d,%v, want 0,nil", len(got), err)
	}
	if err := srv.grantAudit.deleteForApplication("app1"); err != nil {
		t.Fatalf("deleteForApplication: %v", err)
	}
	if got, _ := srv.grantAudit.forApplication("app1"); len(got) != 0 {
		t.Fatalf("after delete = %d, want 0", len(got))
	}

	// On a broken store: record, list, and delete all surface the error.
	srv.grantAudit = brokenStore(newGrantAuditStore(filepath.Join(t.TempDir(), "gone")))
	if err := srv.recordGrantAudit(req, grantAudit{ApplicationID: "x", Action: GrantActionShare}); err == nil {
		t.Fatal("record on broken store: want error")
	}
	if _, err := srv.grantAudit.forApplication("x"); err == nil {
		t.Fatal("forApplication on broken store: want error")
	}
	if err := srv.grantAudit.deleteForApplication("x"); err == nil {
		t.Fatal("deleteForApplication on broken store: want error")
	}
}
