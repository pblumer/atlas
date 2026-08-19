package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureSystemProjectIdempotent asserts the ADR-0122 bootstrap: New() creates
// the protected system project, and re-running ensureSystemProject neither
// duplicates it nor churns its CreatedAt — the "boot twice → one, unchanged"
// property that keeps startup idempotent.
func TestEnsureSystemProjectIdempotent(t *testing.T) {
	srv := newServerForErrors(t)

	got, ok, err := srv.projects.Get(systemProjectID)
	if err != nil {
		t.Fatalf("get system project: %v", err)
	}
	if !ok {
		t.Fatal("New did not create the system project")
	}
	if !got.Protected || got.OwnerID != systemOwnerID {
		t.Fatalf("system project = %+v, want Protected owned by %q", got, systemOwnerID)
	}
	created := got.CreatedAt

	// A second call is a no-op: same record, CreatedAt preserved.
	if err := srv.ensureSystemProject(created + 10_000); err != nil {
		t.Fatalf("second ensureSystemProject: %v", err)
	}
	again, _, err := srv.projects.Get(systemProjectID)
	if err != nil {
		t.Fatalf("re-get: %v", err)
	}
	if again.CreatedAt != created {
		t.Fatalf("CreatedAt churned: %d -> %d", created, again.CreatedAt)
	}
}

// TestEnsureSystemProjectRepairs covers the branch where a record already exists
// under the reserved id but has lost its protection/owner (e.g. hand-edited): the
// bootstrap re-asserts them without recreating the record.
func TestEnsureSystemProjectRepairs(t *testing.T) {
	srv := newServerForErrors(t)

	if err := srv.projects.Save(project{ID: systemProjectID, Name: "tampered", CreatedAt: 5}); err != nil {
		t.Fatalf("seed tampered: %v", err)
	}
	if err := srv.ensureSystemProject(99); err != nil {
		t.Fatalf("repair: %v", err)
	}
	got, _, err := srv.projects.Get(systemProjectID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Protected || got.OwnerID != systemOwnerID {
		t.Fatalf("after repair = %+v, want protected + system owner", got)
	}
	if got.CreatedAt != 5 {
		t.Fatalf("CreatedAt = %d, want 5 preserved", got.CreatedAt)
	}
}

// TestEnsureSystemProjectStoreError covers the error return: a broken store makes
// the initial get fail.
func TestEnsureSystemProjectStoreError(t *testing.T) {
	srv := newServerForErrors(t)
	srv.projects = brokenStore(newProjectStore(filepath.Join(t.TempDir(), "nope", "deeper")))
	// get() of a missing file is not an error, so force a real read error by making
	// the record path a directory.
	if err := srv.ensureSystemProject(1); err == nil {
		// A non-existent parent dir yields ok=false, nil — so a fresh save must
		// succeed here; instead point at an unwritable location to fail the save.
		srv.projects = brokenStore(newProjectStore("/proc/atlas-cannot-write"))
		if err := srv.ensureSystemProject(1); err == nil {
			t.Fatal("ensureSystemProject with unwritable store: want error")
		}
	}
}

// TestProtectedEffectiveRoleVisibleToAll pins the ADR-0122 visibility rule: a
// protected project is readable (viewer) by any authenticated principal and
// owner-equivalent for admins, while ordinary access rules are unchanged.
func TestProtectedEffectiveRoleVisibleToAll(t *testing.T) {
	prot := project{ID: systemProjectID, OwnerID: systemOwnerID, Visibility: VisibilityShared, Protected: true}
	if r := prot.effectiveRole(&Principal{UserID: "u1"}, true); r != ScopeRoleViewer {
		t.Fatalf("authed user on protected = %q, want viewer", r)
	}
	if r := prot.effectiveRole(&Principal{UserID: "a1", Roles: []string{RoleAdmin}}, true); r != ScopeRoleOwner {
		t.Fatalf("admin on protected = %q, want owner", r)
	}
	if r := prot.effectiveRole(nil, true); r != "" {
		t.Fatalf("anonymous on protected = %q, want no access", r)
	}
	// Regression: a normal private project still hides from a stranger.
	priv := project{ID: "p", OwnerID: "owner1", Visibility: VisibilityPrivate}
	if r := priv.effectiveRole(&Principal{UserID: "stranger"}, true); r != "" {
		t.Fatalf("stranger on private = %q, want no access", r)
	}
}

// TestProtectedProjectRefusesMutation is the enforcement heart of ADR-0122: with
// auth off — where every caller is treated as owner (the highest access) — the
// protected system project still refuses every mutating design-time operation,
// while an ordinary project accepts them. Because the highest-access caller is
// refused, no role can bypass the guard.
func TestProtectedProjectRefusesMutation(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	do := func(method, path, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		// Inject an admin principal too, so the case doubles as "not even an admin".
		req = req.WithContext(withPrincipal(req.Context(), &Principal{UserID: "a1", Roles: []string{RoleAdmin}}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Seed an ungrouped draft so the "move a draft into it" case has something to
	// move (draftBody's process id is "p").
	if got := do(http.MethodPost, "/api/v1/drafts", draftBody); got != http.StatusOK {
		t.Fatalf("seed ungrouped draft = %d, want 200", got)
	}

	base := "/api/v1/projects/" + systemProjectID
	cases := []struct {
		name, method, path, body string
	}{
		{"rename", http.MethodPatch, base, `{"name":"x"}`},
		{"delete", http.MethodDelete, base, ""},
		{"add member", http.MethodPut, base + "/members/u1", `{"role":"editor"}`},
		{"remove member", http.MethodDelete, base + "/members/u1", ""},
		{"save draft into it", http.MethodPost, "/api/v1/drafts?projectId=" + systemProjectID, draftBody},
		{"move draft into it", http.MethodPatch, "/api/v1/drafts/p", `{"projectId":"` + systemProjectID + `"}`},
	}
	for _, tc := range cases {
		if got := do(tc.method, tc.path, tc.body); got != http.StatusForbidden {
			t.Fatalf("%s: status=%d, want 403", tc.name, got)
		}
	}

	// The guard is targeted: an ordinary project still accepts a rename.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"Ordinary"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create ordinary: %d", rec.Code)
	}
	var created struct {
		ID        string `json:"id"`
		Protected bool   `json:"protected"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Protected {
		t.Fatal("ordinary project should not be protected")
	}
	if got := do(http.MethodPatch, "/api/v1/projects/"+created.ID, `{"name":"Renamed"}`); got != http.StatusOK {
		t.Fatalf("rename ordinary = %d, want 200", got)
	}
}
