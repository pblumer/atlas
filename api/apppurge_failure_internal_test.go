package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What a purge and a forced delete do when a step cannot be taken
// (ADR-0468). A store whose directory
// has been replaced by a file refuses every read and every removal on every OS
// (sidecar.Store's brokenDir), which is the lever here: each step is made to fail in
// turn, and each failure has to be reported — never read as "nothing there" — with
// the application record still in place for the request that finishes the work.

// breakStore puts a regular file where a store's directory is.
func breakStore(t *testing.T, dataDir, store string) {
	t.Helper()
	path := filepath.Join(dataDir, store)
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("remove %s: %v", store, err)
	}
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("break %s: %v", store, err)
	}
}

// TestPurgePreviewRefusesAStoreItCannotRead: every store the inventory reads is
// asked, and one that cannot answer fails the preview and the purge alike — an
// application whose forms cannot be listed is not an application without forms.
func TestPurgePreviewRefusesAStoreItCannotRead(t *testing.T) {
	for _, store := range []string{
		"projects", "decisions", "drafts", "dmn-drafts", "dmnrefs", "forms",
		"playground-scenarios", "panorama-models", "information-models",
	} {
		t.Run(store, func(t *testing.T) {
			dir := t.TempDir()
			st := bootDecisionStack(t, dir)
			defer st.shutdown()
			app := st.x.mkProject("Unreadable " + store)
			breakStore(t, dir, store)

			if code, b := st.x.do(http.MethodGet, "/api/v1/applications/"+app+"/deletion-preview", ""); code != http.StatusInternalServerError {
				t.Errorf("preview: %d %s, want 500", code, b)
			}
			if code, b := st.x.do(http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", ""); code != http.StatusInternalServerError {
				t.Errorf("purge: %d %s, want 500", code, b)
			}
			if store == "projects" {
				return // the record cannot be read back from a broken store either
			}
			st.srv.do(func() {
				if _, ok, _ := st.srv.projects.Get(app); !ok {
					t.Error("a purge that could not read the application deleted it")
				}
			})
		})
	}
}

// TestDeletingTheApplicationRecordStopsAtTheStepThatFails: the metadata reachable only
// through the application's id goes before the record itself, so a failure on the way
// leaves the record for the request that finishes the work — the order a plain delete
// and the last step of a purge share.
func TestDeletingTheApplicationRecordStopsAtTheStepThatFails(t *testing.T) {
	for _, store := range []string{"releases", "grant-audit", "application-constants", "projects"} {
		t.Run(store, func(t *testing.T) {
			dir := t.TempDir()
			st := bootDecisionStack(t, dir)
			defer st.shutdown()
			id := st.x.mkProject("Record " + store)
			breakStore(t, dir, store)

			var err error
			st.srv.do(func() { err = st.srv.deleteApplicationRecordOnLoop(id) })
			if err == nil {
				t.Fatalf("deleting the record with %s broken reported success", store)
			}
			if store == "projects" {
				return
			}
			st.srv.do(func() {
				if _, ok, _ := st.srv.projects.Get(id); !ok {
					t.Error("the record went although the metadata before it could not be removed")
				}
			})
		})
	}
}

// TestPurgeContentsStopsAtTheStepThatFails: each removal after the deployed versions
// is made to fail in turn. The error names it, and the application record — the last
// step — is still there.
func TestPurgeContentsStopsAtTheStepThatFails(t *testing.T) {
	for _, tc := range []struct {
		store string
		inv   func(app project) applicationInventory
	}{
		{"decisions", func(app project) applicationInventory {
			return applicationInventory{app: app, decisions: []persistedDecision{{Key: 77}}}
		}},
		{"dmnrefs", func(app project) applicationInventory {
			return applicationInventory{app: app, refs: []dmnRef{{ID: "ref-1", ModelRef: "model-1"}}}
		}},
		{"dmn-drafts", func(app project) applicationInventory {
			return applicationInventory{app: app, dmnDrafts: []dmnDraft{{ID: "dd-1"}}}
		}},
		{"drafts", func(app project) applicationInventory {
			return applicationInventory{app: app, drafts: []draft{{ProcessID: "p-1"}}}
		}},
		{"forms", func(app project) applicationInventory {
			return applicationInventory{app: app, forms: []form{{ID: "f-1"}}}
		}},
		{"playground-scenarios", func(app project) applicationInventory {
			return applicationInventory{app: app, scenarios: []playgroundScenario{{ID: "sc-1"}}}
		}},
		{"panorama-models", func(app project) applicationInventory { return applicationInventory{app: app} }},
		{"information-models", func(app project) applicationInventory { return applicationInventory{app: app} }},
	} {
		t.Run(tc.store, func(t *testing.T) {
			dir := t.TempDir()
			st := bootDecisionStack(t, dir)
			defer st.shutdown()
			id := st.x.mkProject("Stuck " + tc.store)
			var app project
			st.srv.do(func() { app, _, _ = st.srv.projects.Get(id) })
			breakStore(t, dir, tc.store)

			var err error
			st.srv.do(func() { err = st.srv.purgeContentsOnLoop(tc.inv(app)) })
			if err == nil {
				t.Fatalf("purging with %s broken reported success", tc.store)
			}
			st.srv.do(func() {
				if _, ok, _ := st.srv.projects.Get(id); !ok {
					t.Error("the application record went although an earlier step failed")
				}
			})
		})
	}
}

// TestPurgeKeepsAModelAnotherApplicationReferences: the stored model behind a
// reference goes with the purged application only when no reference elsewhere
// points at it — deleting it would leave the other application's reference
// unresolved, the one thing ADR-0336 refuses to do behind an author's back.
func TestPurgeKeepsAModelAnotherApplicationReferences(t *testing.T) {
	st := bootDecisionStack(t, t.TempDir())
	defer st.shutdown()
	x := st.x
	uploadModel(t, x, "shared-elig", eligibilityDMN("approve"))
	purged, kept := x.mkProject("Purged"), x.mkProject("Kept")
	x.addRef(purged, "Purged eligibility", "shared-elig")
	x.addRef(kept, "Kept eligibility", "shared-elig")

	if code, b := x.do(http.MethodDelete, "/api/v1/applications/"+purged+"?purge=true", ""); code != http.StatusNoContent {
		t.Fatalf("purge: %d %s", code, b)
	}
	if _, b := x.do(http.MethodGet, "/api/v1/dmn-models", ""); !strings.Contains(string(b), "shared-elig") {
		t.Fatalf("the model another application references was deleted: %s", b)
	}
}

// TestRemoveDeploymentsWithoutTheRightToCancel: a caller who may not cancel
// instances removes a version only while nothing runs under it. An instance that
// started after the caller was told there was none stops the removal rather than
// being cancelled on their behalf.
func TestRemoveDeploymentsWithoutTheRightToCancel(t *testing.T) {
	st := bootDecisionStack(t, t.TempDir())
	defer st.shutdown()
	key := deployProcess(t, st.x, `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="waits" isExecutable="true">
    <startEvent id="s"/><userTask id="wait"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/><sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`)
	if code, b := st.x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}"); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, b)
	}
	_, err := st.srv.removeDeployments([]uint64{key}, false)
	var forbid errMayNotCancel
	if !errors.As(err, &forbid) || forbid.running != 1 {
		t.Fatalf("removeDeployments without the right = %v, want errMayNotCancel for 1 instance", err)
	}
	st.srv.do(func() {
		if _, ok := st.srv.deployments[key]; !ok {
			t.Error("the version was removed although its instance was not cancelled")
		}
	})
}

// TestRemoveDeploymentsErrorAnswers pins how each failure of removeDeployments is
// answered: instances that kept starting are a conflict the caller can resolve by
// deactivating the process, a missing right is 403, anything else the server's.
func TestRemoveDeploymentsErrorAnswers(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		want string
	}{
		{errStillRunning{running: 3}, http.StatusConflict, "3 instance(s) were still running"},
		{errMayNotCancel{running: 2}, http.StatusForbidden, "needs the operator role"},
		{errors.New("disk on fire"), http.StatusInternalServerError, "disk on fire"},
	} {
		rec := httptest.NewRecorder()
		removeDeploymentsError(rec, tc.err)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%T: %d %s, want %d containing %q", tc.err, rec.Code, rec.Body.String(), tc.code, tc.want)
		}
	}
}

// TestTheSystemApplicationCannotBePurged: the protected application (ADR-0122) is
// refused a preview and a purge, for every caller.
func TestTheSystemApplicationCannotBePurged(t *testing.T) {
	_, x := newSystemServer(t, WithSystemProcesses())
	if code, b := x.do(http.MethodGet, "/api/v1/applications/"+systemProjectID+"/deletion-preview", ""); code != http.StatusForbidden {
		t.Errorf("preview of the system application: %d %s, want 403", code, b)
	}
	if code, b := x.do(http.MethodDelete, "/api/v1/applications/"+systemProjectID+"?purge=true", ""); code != http.StatusForbidden {
		t.Errorf("purge of the system application: %d %s, want 403", code, b)
	}
}
