package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adminReq builds a request carrying an admin Principal and the given path
// values, so a scope handler can be driven directly (bypassing the auth
// middleware) to reach branches a normal HTTP flow can't. An admin is
// owner-equivalent on every project, so authorization always passes and the
// store-error branches below are what get exercised.
func adminReq(method string, body io.Reader, id, userID string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, "/api/v1/projects/"+id+"/members/"+userID, body)
	req.SetPathValue("id", id)
	req.SetPathValue("userId", userID)
	req = req.WithContext(withPrincipal(req.Context(), &Principal{UserID: "usr_admin", Roles: []string{RoleAdmin}}))
	return req, httptest.NewRecorder()
}

// TestScopeMemberHandlerErrors covers the failure branches of the sharing-scope
// member handlers (ADR-0071): body-read and JSON errors, an unknown or broken
// project, a broken user store, and a save/count failure after authorization.
func TestScopeMemberHandlerErrors(t *testing.T) {
	srv := newServerForErrors(t)
	srv.authEnabled = true // sharing is only meaningful under auth

	realDrafts := srv.drafts
	brokenDrafts := &draftStore{dir: filepath.Join(t.TempDir(), "gone")}

	// Seed a real project and a real user so the later branches can load both.
	now := int64(1)
	proj := project{ID: "p1", Name: "P", OwnerID: "usr_owner", Visibility: VisibilityShared, CreatedAt: now, UpdatedAt: now}
	if err := srv.projects.save(proj); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := srv.users.save(User{ID: "usr_target", Username: "t", Source: SourceLocal, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Body read failure → 400.
	req, rec := adminReq(http.MethodPut, errReader{}, "p1", "usr_target")
	srv.handleSetProjectMember(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("read-body error = %d, want 400", rec.Code)
	}

	// Malformed JSON → 400.
	req, rec = adminReq(http.MethodPut, strings.NewReader("{"), "p1", "usr_target")
	srv.handleSetProjectMember(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON = %d, want 400", rec.Code)
	}

	// Unknown project → 404 (authorizeProject get returns ok=false).
	req, rec = adminReq(http.MethodPut, strings.NewReader(`{"role":"viewer"}`), "ghost", "usr_target")
	srv.handleSetProjectMember(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d, want 404", rec.Code)
	}

	// A project record whose path is a directory makes get() error (a real error,
	// not a clean not-found), exercising the 500 branch of authorizeProject for
	// both member handlers.
	if err := os.MkdirAll(srv.projects.fileFor("pdir"), 0o755); err != nil {
		t.Fatalf("make project dir: %v", err)
	}
	req, rec = adminReq(http.MethodPut, strings.NewReader(`{"role":"viewer"}`), "pdir", "usr_target")
	srv.handleSetProjectMember(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken project store (set) = %d, want 500", rec.Code)
	}
	req, rec = adminReq(http.MethodDelete, nil, "pdir", "usr_target")
	srv.handleRemoveProjectMember(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken project store (remove) = %d, want 500", rec.Code)
	}

	// Broken user store → 500 (users.get errors: the record path is a directory).
	if err := os.MkdirAll(srv.users.fileFor("usr_dir"), 0o755); err != nil {
		t.Fatalf("make user dir: %v", err)
	}
	req, rec = adminReq(http.MethodPut, strings.NewReader(`{"role":"editor"}`), "p1", "usr_dir")
	srv.handleSetProjectMember(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken user lookup = %d, want 500", rec.Code)
	}

	// Save/count failure after authorization → 500 (the artifact count reads the
	// broken drafts store, after the project save has already succeeded).
	srv.drafts = brokenDrafts
	req, rec = adminReq(http.MethodPut, strings.NewReader(`{"role":"editor"}`), "p1", "usr_target")
	srv.handleSetProjectMember(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken count (set) = %d, want 500", rec.Code)
	}
	req, rec = adminReq(http.MethodDelete, nil, "p1", "usr_target")
	srv.handleRemoveProjectMember(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken count (remove) = %d, want 500", rec.Code)
	}
	srv.drafts = realDrafts
}

// TestUpdateProjectTransferLookupError covers the transfer branch where the new
// owner's user record cannot be read (ADR-0071): a 500, distinct from the 400 an
// unknown owner returns.
func TestUpdateProjectTransferLookupError(t *testing.T) {
	srv := newServerForErrors(t)
	srv.authEnabled = true

	now := int64(1)
	if err := srv.projects.save(project{ID: "p1", Name: "P", OwnerID: "usr_owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := os.MkdirAll(srv.users.fileFor("usr_dir"), 0o755); err != nil {
		t.Fatalf("make user dir: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/p1", strings.NewReader(`{"ownerId":"usr_dir"}`))
	req.SetPathValue("id", "p1")
	req = req.WithContext(withPrincipal(req.Context(), &Principal{UserID: "usr_admin", Roles: []string{RoleAdmin}}))
	rec := httptest.NewRecorder()
	srv.handleUpdateProject(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("transfer with broken user lookup = %d, want 500", rec.Code)
	}
}

// TestDeleteAbsentProjectIsIdempotent checks that deleting a project that does
// not exist still returns 204 — a caller cannot tell "gone" from "never existed"
// (ADR-0071), preserving the idempotent-delete contract of ADR-0034.
func TestDeleteAbsentProjectIsIdempotent(t *testing.T) {
	srv := newServerForErrors(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/ghost", nil)
	req.SetPathValue("id", "ghost")
	rec := httptest.NewRecorder()
	srv.handleDeleteProject(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete absent = %d, want 204", rec.Code)
	}
}

// TestProjectViewForNormalizes checks the display normalization: a pre-scopes
// project (no visibility, nil members) renders as private with an empty member
// array, and MyRole is filled from the same access rule (ADR-0071).
func TestProjectViewForNormalizes(t *testing.T) {
	srv := &Server{} // auth off → everyone is owner-equivalent
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	v := srv.projectViewFor(req, project{ID: "legacy"}, 0)
	if v.Visibility != VisibilityPrivate {
		t.Fatalf("visibility = %q, want private", v.Visibility)
	}
	if v.Members == nil || len(v.Members) != 0 {
		t.Fatalf("members = %v, want empty non-nil", v.Members)
	}
	if v.MyRole != ScopeRoleOwner {
		t.Fatalf("myRole = %q, want owner (auth off)", v.MyRole)
	}
}

// TestRemoveMemberList covers the slice helper directly: it keeps other members,
// and removing an absent user is a no-op.
func TestRemoveMemberList(t *testing.T) {
	ms := []projectMember{
		{Ref: principalRef{Type: PrincipalTypeUser, ID: "a"}, Role: ScopeRoleViewer},
		{Ref: principalRef{Type: PrincipalTypeUser, ID: "b"}, Role: ScopeRoleEditor},
	}
	got := removeMember(ms, "a")
	if len(got) != 1 || got[0].Ref.ID != "b" {
		t.Fatalf("remove a = %v, want [b]", got)
	}
	if again := removeMember(got, "zzz"); len(again) != 1 || again[0].Ref.ID != "b" {
		t.Fatalf("remove absent = %v, want unchanged [b]", again)
	}
}
