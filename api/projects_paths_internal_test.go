package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Applications (ADR-0034, ADR-0189) when the Panorama models filed in them cannot
// be counted, and when a renamed application cannot be written. A listing without
// the Panorama counts would understate what an application holds, and a delete
// that could not count them could orphan models that belong to nobody else.

// projectsPathsList lists the applications.
func projectsPathsList(t *testing.T, s *Server) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, "/api/v1/projects", nil)
}

// TestProjectsPanoramaModelsThatCannotBeCountedStopTheAnswer, for the listing and
// for a delete — which keeps the application.
func TestProjectsPanoramaModelsThatCannotBeCountedStopTheAnswer(t *testing.T) {
	srv := newServerForErrors(t)
	id := processMovePathsProject(t, srv)
	recertifyHTTPPathsBreakDir(t, filepath.Join(srv.dataDir, "panorama-models"))

	if code, body := projectsPathsList(t, srv); code != http.StatusInternalServerError ||
		!strings.Contains(body, "list projects") {
		t.Errorf("list = %d %s, want 500", code, body)
	}
	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodDelete, "/api/v1/projects/"+id, nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "delete project") {
		t.Errorf("delete = %d %s, want 500", code, body)
	}
	var (
		ok  bool
		err error
	)
	srv.do(func() { _, ok, err = srv.projects.Get(id) })
	if err != nil || !ok {
		t.Errorf("the application is gone after a refused delete: ok=%v err=%v", ok, err)
	}
}

// TestProjectsARenameThatCannotBeWrittenKeepsTheOldName.
func TestProjectsARenameThatCannotBeWrittenKeepsTheOldName(t *testing.T) {
	srv := newServerForErrors(t)
	id := processMovePathsProject(t, srv)
	reconcileApplyPathsBlockSave(t, srv.projects.FileFor(id))

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPatch, "/api/v1/projects/"+id,
		strings.NewReader(`{"name":"Offboarding"}`))
	if code != http.StatusInternalServerError {
		t.Errorf("rename = %d %s, want 500", code, body)
	}
	_, list := projectsPathsList(t, srv)
	var got []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(list), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, list)
	}
	name := ""
	for _, p := range got {
		if p.ID == id {
			name = p.Name
		}
	}
	if name != "Onboarding" {
		t.Errorf("application %s is named %q in %+v, want the old name kept", id, name, got)
	}
}
