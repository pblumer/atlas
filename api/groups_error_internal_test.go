package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGroupStoreErrors drives the 500 branches of the group handlers and the
// login group-snapshot that a normal flow can't reach: a broken group store, a
// group/user record that errors on read, and a broken store during login. Auth is
// off, so requireAdmin passes and the store error is what surfaces.
func TestGroupStoreErrors(t *testing.T) {
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

	realGroups := srv.groups
	if err := srv.groups.Save(group{ID: "g1", Name: "G", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	// A) A broken store (its directory removed) fails list, create, and delete.
	srv.groups = brokenStore(newGroupStore(filepath.Join(t.TempDir(), "gone")))
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/groups", ""},
		{"POST", "/api/v1/groups", `{"name":"x"}`},
		{"DELETE", "/api/v1/groups/g1", ""},
	} {
		if got := do(tc.method, tc.path, tc.body); got != http.StatusInternalServerError {
			t.Fatalf("%s %s broken store = %d, want 500", tc.method, tc.path, got)
		}
	}
	srv.groups = realGroups

	// B) A group record whose path is a directory errors on read (not a clean
	// miss), so the by-id handlers surface a 500.
	if err := os.MkdirAll(srv.groups.FileFor("gdir"), 0o755); err != nil {
		t.Fatalf("mk gdir: %v", err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "/api/v1/groups/gdir", `{"name":"z"}`},
		{"PUT", "/api/v1/groups/gdir/members/u1", ""},
		{"DELETE", "/api/v1/groups/gdir/members/u1", ""},
	} {
		if got := do(tc.method, tc.path, tc.body); got != http.StatusInternalServerError {
			t.Fatalf("%s %s dir record = %d, want 500", tc.method, tc.path, got)
		}
	}

	// C) Adding a member fails when the user lookup errors (user record is a dir).
	if err := os.MkdirAll(srv.users.FileFor("udir"), 0o755); err != nil {
		t.Fatalf("mk udir: %v", err)
	}
	if got := do("PUT", "/api/v1/groups/g1/members/udir", ""); got != http.StatusInternalServerError {
		t.Fatalf("add member with broken user lookup = %d, want 500", got)
	}

	// D) Login fails when the group snapshot can't be read.
	hash, err := hashPassword("password1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := srv.users.Save(User{ID: "u1", Username: "bob", Source: SourceLocal, PasswordHash: hash, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	srv.groups = brokenStore(newGroupStore(filepath.Join(t.TempDir(), "gone2")))
	if got := do("POST", "/api/v1/auth/login", `{"username":"bob","password":"password1"}`); got != http.StatusInternalServerError {
		t.Fatalf("login with broken group store = %d, want 500", got)
	}
	srv.groups = realGroups
}

// TestGroupStoreHelperEdges covers the empty-needle short-circuits of the store
// helpers.
func TestGroupStoreHelperEdges(t *testing.T) {
	srv := newServerForErrors(t)
	if _, ok, err := srv.groups.byName("  ", ""); ok || err != nil {
		t.Fatalf("byName(blank) = %v,%v, want false,nil", ok, err)
	}
	if ids, err := srv.groups.idsForUser(""); ids != nil || err != nil {
		t.Fatalf("idsForUser(empty) = %v,%v, want nil,nil", ids, err)
	}
}
