package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The failure paths of a forced delete and a purge
// (ADR-0468), in the shape the other
// handler-path tests use: a directory where a record is expected, which no OS will
// remove, and a record cut short, which no store can read. What these hold is the
// order of the steps — a failure leaves the application in place, and the same request
// finishes the work once the obstacle is gone.

// purgeBlock puts a non-empty directory in place of one record, so removing it fails,
// and returns the function that clears it again.
func purgeBlock(t *testing.T, dir, rel string) func() {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}
	handlersWrite(t, dir, filepath.Join(rel, "keep"), "x")
	return func() {
		if err := os.RemoveAll(path); err != nil {
			t.Fatalf("clear %s: %v", rel, err)
		}
	}
}

// TestForcedDeleteRefusedWhenItsRecordCannotBeRemoved: a forced delete is
// acknowledged only once the record is gone, as a plain one is.
func TestForcedDeleteRefusedWhenItsRecordCannotBeRemoved(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersDeploy(t, s.ts, diagramBPMN)
	purgeBlock(t, dir, filepath.Join("deployments", "1.json"))

	code, body := doReq(t, s.ts, http.MethodDelete, "/api/v1/processes/1?force=true", "", "")
	handlersRefusal(t, "forced delete", code, body, http.StatusInternalServerError, "delete deployment")
	if keys := handlersProcessKeys(t, s.ts); len(keys) != 1 {
		t.Errorf("after a refused forced delete the listing holds %v, want the definition still there", keys)
	}
	if code, body := doReq(t, s.ts, http.MethodDelete, "/api/v1/processes/99?force=true", "", ""); code != http.StatusNotFound {
		t.Errorf("forced delete of an unknown key: %d %s, want 404", code, body)
	}
}

// TestPurgeThatStopsHalfWayIsFinishedByRepeatingIt: a deployed version and then a
// stored DMN model that cannot be removed each stop the purge with the application
// still there, and the same request finishes the work once each obstacle is cleared.
func TestPurgeThatStopsHalfWayIsFinishedByRepeatingIt(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	app := mkApp(t, s.ts, "Halfway")
	if code, b := doReq(t, s.ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("halfway"), "application/xml"); code != http.StatusOK {
		t.Fatalf("draft: %d %s", code, b)
	}
	if code, b := doReq(t, s.ts, http.MethodPost, "/api/v1/forms",
		`{"id":"halfway-form","name":"Halfway","projectId":"`+app+`","schema":{"type":"default"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("form: %d %s", code, b)
	}
	ref := uploadModel(t, s.ts, "halfway", freigabeDMN)
	addDecisionIn(t, s.ts, app, "Freigabestufe", ref)
	if code, res := publishApp(t, s.ts, app, ""); code != http.StatusOK || !res.Deployed {
		t.Fatalf("publish: %d %+v", code, res)
	}

	stillThere := func(when string) {
		t.Helper()
		if _, b := doReq(t, s.ts, http.MethodGet, "/api/v1/applications", "", ""); !strings.Contains(string(b), app) {
			t.Fatalf("%s: the application is gone, so a retry could not finish the purge", when)
		}
	}

	keys := handlersProcessKeys(t, s.ts)
	if len(keys) != 1 {
		t.Fatalf("deployed %v, want the one process", keys)
	}
	clear := purgeBlock(t, dir, filepath.Join("deployments", fmt.Sprintf("%d.json", keys[0])))
	code, body := doReq(t, s.ts, http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", "", "")
	handlersRefusal(t, "purge with a stuck deployment", code, body, http.StatusInternalServerError, "delete deployment")
	stillThere("after a stuck deployment")
	clear()

	clear = purgeBlock(t, dir, filepath.Join("dmn-models", ref+".dmn"))
	code, body = doReq(t, s.ts, http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", "", "")
	handlersRefusal(t, "purge with a stuck model", code, body, http.StatusInternalServerError, "purge application")
	stillThere("after a stuck model")
	clear()

	if code, b := doReq(t, s.ts, http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", "", ""); code != http.StatusNoContent {
		t.Fatalf("repeated purge: %d %s, want 204", code, b)
	}
	if _, b := doReq(t, s.ts, http.MethodGet, "/api/v1/applications", "", ""); strings.Contains(string(b), app) {
		t.Fatal("the repeated purge left the application")
	}
	if keys := handlersProcessKeys(t, s.ts); len(keys) != 0 {
		t.Errorf("the repeated purge left %v deployed", keys)
	}
	// Purging what is already gone is the outcome the caller asked for.
	if code, b := doReq(t, s.ts, http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", "", ""); code != http.StatusNoContent {
		t.Fatalf("purge of an absent application: %d %s, want 204", code, b)
	}
}

// TestPurgeAndPreviewRefusedWhenAStoreCannotBeRead: a draft record cut short means the
// application's contents cannot be listed, and a purge that cannot list what it
// deletes deletes nothing.
func TestPurgeAndPreviewRefusedWhenAStoreCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	app := mkApp(t, s.ts, "Unreadable")
	handlersWrite(t, dir, filepath.Join("drafts", handlersHexName("cut")), handlersCutRecord)

	code, body := doReq(t, s.ts, http.MethodGet, "/api/v1/applications/"+app+"/deletion-preview", "", "")
	handlersRefusal(t, "preview", code, body, http.StatusInternalServerError, "read application")
	code, body = doReq(t, s.ts, http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", "", "")
	handlersRefusal(t, "purge", code, body, http.StatusInternalServerError, "read application")
	// The listing reads the same store, so the record is mended before asking it.
	if err := os.Remove(filepath.Join(dir, "drafts", handlersHexName("cut"))); err != nil {
		t.Fatal(err)
	}
	if _, b := doReq(t, s.ts, http.MethodGet, "/api/v1/applications", "", ""); !strings.Contains(string(b), app) {
		t.Fatal("a purge that could not read the application deleted it")
	}
	if code, b := doReq(t, s.ts, http.MethodGet, "/api/v1/applications/missing/deletion-preview", "", ""); code != http.StatusNotFound {
		t.Errorf("preview of an unknown application: %d %s, want 404", code, b)
	}
}

// TestPurgePreviewRefusesWhatItCannotAskOutside: what outside the application depends
// on its processes is read from the catalogue and the order stores, and a store that
// cannot answer fails the preview rather than reporting that nothing depends on them.
func TestPurgePreviewRefusesWhatItCannotAskOutside(t *testing.T) {
	for _, tc := range []struct{ store, want string }{
		{"catalog", "read application"},
		{filepath.Join("catalog", "releases"), "read application"},
		{"orders", "check orders"},
	} {
		t.Run(tc.store, func(t *testing.T) {
			dir := t.TempDir()
			s := boot(t, dir)
			defer s.shutdown()
			app := mkApp(t, s.ts, "Outside "+tc.store)
			if code, b := doReq(t, s.ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("outside"), "application/xml"); code != http.StatusOK {
				t.Fatalf("draft: %d %s", code, b)
			}
			if code, res := publishApp(t, s.ts, app, ""); code != http.StatusOK || !res.Deployed {
				t.Fatalf("publish: %d %+v", code, res)
			}
			// A catalogue, so its releases are asked for.
			if code, b := doReq(t, s.ts, http.MethodPost, "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`, "application/json"); code != http.StatusCreated {
				t.Fatalf("catalogue: %d %s", code, b)
			}
			path := filepath.Join(dir, tc.store)
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			handlersWrite(t, dir, tc.store, "not a directory")

			code, body := doReq(t, s.ts, http.MethodGet, "/api/v1/applications/"+app+"/deletion-preview", "", "")
			handlersRefusal(t, "preview", code, body, http.StatusInternalServerError, tc.want)
		})
	}
}
