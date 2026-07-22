package api

import (
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

// TestDraftHandlerStoreErrors drives the store-error branches of the draft
// handlers (500s), which a normal HTTP flow can't reach, by pointing the store at
// broken paths.
func TestDraftHandlerStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	do := func(method, path, body string) int {
		var r *strings.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		var req *http.Request
		if r != nil {
			req = httptest.NewRequest(method, path, r)
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// List/save fail when the drafts directory does not exist.
	srv.drafts = &draftStore{dir: filepath.Join(t.TempDir(), "gone")}
	if code := do(http.MethodGet, "/api/v1/drafts", ""); code != http.StatusInternalServerError {
		t.Fatalf("list with broken store = %d, want 500", code)
	}
	if code := do(http.MethodPost, "/api/v1/drafts", draftBody); code != http.StatusInternalServerError {
		t.Fatalf("save with broken store = %d, want 500", code)
	}

	// get/delete fail (non-not-exist errors) when the record path is a directory.
	good := filepath.Join(t.TempDir(), "drafts")
	ds, err := newDraftStore(good)
	if err != nil {
		t.Fatalf("newDraftStore: %v", err)
	}
	srv.drafts = ds
	recordDir := ds.fileFor("x")
	if err := os.MkdirAll(filepath.Join(recordDir, "child"), 0o755); err != nil {
		t.Fatalf("make record dir: %v", err)
	}
	if code := do(http.MethodGet, "/api/v1/drafts/x/xml", ""); code != http.StatusInternalServerError {
		t.Fatalf("draft xml with dir record = %d, want 500", code)
	}
	if code := do(http.MethodDelete, "/api/v1/drafts/x", ""); code != http.StatusInternalServerError {
		t.Fatalf("delete with non-empty dir record = %d, want 500", code)
	}
}

const draftBody = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
	`<bpmn:process id="p" name="P"/></bpmn:definitions>`

// TestNewFailsWhenDraftDirUnusable covers New's newDraftStore error branch: the
// deployments dir is creatable but a file sits where the drafts dir must go.
func TestNewFailsWhenDraftDirUnusable(t *testing.T) {
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

	// A regular file where the drafts subdirectory needs to be created.
	if err := os.WriteFile(filepath.Join(dir, "drafts"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if srv, err := New(proc, store, dir); err == nil {
		srv.Close()
		t.Fatal("New with an unusable drafts dir: want error, got nil")
	}
}
