package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
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
	if r := prot.effectiveRole(&httpapi.Principal{UserID: "u1"}, true); r != ScopeRoleViewer {
		t.Fatalf("authed user on protected = %q, want viewer", r)
	}
	if r := prot.effectiveRole(&httpapi.Principal{UserID: "a1", Roles: []string{RoleAdmin}}, true); r != ScopeRoleOwner {
		t.Fatalf("admin on protected = %q, want owner", r)
	}
	if r := prot.effectiveRole(nil, true); r != "" {
		t.Fatalf("anonymous on protected = %q, want no access", r)
	}
	// Regression: a normal private project still hides from a stranger.
	priv := project{ID: "p", OwnerID: "owner1", Visibility: VisibilityPrivate}
	if r := priv.effectiveRole(&httpapi.Principal{UserID: "stranger"}, true); r != "" {
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
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "a1", Roles: []string{RoleAdmin}}))
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

// TestProtectedArtifactRefusesRename covers the newer way to reach a platform-managed
// artifact: an identity-aware save renames a record by writing it under a new id and
// *deleting* the one it came from (ADR-draft-artifact-id-renames). The write's own
// protected check looks at the id being saved onto, which on a rename is a free one —
// so the record being deleted has to be checked in its own right, or a rename would be
// the way to take a system draft or form out of the project that protects it.
func TestProtectedArtifactRefusesRename(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	// Seed one of each inside the protected system project, past the HTTP guards that
	// would refuse putting them there.
	if err := srv.drafts.Save(draft{ProcessID: "sys-proc", Name: "System", ProjectID: systemProjectID, XML: draftBody}); err != nil {
		t.Fatalf("seed system draft: %v", err)
	}
	if err := srv.forms.Save(form{ID: "sys-form", Name: "System", ProjectID: systemProjectID, Schema: `{"type":"default","components":[]}`}); err != nil {
		t.Fatalf("seed system form: %v", err)
	}

	do := func(method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		// An admin principal, so the case doubles as "not even an admin".
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "a1", Roles: []string{RoleAdmin}}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	renamedDraft := strings.Replace(draftBody, `id="p"`, `id="stolen"`, 1)
	if renamedDraft == draftBody {
		t.Fatal("draftBody no longer carries id=\"p\"; fix this test's rename")
	}
	// Both attempts also try to drop the artifact into Ungrouped on the way out, which
	// is what would slip past a check that only looks at the destination.
	if got := do(http.MethodPost, "/api/v1/drafts?from=sys-proc&projectId=", renamedDraft); got != http.StatusForbidden {
		t.Fatalf("rename system draft = %d, want 403", got)
	}
	if got := do(http.MethodPost, "/api/v1/forms",
		`{"id":"stolen-form","schema":{"type":"default","components":[]},"from":"sys-form","projectId":""}`); got != http.StatusForbidden {
		t.Fatalf("rename system form = %d, want 403", got)
	}

	// Nothing moved: both are still filed under the protected project, and neither new
	// id exists.
	if _, ok, err := srv.drafts.Get("stolen"); err != nil || ok {
		t.Fatalf("renamed draft was written anyway (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := srv.forms.Get("stolen-form"); err != nil || ok {
		t.Fatalf("renamed form was written anyway (ok=%v err=%v)", ok, err)
	}
	if rec, ok, err := srv.drafts.Get("sys-proc"); err != nil || !ok || rec.ProjectID != systemProjectID {
		t.Fatalf("system draft = %+v ok=%v err=%v, want still in the system project", rec, ok, err)
	}
	if rec, ok, err := srv.forms.Get("sys-form"); err != nil || !ok || rec.ProjectID != systemProjectID {
		t.Fatalf("system form = %+v ok=%v err=%v, want still in the system project", rec, ok, err)
	}
}
