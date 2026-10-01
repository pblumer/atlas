package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// directorySyncHTTPPathsBody is a one-account change set, applied from revision 0.
const directorySyncHTTPPathsBody = `{"apply":true,"fromRevision":0,
	"users":[{"id":"oid-ada","userPrincipalName":"ada@example.org","displayName":"Ada",
		"mail":"ada@example.org","accountEnabled":true}],
	"usersDeltaLink":"https://graph.microsoft.com/v1.0/users/delta?$deltatoken=U1"}`

// directorySyncHTTPPathsCall drives one of the two directory routes directly: the
// routes need authentication on, and the handler's own gate is all that is in play.
func directorySyncHTTPPathsCall(srv *Server, method string, body *strings.Reader) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	if method == http.MethodGet {
		srv.handleDirectorySyncState(rec, httptest.NewRequest(http.MethodGet, "/api/v1/directory-sync", nil))
		return rec
	}
	srv.handleDirectorySync(rec, httptest.NewRequest(http.MethodPost, "/api/v1/directory-sync", body))
	return rec
}

// TestDirectorySyncHTTPRefusesABodyThatCannotBeRead: a change set that broke off in
// transit is refused before anything is decided, and the cursor does not move —
// which is what makes the next run read the whole set again.
func TestDirectorySyncHTTPRefusesABodyThatCannotBeRead(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	rec := httptest.NewRecorder()
	srv.handleDirectorySync(rec, httptest.NewRequest(http.MethodPost, "/api/v1/directory-sync", errReader{}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
		t.Fatalf("broken body: %d (%s), want 400 'read body'", rec.Code, rec.Body)
	}
	if st := directorySyncHTTPPathsCall(srv, http.MethodGet, nil); st.Code != http.StatusOK ||
		!strings.Contains(st.Body.String(), `"revision":0`) {
		t.Errorf("state after a refused run: %d (%s), want revision 0", st.Code, st.Body)
	}
}

// TestDirectorySyncHTTPFailsWhenAStoreCannotBeRead: a mirror decides against the
// stored cursor, the accounts and the groups. Deciding against any of them read as
// empty would create a second account for everybody who already has one.
func TestDirectorySyncHTTPFailsWhenAStoreCannotBeRead(t *testing.T) {
	for name, dirOf := range map[string]func(*Server) string{
		"cursor":   func(s *Server) string { return s.directorySync.Dir() },
		"accounts": func(s *Server) string { return s.users.Dir() },
		"groups":   func(s *Server) string { return s.groups.Dir() },
	} {
		t.Run(name, func(t *testing.T) {
			srv := newServerWithOptions(t, WithAuth())
			approvalsPathsDirAsFile(t, dirOf(srv))
			rec := directorySyncHTTPPathsCall(srv, http.MethodPost, strings.NewReader(directorySyncHTTPPathsBody))
			if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "directory sync: ") {
				t.Errorf("unreadable %s: %d (%s), want 500", name, rec.Code, rec.Body)
			}
		})
	}
}

// TestDirectorySyncHTTPReportsAWriteThatStoppedBeforeTheCursor: the accounts were
// written but the cursor could not be. The run must fail loudly, so the scheduler
// sends the same change set again, rather than report a pass the next run would
// then skip.
func TestDirectorySyncHTTPReportsAWriteThatStoppedBeforeTheCursor(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	// Gone rather than replaced: it reads as "never run", and only the final write of
	// the cursor fails.
	if err := os.RemoveAll(srv.directorySync.Dir()); err != nil {
		t.Fatal(err)
	}
	rec := directorySyncHTTPPathsCall(srv, http.MethodPost, strings.NewReader(directorySyncHTTPPathsBody))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "directory sync: ") {
		t.Fatalf("unwritable cursor: %d (%s), want 500", rec.Code, rec.Body)
	}
	if _, err := os.Stat(srv.directorySync.Dir()); !os.IsNotExist(err) {
		t.Errorf("a cursor record appeared although its write failed: %v", err)
	}
}

// TestDirectorySyncHTTPStateFailsWhenTheCursorCannotBeRead: "where does the next run
// resume" answered with an empty cursor would schedule a full enumeration over a
// mirror that is already current.
func TestDirectorySyncHTTPStateFailsWhenTheCursorCannotBeRead(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	approvalsPathsDirAsFile(t, srv.directorySync.Dir())
	rec := directorySyncHTTPPathsCall(srv, http.MethodGet, nil)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "directory sync state") {
		t.Errorf("unreadable cursor: %d (%s), want 500", rec.Code, rec.Body)
	}
}

// TestDirectorySyncHTTPDuringShutdown: a closure that never ran leaves a zero report,
// which a scheduled run would record as a clean pass over a change set nobody read.
func TestDirectorySyncHTTPDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t, WithAuth())
	closeSrv()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := directorySyncHTTPPathsCall(srv, method, strings.NewReader(directorySyncHTTPPathsBody))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "shutting down") {
			t.Errorf("%s during shutdown: %d (%s), want 503", method, rec.Code, rec.Body)
		}
	}
}
