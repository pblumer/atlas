package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Branches of the application (ADR-0034 project) handlers that a passing request
// never reaches: the visibility edit path, the artifact-count failure during an
// update, and the unreadable-record path that must read as a 500 rather than a
// 404. Plus the dmn-ref half of the artifact count, which the drafts-only tests
// never exercise.

func TestProjectUpdateAndDeleteErrorBranches(t *testing.T) {
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

	code, body := do(http.MethodPost, "/api/v1/applications", `{"name":"Editable"}`)
	if code != http.StatusOK {
		t.Fatalf("create = %d", code)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Setting visibility is its own PATCH branch, separate from a rename.
	code, body = do(http.MethodPatch, "/api/v1/applications/"+app.ID, `{"visibility":"shared"}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"visibility":"shared"`) {
		t.Fatalf("set visibility = %d body=%s", code, body)
	}

	// Count failure during update: a broken dmn-ref store makes the artifact count
	// fail after the save has already succeeded, which is its own 500 branch.
	realDmnrefs := srv.dmnrefs
	srv.dmnrefs = &dmnRefStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got, _ := do(http.MethodPatch, "/api/v1/applications/"+app.ID, `{"name":"Renamed"}`); got != http.StatusInternalServerError {
		t.Errorf("update with a broken count = %d, want 500", got)
	}
	srv.dmnrefs = realDmnrefs

	// A read failure on the project itself is a 500, not a 404: make the record path
	// a directory so ReadFile errors rather than reporting "not found".
	realProjects := srv.projects
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.MkdirAll(ps.fileFor("unreadable"), 0o755); err != nil {
		t.Fatalf("make record dir: %v", err)
	}
	srv.projects = ps
	if got, _ := do(http.MethodDelete, "/api/v1/applications/unreadable", ""); got != http.StatusInternalServerError {
		t.Errorf("delete with an unreadable record = %d, want 500", got)
	}
	if got, _ := do(http.MethodPatch, "/api/v1/applications/unreadable", `{"name":"X"}`); got != http.StatusInternalServerError {
		t.Errorf("update with an unreadable record = %d, want 500", got)
	}
	srv.projects = realProjects

	// The project delete's own failure branch (get succeeds, the removal fails) is
	// not reachable here: the tests run as root, so directory permissions cannot be
	// used to block the unlink, and any record shape that breaks the removal also
	// breaks the read that precedes it. Left uncovered deliberately (ADR-0018)
	// rather than contrived.
}

// TestCountArtifactsIncludesDecisionReferences covers the dmn-ref half of the
// artifact count: an application's artifact total is drafts *plus* registered
// decision references, which the listing and the update response both report.
func TestCountArtifactsIncludesDecisionReferences(t *testing.T) {
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

	code, body := do(http.MethodPost, "/api/v1/applications", `{"name":"Counted"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create = %d", code)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// One draft and one decision reference, both filed under the application.
	if got, b := do(http.MethodPost, "/api/v1/drafts?projectId="+app.ID,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="counted"><startEvent id="s"/></process>
</definitions>`, "application/xml"); got != http.StatusOK {
		t.Fatalf("save draft = %d body=%s", got, b)
	}
	if got, b := do(http.MethodPost, "/api/v1/dmnrefs",
		`{"name":"Rule","modelRef":"m1","projectId":"`+app.ID+`"}`, "application/json"); got != http.StatusOK {
		t.Fatalf("create dmn ref = %d body=%s", got, b)
	}

	// The rename response recomputes the count over both artifact kinds.
	code, body = do(http.MethodPatch, "/api/v1/applications/"+app.ID, `{"name":"Counted v2"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("rename = %d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"artifacts":2`) {
		t.Fatalf("artifact count = %s, want 2 (one draft + one decision reference)", body)
	}
}
