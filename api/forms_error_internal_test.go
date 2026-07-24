package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormHandlerStoreErrors covers the 500 branches of the form handlers by
// corrupting the form store the running server owns (this is a white-box test in
// package api, so it can reach srv.forms). Each corruption targets one handler's
// store-failure path.
func TestFormHandlerStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	do := func(method, path, body string) int {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	// A corrupt hex-named record makes loadAll (list) and get (fetch) fail.
	if err := os.WriteFile(srv.forms.fileFor("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("plant corrupt record: %v", err)
	}
	if code := do(http.MethodGet, "/api/v1/forms", ""); code != http.StatusInternalServerError {
		t.Errorf("list with corrupt record: %d, want 500", code)
	}
	if code := do(http.MethodGet, "/api/v1/forms/bad", ""); code != http.StatusInternalServerError {
		t.Errorf("get corrupt record: %d, want 500", code)
	}
	if err := os.Remove(srv.forms.fileFor("bad")); err != nil {
		t.Fatalf("cleanup corrupt record: %v", err)
	}

	// A directory at a record path makes get's read and delete's remove fail.
	dirPath := srv.forms.fileFor("dir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir record path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if code := do(http.MethodGet, "/api/v1/forms/dir", ""); code != http.StatusInternalServerError {
		t.Errorf("get over a directory: %d, want 500", code)
	}
	if code := do(http.MethodDelete, "/api/v1/forms/dir", ""); code != http.StatusInternalServerError {
		t.Errorf("delete a non-empty directory: %d, want 500", code)
	}

	// A directory at the atomic-write temp path makes save fail.
	tmp := srv.forms.fileFor("saveme") + ".tmp"
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatalf("mkdir temp path: %v", err)
	}
	if code := do(http.MethodPost, "/api/v1/forms", `{"id":"saveme","schema":{"type":"default"}}`); code != http.StatusInternalServerError {
		t.Errorf("save with a blocked temp path: %d, want 500", code)
	}
}
