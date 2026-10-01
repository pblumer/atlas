package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inventoryLoadHTTPPathsBody is the smallest well-formed reading: a named system and
// nothing observed. It is enough to reach every store the run reads.
const inventoryLoadHTTPPathsBody = `{"system":"sap","observations":[]}`

// inventoryLoadHTTPPathsPost posts one reading through the mux.
func inventoryLoadHTTPPathsPost(t *testing.T, srv *Server, body string) (int, string) {
	t.Helper()
	code, b := serveInternal(t, srv, http.MethodPost, "/api/v1/inventory-load", body, "application/json")
	return code, string(b)
}

// TestInventoryLoadHTTPRefusesWhatItCannotRead: a reading whose body broke off, or
// that is not JSON, is refused whole. Deciding over half a batch would report every
// right the missing half carried as unmodelled.
func TestInventoryLoadHTTPRefusesWhatItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/inventory-load", errReader{}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
		t.Errorf("broken body: %d (%s), want 400 'read body'", rec.Code, rec.Body)
	}
	if code, body := inventoryLoadHTTPPathsPost(t, srv, `{"system":`); code != http.StatusBadRequest ||
		!strings.Contains(body, "invalid JSON body") {
		t.Errorf("malformed body: %d (%s), want 400 'invalid JSON body'", code, body)
	}
	// Neither left a trace of a run.
	if code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/inventory-load?system=sap", "", ""); code != http.StatusOK ||
		!strings.Contains(string(body), `"runs":0`) {
		t.Errorf("state after refusals: %d (%s), want no runs", code, body)
	}
}

// TestInventoryLoadHTTPFailsWhenAStoreCannotBeRead: a load decides against the
// accounts, the catalogue and the system's own history. Any of them unreadable means
// the run cannot decide, and it must say so — a plan built against an empty account
// list would call every holder unknown.
func TestInventoryLoadHTTPFailsWhenAStoreCannotBeRead(t *testing.T) {
	for name, dirOf := range map[string]func(*Server) string{
		"accounts":  func(s *Server) string { return s.users.Dir() },
		"catalogue": func(s *Server) string { return filepath.Join(s.dataDir, "catalog", "items") },
		"history":   func(s *Server) string { return s.inventoryLoads.Dir() },
	} {
		t.Run(name, func(t *testing.T) {
			srv := newServerForErrors(t)
			approvalsPathsDirAsFile(t, dirOf(srv))
			code, body := inventoryLoadHTTPPathsPost(t, srv, inventoryLoadHTTPPathsBody)
			if code != http.StatusInternalServerError || !strings.Contains(body, "inventory load: ") {
				t.Errorf("unreadable %s: %d (%s), want 500", name, code, body)
			}
		})
	}
}

// TestInventoryLoadHTTPReportsARunItCouldNotRecord: the decision was made, but the
// record that it was made could not be written. Answering 200 would let a scheduled
// run believe its reading is on file, and the next one would be compared against a
// history that does not include it.
func TestInventoryLoadHTTPReportsARunItCouldNotRecord(t *testing.T) {
	srv := newServerForErrors(t)
	// Gone rather than replaced: a store whose folder is missing reads as empty but
	// cannot be written, which is exactly the write failing after a good read.
	if err := os.RemoveAll(srv.inventoryLoads.Dir()); err != nil {
		t.Fatal(err)
	}
	code, body := inventoryLoadHTTPPathsPost(t, srv, inventoryLoadHTTPPathsBody)
	if code != http.StatusInternalServerError || !strings.Contains(body, "record the run") {
		t.Errorf("unwritable history: %d (%s), want 500 'record the run'", code, body)
	}
}

// TestInventoryLoadHTTPStateFailsWhenTheHistoryCannotBeRead: "has this system ever
// been loaded" answered "no" from a store nobody can read would invite a first,
// unguarded load over one that already happened.
func TestInventoryLoadHTTPStateFailsWhenTheHistoryCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDirAsFile(t, srv.inventoryLoads.Dir())
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/inventory-load?system=sap", "", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "inventory load state") {
		t.Errorf("unreadable history: %d (%s), want 500 'inventory load state'", code, body)
	}
}

// TestInventoryLoadHTTPDuringShutdown: with the loop gone neither route ran, and a
// zero answer would read as "never loaded" or as a run that decided nothing.
func TestInventoryLoadHTTPDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()

	rec := httptest.NewRecorder()
	srv.handleInventoryLoadState(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inventory-load?system=sap", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "shutting down") {
		t.Errorf("state during shutdown: %d (%s), want 503", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	srv.handleInventoryLoad(rec, httptest.NewRequest(http.MethodPost, "/api/v1/inventory-load",
		strings.NewReader(inventoryLoadHTTPPathsBody)))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "shutting down") {
		t.Errorf("load during shutdown: %d (%s), want 503", rec.Code, rec.Body)
	}
}
