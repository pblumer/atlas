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

// Error and edge paths of the ADR-0128 release layer: the store's skip/decode
// branches, and the handler branches that only fire when a store read or write
// fails. The happy paths live in releases_test.go; these are the ones a passing
// publish never reaches.

// TestReleaseStoreLoadAllSkipsForeignFiles covers loadAll's filters: a directory,
// a non-.json file, and a .json file whose stem is not hex are all ignored, so a
// stray file in the data dir cannot break the history.
func TestReleaseStoreLoadAllSkipsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	rs, err := newReleaseStore(dir)
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	if err := rs.save(applicationRelease{ID: "a1", ApplicationID: "app", Version: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Foreign entries the loader must skip.
	if err := os.MkdirAll(filepath.Join(dir, "a-subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write non-hex json: %v", err)
	}

	all, err := rs.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "a1" {
		t.Fatalf("loadAll = %+v, want just the real record", all)
	}
}

// TestReleaseStoreLoadAllOrdersNewestFirst pins the ordering contract the history
// view depends on: grouped by application, highest version first.
func TestReleaseStoreLoadAllOrdersNewestFirst(t *testing.T) {
	rs, err := newReleaseStore(t.TempDir())
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	for _, rec := range []applicationRelease{
		{ID: "r1", ApplicationID: "b", Version: 1},
		{ID: "r3", ApplicationID: "b", Version: 3},
		{ID: "r2", ApplicationID: "b", Version: 2},
		{ID: "r9", ApplicationID: "a", Version: 1},
	} {
		if err := rs.save(rec); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	got, err := rs.forApplication("b")
	if err != nil {
		t.Fatalf("forApplication: %v", err)
	}
	if len(got) != 3 || got[0].Version != 3 || got[1].Version != 2 || got[2].Version != 1 {
		t.Fatalf("forApplication(b) = %+v, want v3,v2,v1", got)
	}
	// An application with no releases reads as an empty list, never nil, so the
	// JSON response is [] rather than null.
	empty, err := rs.forApplication("nobody")
	if err != nil {
		t.Fatalf("forApplication(nobody): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("forApplication(nobody) = %+v, want an empty non-nil slice", empty)
	}
}

// TestReleaseStoreOrdersEqualVersionsById covers the sort's tie-break: two
// releases of one application at the same version order deterministically by id,
// so the history never reshuffles between reads.
func TestReleaseStoreOrdersEqualVersionsById(t *testing.T) {
	rs, err := newReleaseStore(t.TempDir())
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	for _, id := range []string{"zz", "aa", "mm"} {
		if err := rs.save(applicationRelease{ID: id, ApplicationID: "app", Version: 7}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	got, err := rs.forApplication("app")
	if err != nil {
		t.Fatalf("forApplication: %v", err)
	}
	if len(got) != 3 || got[0].ID != "aa" || got[1].ID != "mm" || got[2].ID != "zz" {
		t.Fatalf("tie-break order = %+v, want aa, mm, zz", got)
	}
}

// TestDeleteForApplicationRemoveError covers the os.Remove failure branch: a
// record path that is a non-empty directory cannot be removed, and the error
// surfaces instead of being reported as a clean delete.
func TestDeleteForApplicationRemoveError(t *testing.T) {
	dir := t.TempDir()
	rs, err := newReleaseStore(dir)
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	if err := rs.save(applicationRelease{ID: "victim", ApplicationID: "app", Version: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Replace the record file with a non-empty directory at the same path.
	if err := os.Remove(rs.fileFor("victim")); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(rs.fileFor("victim"), "child"), 0o755); err != nil {
		t.Fatalf("make record dir: %v", err)
	}
	// loadAll must still see it as a record (the name is unchanged) for the delete
	// loop to reach the Remove; a directory entry is skipped, so seed a sibling that
	// keeps the application in the listing and points the loop at the bad path.
	if err := rs.save(applicationRelease{ID: "victim2", ApplicationID: "app", Version: 2}); err != nil {
		t.Fatalf("save sibling: %v", err)
	}
	if err := os.Remove(rs.fileFor("victim2")); err != nil {
		t.Fatalf("remove sibling: %v", err)
	}
	// Write a valid record whose *stored* id maps to the directory path, so the
	// delete loop tries to remove the undeletable path.
	rec := applicationRelease{ID: "victim", ApplicationID: "app", Version: 1}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(rs.fileFor("marker"), raw, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := rs.deleteForApplication("app"); err == nil {
		t.Fatal("deleteForApplication over an undeletable record: want an error")
	}
}

// TestReleaseStoreLoadAllDecodeError covers the malformed-record branch: a
// hex-named .json file that is not a release fails the load loudly rather than
// being silently dropped.
func TestReleaseStoreLoadAllDecodeError(t *testing.T) {
	dir := t.TempDir()
	rs, err := newReleaseStore(dir)
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	if err := os.WriteFile(rs.fileFor("bad"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := rs.loadAll(); err == nil {
		t.Fatal("loadAll with a malformed record: want an error")
	}
}

// TestReleaseStoreErrorsOnMissingDir covers loadAll/forApplication/
// deleteForApplication against a directory that does not exist.
func TestReleaseStoreErrorsOnMissingDir(t *testing.T) {
	broken := &releaseStore{dir: filepath.Join(t.TempDir(), "gone")}
	if _, err := broken.loadAll(); err == nil {
		t.Error("loadAll on a missing dir: want an error")
	}
	if _, err := broken.forApplication("x"); err == nil {
		t.Error("forApplication on a missing dir: want an error")
	}
	if err := broken.deleteForApplication("x"); err == nil {
		t.Error("deleteForApplication on a missing dir: want an error")
	}
}

// TestNewReleaseStoreRejectsUnusableDir covers the MkdirAll failure branch: a path
// whose parent is a regular file cannot become a directory.
func TestNewReleaseStoreRejectsUnusableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := newReleaseStore(filepath.Join(file, "releases")); err == nil {
		t.Fatal("newReleaseStore under a file path: want an error")
	}
}

// TestDeleteForApplicationIsIdempotent covers the "nothing removed" early return:
// deleting the releases of an application that has none is a no-op success.
func TestDeleteForApplicationIsIdempotent(t *testing.T) {
	rs, err := newReleaseStore(t.TempDir())
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	if err := rs.save(applicationRelease{ID: "keep", ApplicationID: "other", Version: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := rs.deleteForApplication("absent"); err != nil {
		t.Fatalf("deleteForApplication(absent) = %v, want nil", err)
	}
	all, err := rs.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("unrelated release was removed: %+v", all)
	}
}

// TestLoadReleaseVersionsRebuildsCounter covers the startup rebuild directly,
// including its error branch, without going through a full server boot.
func TestLoadReleaseVersionsRebuildsCounter(t *testing.T) {
	rs, err := newReleaseStore(t.TempDir())
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	for _, rec := range []applicationRelease{
		{ID: "x1", ApplicationID: "a", Version: 1},
		{ID: "x2", ApplicationID: "a", Version: 4},
		{ID: "x3", ApplicationID: "b", Version: 2},
	} {
		if err := rs.save(rec); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	s := &Server{releases: rs, appVersions: map[string]int32{}}
	if err := s.loadReleaseVersions(); err != nil {
		t.Fatalf("loadReleaseVersions: %v", err)
	}
	if s.appVersions["a"] != 4 || s.appVersions["b"] != 2 {
		t.Fatalf("appVersions = %v, want a=4 b=2", s.appVersions)
	}

	broken := &Server{releases: &releaseStore{dir: filepath.Join(t.TempDir(), "gone")}, appVersions: map[string]int32{}}
	if err := broken.loadReleaseVersions(); err == nil {
		t.Fatal("loadReleaseVersions with a broken store: want an error")
	}
}

// TestPublishBadBody covers the body-handling branches of the publish handler: an
// unreadable body and malformed JSON are both 400s, before any deploy happens.
func TestPublishBadBody(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/x/publish", errReader{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("publish with an unreadable body = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/applications/x/publish", strings.NewReader(`{`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("publish with malformed JSON = %d, want 400", rec.Code)
	}
}

// TestReleaseHandlerStoreErrors drives the 500 branches of the release handlers by
// pointing the release store at a path that cannot be read.
func TestReleaseHandlerStoreErrors(t *testing.T) {
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

	// mkApp seeds a real application and returns its id.
	mkApp := func(name string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/applications", strings.NewReader(`{"name":"`+name+`"}`))
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed application %s = %d", name, rec.Code)
		}
		var a struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatalf("decode application: %v", err)
		}
		return a.ID
	}
	app := mkApp("Errors")
	doomed := mkApp("Doomed")

	// Seed a deployable draft into `app` while the stores still work, so the publish
	// case below fails only on recording the release, not on the deploy.
	draftReq := httptest.NewRequest(http.MethodPost, "/api/v1/drafts?projectId="+app, strings.NewReader(
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="relerr" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`))
	draftReq.Header.Set("Content-Type", "application/xml")
	draftRec := httptest.NewRecorder()
	h.ServeHTTP(draftRec, draftReq)
	if draftRec.Code != http.StatusOK {
		t.Fatalf("seed draft = %d body=%s", draftRec.Code, draftRec.Body.String())
	}

	realReleases := srv.releases
	// A store whose directory does not exist: reads fail, and so does the atomic
	// write (its temp file cannot be created), which is what the publish case needs.
	srv.releases = &releaseStore{dir: filepath.Join(t.TempDir(), "gone")}

	if got := do(http.MethodGet, "/api/v1/applications/"+app+"/releases", ""); got != http.StatusInternalServerError {
		t.Errorf("list releases with a broken store = %d, want 500", got)
	}
	if got := do(http.MethodGet, "/api/v1/applications/"+app+"/deployments", ""); got != http.StatusInternalServerError {
		t.Errorf("deployments view with a broken store = %d, want 500", got)
	}
	// Deleting an application also clears its releases, so a broken store surfaces
	// there too rather than reporting a clean delete. Releases are cleared first, so
	// the failure leaves the application intact and a retry can self-heal.
	if got := do(http.MethodDelete, "/api/v1/applications/"+doomed, ""); got != http.StatusInternalServerError {
		t.Errorf("delete application with a broken release store = %d, want 500", got)
	}
	// A publish whose bundle deploys cleanly but whose release cannot be recorded
	// reports the failure rather than pretending the publish did not happen.
	if got := do(http.MethodPost, "/api/v1/applications/"+app+"/publish", `{"note":"x"}`); got != http.StatusInternalServerError {
		t.Errorf("publish with an unwritable release store = %d, want 500", got)
	}

	// The bundle also loads the application's DMN references; a broken dmn-ref store
	// fails the publish during collection, before anything is deployed.
	realDmnrefs := srv.dmnrefs
	srv.dmnrefs = &dmnRefStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := do(http.MethodPost, "/api/v1/applications/"+app+"/publish", `{}`); got != http.StatusInternalServerError {
		t.Errorf("publish with a broken dmn-ref store = %d, want 500", got)
	}
	srv.dmnrefs = realDmnrefs

	srv.releases = realReleases
	if got := do(http.MethodDelete, "/api/v1/applications/"+doomed, ""); got != http.StatusNoContent {
		t.Errorf("retry delete after the store recovered = %d, want 204 (the application survived the failure)", got)
	}
}
