package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
)

// Branches of the git-binding handlers (ADR-0134 slice 4b) that a working request
// never reaches: the ones where the binding store itself cannot be read or
// written. They matter because "not bound" and "cannot tell whether it is bound"
// are different answers, and reporting the second as the first would send an
// operator looking in the wrong place.

// TestGitBindingHandlerStoreFailures drives each handler against a store that
// fails, and checks every one reports a 500 rather than a plausible-looking
// success.
func TestGitBindingHandlerStoreFailures(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	code, body := do(http.MethodPost, "/api/v1/applications", `{"name":"Onboarding"}`)
	if code != http.StatusOK {
		t.Fatalf("create application = %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	base := "/api/v1/applications/" + app.ID
	bindBody := `{"repoUrl":"https://example.com/org/app.git"}`

	// A store whose directory does not exist: the read finds nothing (which reads
	// as "not bound", correctly), and the write then fails.
	real := srv.gitBindings
	srv.gitBindings = &gitBindingStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got, body := do(http.MethodPut, base+"/git", bindBody); got != http.StatusInternalServerError {
		t.Errorf("bind with an unwritable store = %d %s, want 500", got, body)
	}
	if got, body := do(http.MethodDelete, base+"/git", ""); got != http.StatusInternalServerError {
		t.Errorf("unbind with an unwritable store = %d %s, want 500", got, body)
	}
	srv.gitBindings = real

	// Now a record that exists but cannot be read: every handler that consults the
	// binding has to surface that rather than treating it as absent.
	if got, body := do(http.MethodPut, base+"/git", bindBody); got != http.StatusOK {
		t.Fatalf("bind = %d %s", got, body)
	}
	recordPath := srv.gitBindings.fileFor(app.ID)
	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	if err := os.MkdirAll(recordPath, 0o755); err != nil {
		t.Fatalf("mkdir over record: %v", err)
	}
	for _, c := range []struct {
		name, method, path, body string
	}{
		{"status", http.MethodGet, base + "/git", ""},
		{"rebind", http.MethodPut, base + "/git", bindBody},
		{"import", http.MethodPost, base + "/git/import", ""},
		{"commit", http.MethodPost, base + "/git/commit", ""},
	} {
		if got, body := do(c.method, c.path, c.body); got != http.StatusInternalServerError {
			t.Errorf("%s with an unreadable record = %d %s, want 500", c.name, got, body)
		}
	}
}

// TestGitCommitReportsAnUnreadableApplication: the commit gathers the application's
// source on the run loop, and a store that cannot be read there means there is
// nothing honest to push.
func TestGitCommitReportsAnUnreadableApplication(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	code, body := do(http.MethodPost, "/api/v1/applications", `{"name":"Onboarding"}`)
	if code != http.StatusOK {
		t.Fatalf("create application = %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	base := "/api/v1/applications/" + app.ID
	if got, body := do(http.MethodPut, base+"/git", `{"repoUrl":"https://example.com/org/app.git"}`); got != http.StatusOK {
		t.Fatalf("bind = %d %s", got, body)
	}

	// The drafts store fails, so the tree cannot be built: refuse before any
	// network I/O rather than pushing an application with pieces missing.
	srv.drafts = &draftStore{dir: filepath.Join(t.TempDir(), "gone")}
	got, out := do(http.MethodPost, base+"/git/commit", `{"message":"x"}`)
	if got != http.StatusInternalServerError {
		t.Fatalf("commit with a broken draft store = %d %s, want 500", got, out)
	}
	if !strings.Contains(string(out), "prepare commit") {
		t.Errorf("body = %s", out)
	}
}

// TestGitImportReportsAStoreFailure: an import that cannot be written into the
// artifact stores is a 500, not a success with a smaller count. It drives the real
// transport against a local repository, then breaks the store the tree has to land
// in.
func TestGitImportReportsAStoreFailure(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body, contentType string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	code, body := do(http.MethodPost, "/api/v1/applications", `{"name":"Onboarding"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application = %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p1" name="Erster" isExecutable="true"><startEvent id="s"/></process>
</definitions>`
	if code, body := do(http.MethodPost, "/api/v1/drafts?projectId="+app.ID, xml, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}

	repoDir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := git.PlainInit(repoDir, true); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	base := "/api/v1/applications/" + app.ID
	if code, body := do(http.MethodPut, base+"/git",
		`{"repoUrl":"file://`+repoDir+`","branch":"main"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("bind = %d %s", code, body)
	}
	if code, body := do(http.MethodPost, base+"/git/commit", `{"message":"seed"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("commit = %d %s", code, body)
	}

	// The tree is in the repository; now the store it has to land in fails.
	srv.drafts = &draftStore{dir: filepath.Join(t.TempDir(), "gone")}
	code, out := do(http.MethodPost, base+"/git/import", "", "application/json")
	if code != http.StatusInternalServerError {
		t.Fatalf("import with a broken draft store = %d %s, want 500", code, out)
	}
	if !strings.Contains(string(out), "import from git") {
		t.Errorf("body = %s", out)
	}
}

// TestSourceUploadReportsAStoreFailure: the archive upload lands in the same
// stores a git import does, and a failure there is a 500 rather than a success
// with a smaller count.
func TestSourceUploadReportsAStoreFailure(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path string, body []byte, contentType string) (int, []byte) {
		var req *http.Request
		if len(body) > 0 {
			req = httptest.NewRequest(method, path, bytes.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	code, body := do(http.MethodPost, "/api/v1/applications", []byte(`{"name":"Onboarding"}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application = %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p1" name="Erster" isExecutable="true"><startEvent id="s"/></process>
</definitions>`
	if code, body := do(http.MethodPost, "/api/v1/drafts?projectId="+app.ID, []byte(xml), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	code, archive := do(http.MethodGet, "/api/v1/applications/"+app.ID+"/source", nil, "")
	if code != http.StatusOK {
		t.Fatalf("export = %d", code)
	}

	srv.drafts = &draftStore{dir: filepath.Join(t.TempDir(), "gone")}
	code, out := do(http.MethodPost, "/api/v1/applications/source", archive, "application/gzip")
	if code != http.StatusInternalServerError {
		t.Fatalf("import with a broken draft store = %d %s, want 500", code, out)
	}
	if !strings.Contains(string(out), "import source") {
		t.Errorf("body = %s", out)
	}
}
