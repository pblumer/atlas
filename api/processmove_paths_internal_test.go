package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Filing a deployed process into an application, for the requests it refuses
// and for a deployment record that is missing, unreadable or unwritable. The
// in-memory registry is only changed after its record is on disk, so in every
// failure the definition must still be where it was.

// processMovePathsDeploy deploys a one-step process and returns its key.
func processMovePathsDeploy(t *testing.T, s *Server, processID string) uint64 {
	t.Helper()
	code, body := recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost, "/api/v1/deployments",
		strings.NewReader(pendingWorkPathsTaskBPMN(processID)))
	if code != http.StatusOK {
		t.Fatalf("deploy %s: %d %s", processID, code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &dep); err != nil || dep.Key == 0 {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return dep.Key
}

// processMovePathsProject creates an application and returns its id.
func processMovePathsProject(t *testing.T, s *Server) string {
	t.Helper()
	code, body := recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost, "/api/v1/projects",
		strings.NewReader(`{"name":"Onboarding"}`))
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create project: %d %s", code, body)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil || p.ID == "" {
		t.Fatalf("decode project: %v (%s)", err, body)
	}
	return p.ID
}

// processMovePathsMove files one definition into a project.
func processMovePathsMove(t *testing.T, s *Server, key uint64, projectID string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPatch, fmt.Sprintf("/api/v1/processes/%d", key),
		strings.NewReader(`{"projectId":"`+projectID+`"}`))
}

// processMovePathsProjectOf reads where the registry files a definition.
func processMovePathsProjectOf(s *Server, key uint64) string {
	var out string
	s.do(func() {
		if d, ok := s.deployments[key]; ok {
			out = d.ProjectID
		}
	})
	return out
}

// TestProcessMoveRefusesAMalformedRequest before anything is read.
func TestProcessMoveRefusesAMalformedRequest(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	code, body := recertifyHTTPPathsCall(t, h, http.MethodPatch, "/api/v1/processes/not-a-key",
		strings.NewReader(`{"projectId":""}`))
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid definition key") {
		t.Errorf("bad key = %d %s, want 400", code, body)
	}
	key := processMovePathsDeploy(t, srv, "move-me")
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPatch, fmt.Sprintf("/api/v1/processes/%d", key),
		strings.NewReader(`project please`))
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid body") {
		t.Errorf("bad body = %d %s, want 400", code, body)
	}
}

// TestProcessMoveMovesOnlyThisDrawing. Another process deployed beside it is not
// part of its drawing and stays where it is.
func TestProcessMoveMovesOnlyThisDrawing(t *testing.T) {
	srv := newServerForErrors(t)
	mine := processMovePathsDeploy(t, srv, "move-me")
	other := processMovePathsDeploy(t, srv, "leave-me")
	proj := processMovePathsProject(t, srv)

	code, body := processMovePathsMove(t, srv, mine, proj)
	if code != http.StatusOK {
		t.Fatalf("move = %d %s", code, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`"moved":[%d]`, mine)) {
		t.Errorf("answer = %s, want only %d moved", body, mine)
	}
	if got := processMovePathsProjectOf(srv, other); got != "" {
		t.Errorf("the other process was filed into %q", got)
	}
}

// TestProcessMoveARecordThatHasDivergedIsNotInvented. When the registry holds a
// definition its sidecar does not, the move skips it rather than writing a record
// this server made up.
func TestProcessMoveARecordThatHasDivergedIsNotInvented(t *testing.T) {
	srv := newServerForErrors(t)
	key := processMovePathsDeploy(t, srv, "move-me")
	proj := processMovePathsProject(t, srv)
	path := srv.deploys.fileFor(key)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove record: %v", err)
	}

	code, body := processMovePathsMove(t, srv, key, proj)
	if code != http.StatusOK || !strings.Contains(body, `"moved":null`) && !strings.Contains(body, `"moved":[]`) {
		t.Errorf("move = %d %s, want 200 with nothing moved", code, body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a deployment record was written for a definition with none: %v", err)
	}
	if got := processMovePathsProjectOf(srv, key); got != "" {
		t.Errorf("registry files it under %q, want it unmoved", got)
	}
}

// TestProcessMoveARecordThatCannotBeReadOrWrittenIsAFault. Neither leaves the
// registry saying the definition moved.
func TestProcessMoveARecordThatCannotBeReadOrWrittenIsAFault(t *testing.T) {
	t.Run("unreadable", func(t *testing.T) {
		srv := newServerForErrors(t)
		key := processMovePathsDeploy(t, srv, "move-me")
		proj := processMovePathsProject(t, srv)
		if err := os.WriteFile(srv.deploys.fileFor(key), []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt record: %v", err)
		}
		if code, body := processMovePathsMove(t, srv, key, proj); code != http.StatusInternalServerError ||
			!strings.Contains(body, "read deployment") {
			t.Errorf("move = %d %s, want 500", code, body)
		}
		if got := processMovePathsProjectOf(srv, key); got != "" {
			t.Errorf("registry files it under %q", got)
		}
	})

	t.Run("unwritable", func(t *testing.T) {
		srv := newServerForErrors(t)
		key := processMovePathsDeploy(t, srv, "move-me")
		proj := processMovePathsProject(t, srv)
		reconcileApplyPathsBlockSave(t, srv.deploys.fileFor(key))
		if code, body := processMovePathsMove(t, srv, key, proj); code != http.StatusInternalServerError ||
			!strings.Contains(body, "persist deployment") {
			t.Errorf("move = %d %s, want 500", code, body)
		}
		if got := processMovePathsProjectOf(srv, key); got != "" {
			t.Errorf("registry files it under %q although the record was not written", got)
		}
	})
}
