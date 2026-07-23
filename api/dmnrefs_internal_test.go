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

// TestDmnRefReadBodyErrors covers the io.ReadAll failure branch of the DMN
// reference handlers by supplying a body that errors on read.
func TestDmnRefReadBodyErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/dmnrefs"},
		{http.MethodPatch, "/api/v1/dmnrefs/x"},
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

// TestDmnRefHandlerStoreErrors drives the store-error branches (500s) of the DMN
// reference handlers, and the DMN-aware branches of the project handlers, by
// pointing the stores at broken paths.
func TestDmnRefHandlerStoreErrors(t *testing.T) {
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
	realDmnrefs := srv.dmnrefs

	// Seed a real project (capture its id) and a real ungrouped reference.
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
	if got := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"D","modelRef":"m"}`); got != http.StatusOK {
		t.Fatalf("seed ref: status=%d", got)
	}
	var seededRef struct {
		ID string `json:"id"`
	}
	// Re-create to capture an id (the seed above discarded its body).
	req = httptest.NewRequest(http.MethodPost, "/api/v1/dmnrefs", strings.NewReader(`{"name":"D2","modelRef":"m2"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &seededRef); err != nil {
		t.Fatalf("decode seeded ref: %v", err)
	}

	brokenDmn := &dmnRefStore{dir: filepath.Join(t.TempDir(), "gone")}

	// Create/list fail when the dmn-refs directory does not exist.
	srv.dmnrefs = brokenDmn
	if got := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"X","modelRef":"m"}`); got != http.StatusInternalServerError {
		t.Fatalf("create with broken store = %d, want 500", got)
	}
	if got := do(http.MethodGet, "/api/v1/dmnrefs", ""); got != http.StatusInternalServerError {
		t.Fatalf("list with broken store = %d, want 500", got)
	}
	// handleListProjects and rename's artifact count both load dmn refs; a broken
	// dmn-refs store surfaces as a 500 in each.
	if got := do(http.MethodGet, "/api/v1/projects", ""); got != http.StatusInternalServerError {
		t.Fatalf("list projects with broken dmn store = %d, want 500", got)
	}
	if got := do(http.MethodPatch, "/api/v1/projects/"+seeded.ID, `{"name":"Renamed"}`); got != http.StatusInternalServerError {
		t.Fatalf("rename count with broken dmn store = %d, want 500", got)
	}
	srv.dmnrefs = realDmnrefs

	// get/delete surface non-not-exist errors when the record path is a directory.
	dsDir := filepath.Join(t.TempDir(), "dmnrefs")
	ds, err := newDmnRefStore(dsDir)
	if err != nil {
		t.Fatalf("newDmnRefStore: %v", err)
	}
	if err := os.MkdirAll(ds.fileFor("x"), 0o755); err != nil {
		t.Fatalf("make record dir: %v", err)
	}
	srv.dmnrefs = ds
	if got := do(http.MethodPatch, "/api/v1/dmnrefs/x", `{"projectId":""}`); got != http.StatusInternalServerError {
		t.Fatalf("move with dir record = %d, want 500", got)
	}
	if err := os.MkdirAll(filepath.Join(ds.fileFor("y"), "child"), 0o755); err != nil {
		t.Fatalf("make non-empty record dir: %v", err)
	}
	if got := do(http.MethodDelete, "/api/v1/dmnrefs/y", ""); got != http.StatusInternalServerError {
		t.Fatalf("delete with non-empty dir record = %d, want 500", got)
	}
	srv.dmnrefs = realDmnrefs

	// Create/move project-lookup branch surfaces a 500 when projects get() errors
	// (a non-not-exist error): point it at a store whose record path is a
	// directory, so the read fails rather than reporting "not found".
	psDir := filepath.Join(t.TempDir(), "projects")
	ps, err := newProjectStore(psDir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.MkdirAll(ps.fileFor("z"), 0o755); err != nil {
		t.Fatalf("make project record dir: %v", err)
	}
	srv.projects = ps
	if got := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"X","modelRef":"m","projectId":"z"}`); got != http.StatusInternalServerError {
		t.Fatalf("create with broken project lookup = %d, want 500", got)
	}
	if got := do(http.MethodPatch, "/api/v1/dmnrefs/"+seededRef.ID, `{"projectId":"z"}`); got != http.StatusInternalServerError {
		t.Fatalf("move with broken project lookup = %d, want 500", got)
	}
	srv.projects = realProjects
}

// TestNewFailsWhenDmnRefDirUnusable covers New's newDmnRefStore error branch: a
// file sits where the dmn-refs subdirectory must be created.
func TestNewFailsWhenDmnRefDirUnusable(t *testing.T) {
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

	// A regular file where the dmn-refs subdirectory needs to be created.
	if err := os.WriteFile(filepath.Join(dir, "dmnrefs"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if srv, err := New(proc, store, dir); err == nil {
		srv.Close()
		t.Fatal("New with an unusable dmn-refs dir: want error, got nil")
	}
}
