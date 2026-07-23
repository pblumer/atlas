package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestProjectReadBodyErrors covers the io.ReadAll failure branch of the project
// and move-draft handlers by supplying a body that errors on read.
func TestProjectReadBodyErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/projects"},
		{http.MethodPatch, "/api/v1/projects/x"},
		{http.MethodPatch, "/api/v1/drafts/x"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, errReader{})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: status=%d, want 400", tc.method, tc.path, rec.Code)
		}
	}
}

// TestProjectHandlerStoreErrors drives the store-error branches of the project
// and project-aware draft handlers (500s), which a normal HTTP flow can't reach,
// by pointing the stores at broken paths.
func TestProjectHandlerStoreErrors(t *testing.T) {
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

	realProjects := srv.projects
	realDrafts := srv.drafts

	// Seed a real project (capture its id) and a real ungrouped draft.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"Real"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed project: status=%d", rec.Code)
	}
	var seeded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &seeded); err != nil {
		t.Fatalf("decode seeded project: %v", err)
	}
	if got := do(http.MethodPost, "/api/v1/drafts", draftBody); got != http.StatusOK {
		t.Fatalf("seed draft: status=%d", got)
	}

	brokenProjects := &projectStore{dir: filepath.Join(t.TempDir(), "gone")}
	brokenDrafts := &draftStore{dir: filepath.Join(t.TempDir(), "gone")}

	// Create/list fail when the projects directory does not exist.
	srv.projects = brokenProjects
	if got := do(http.MethodPost, "/api/v1/projects", `{"name":"X"}`); got != http.StatusInternalServerError {
		t.Fatalf("create with broken store = %d, want 500", got)
	}
	if got := do(http.MethodGet, "/api/v1/projects", ""); got != http.StatusInternalServerError {
		t.Fatalf("list with broken projects = %d, want 500", got)
	}
	srv.projects = realProjects

	// List fails when the drafts directory (used for artifact counts) is broken.
	srv.drafts = brokenDrafts
	if got := do(http.MethodGet, "/api/v1/projects", ""); got != http.StatusInternalServerError {
		t.Fatalf("list with broken drafts = %d, want 500", got)
	}
	// Rename's artifact-count step fails on the same broken drafts store, after
	// the project get + save have succeeded.
	if got := do(http.MethodPatch, "/api/v1/projects/"+seeded.ID, `{"name":"Renamed"}`); got != http.StatusInternalServerError {
		t.Fatalf("rename count with broken drafts = %d, want 500", got)
	}
	srv.drafts = realDrafts

	// get/delete surface non-not-exist errors when the record path is a directory.
	psDir := filepath.Join(t.TempDir(), "projects")
	ps, err := newProjectStore(psDir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.MkdirAll(ps.fileFor("x"), 0o755); err != nil {
		t.Fatalf("make record dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ps.fileFor("y"), "child"), 0o755); err != nil {
		t.Fatalf("make non-empty record dir: %v", err)
	}
	srv.projects = ps
	if got := do(http.MethodPatch, "/api/v1/projects/x", `{"name":"Y"}`); got != http.StatusInternalServerError {
		t.Fatalf("rename with dir record = %d, want 500", got)
	}
	if got := do(http.MethodDelete, "/api/v1/projects/y", ""); got != http.StatusInternalServerError {
		t.Fatalf("delete with non-empty dir record = %d, want 500", got)
	}
	// Save-draft's project-lookup branch fails when projects get() errors.
	if got := do(http.MethodPost, "/api/v1/drafts?projectId=x", draftBody); got != http.StatusInternalServerError {
		t.Fatalf("save draft with broken project lookup = %d, want 500", got)
	}
	// Move-draft's project-lookup branch fails likewise (the draft itself exists).
	if got := do(http.MethodPatch, "/api/v1/drafts/p", `{"projectId":"x"}`); got != http.StatusInternalServerError {
		t.Fatalf("move draft with broken project lookup = %d, want 500", got)
	}
	// Move-draft's draft-get branch fails when the drafts record path is a dir.
	dsDir := filepath.Join(t.TempDir(), "drafts")
	ds, err := newDraftStore(dsDir)
	if err != nil {
		t.Fatalf("newDraftStore: %v", err)
	}
	if err := os.MkdirAll(ds.fileFor("p"), 0o755); err != nil {
		t.Fatalf("make draft record dir: %v", err)
	}
	srv.drafts = ds
	if got := do(http.MethodPatch, "/api/v1/drafts/p", `{"projectId":""}`); got != http.StatusInternalServerError {
		t.Fatalf("move draft with broken draft get = %d, want 500", got)
	}
}

// TestNewFailsWhenProjectDirUnusable covers New's newProjectStore error branch: a
// file sits where the projects subdirectory must be created.
func TestNewFailsWhenProjectDirUnusable(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// A regular file where the projects subdirectory needs to be created.
	if err := os.WriteFile(filepath.Join(dir, "projects"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if srv, err := New(proc, store, dir); err == nil {
		srv.Close()
		t.Fatal("New with an unusable projects dir: want error, got nil")
	}
}
