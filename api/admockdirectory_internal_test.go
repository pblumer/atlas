package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/worker"
)

// What an Atlas keeps of the mock directories its workers hold, and shows in
// Operations › Mock directory (ADR-draft-ad-mock-directory-in-the-console).
//
// The seed card in the Console answers "what does a forest start from"; nothing
// answered "what is in it now", and the seed was routinely mistaken for it — reasonably,
// since it is the only directory-shaped thing an operator could see. These tests cover
// the receiving end: a worker's report arrives, the newest one wins, and what comes
// back is what the view renders.

// reportDirectory posts one snapshot the way a mock worker does.
func reportDirectory(t *testing.T, srv *Server, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ad/mock-directory", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code
}

// readDirectories reads the view back.
func readDirectories(t *testing.T, srv *Server) []ad.MockSnapshot {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ad/mock-directory", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET mock-directory: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Workers []ad.MockSnapshot `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return got.Workers
}

// The account a mock worker created is what the view serves back — the whole point,
// end to end through the API.
func TestAReportedMockForestIsServedBack(t *testing.T) {
	srv, _ := newValidateServer(t)
	if code := reportDirectory(t, srv, `{"worker":"ad-1","seeded":2,"forests":[
		{"url":"ldaps://dc.example.com:636","held":1,"entries":[
			{"dn":"cn=Arno,ou=users,dc=example,dc=com","attributes":{"sAMAccountName":["arno"]}}]}],
		"operations":[{"seq":2,"op":"add","dn":"cn=Arno,ou=users,dc=example,dc=com","detail":"sAMAccountName"}]}`); code != http.StatusNoContent {
		t.Fatalf("report: status=%d, want 204", code)
	}

	got := readDirectories(t, srv)
	if len(got) != 1 || got[0].Worker != "ad-1" || got[0].Seeded != 2 {
		t.Fatalf("view = %+v, want one report from ad-1", got)
	}
	if len(got[0].Forests) != 1 || len(got[0].Forests[0].Entries) != 1 ||
		got[0].Forests[0].Entries[0].DN != "cn=Arno,ou=users,dc=example,dc=com" {
		t.Fatalf("forests = %+v, want the created account", got[0].Forests)
	}
	if got[0].At == 0 {
		t.Error("the report carries no arrival time; the view cannot say how fresh it is")
	}
	if len(got[0].Operations) != 1 || got[0].Operations[0].Op != "add" {
		t.Errorf("operations = %+v, want the add the worker journalled", got[0].Operations)
	}
}

// A worker reports its whole directory every time, so the newest report replaces the
// last — an entry a leaver deleted has to leave the view with it.
func TestANewerReportReplacesTheWorkersLastOne(t *testing.T) {
	srv, _ := newValidateServer(t)
	reportDirectory(t, srv, `{"worker":"ad-1","forests":[{"url":"ldaps://dc","held":1,"entries":[{"dn":"cn=Arno"}]}]}`)
	reportDirectory(t, srv, `{"worker":"ad-1","forests":[{"url":"ldaps://dc","held":0,"entries":[]}]}`)

	got := readDirectories(t, srv)
	if len(got) != 1 {
		t.Fatalf("view = %+v, want one report per worker", got)
	}
	if len(got[0].Forests) != 1 || len(got[0].Forests[0].Entries) != 0 {
		t.Errorf("forest = %+v, want the deleted account gone", got[0].Forests)
	}
}

// A body that is not a snapshot is refused where it was sent, rather than filed as an
// empty directory that would read as "the mockup lost everything".
func TestAMalformedMockReportIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	if code := reportDirectory(t, srv, `not json`); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	if got := readDirectories(t, srv); len(got) != 0 {
		t.Errorf("view = %+v, want nothing filed", got)
	}
}

// Nothing reported is an empty view and not an error: a server with no mock worker is
// the ordinary case, and the Console says so in words.
func TestAnUnreportedMockDirectoryIsAnEmptyView(t *testing.T) {
	srv, _ := newValidateServer(t)
	if got := readDirectories(t, srv); len(got) != 0 {
		t.Errorf("view = %+v, want it empty", got)
	}
}

// A server that keeps no view says so to the worker rather than accepting a report it
// will drop, and answers the read with an empty view rather than an error: one is a
// misconfiguration the reporter should log, the other is a page that has nothing to
// show, which is not the same thing.
func TestAReportIsRefusedWhenTheServerKeepsNoView(t *testing.T) {
	s := &Server{}
	post := httptest.NewRecorder()
	s.handleReportADMockDirectory(post,
		httptest.NewRequest(http.MethodPost, "/api/v1/ad/mock-directory", strings.NewReader(`{"worker":"ad-1"}`)))
	if post.Code != http.StatusServiceUnavailable {
		t.Errorf("report: status = %d, want 503", post.Code)
	}

	get := httptest.NewRecorder()
	s.handleADMockDirectory(get, httptest.NewRequest(http.MethodGet, "/api/v1/ad/mock-directory", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"workers":[]`) {
		t.Errorf("read: status = %d body = %s, want 200 and an empty view", get.Code, get.Body.String())
	}
}

// The worker and the engine name the reporting endpoint the same thing. They are two
// processes agreeing on a variable, so the agreement is asserted rather than assumed —
// the mail outbox's rule, and the same failure if it is broken: a mock worker that
// reports into the void and a Console that stays empty with nothing to say why.
func TestSupervisedADEnvUsesTheWorkersOwnReportingNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://atlas.example", nil, nil))
	var saveErr error
	srv.do(func() { saveErr = srv.settings.saveADMock(adMockSetting{Enabled: true}) })
	if saveErr != nil {
		t.Fatalf("saveADMock: %v", saveErr)
	}

	env := envOf(t, srv.adWorkerEnv())
	if got := env[adMockViewURLEnv]; got != "http://atlas.example/api/v1/ad/mock-directory" {
		t.Errorf("%s = %q, want this server's own reporting endpoint", adMockViewURLEnv, got)
	}
	// The variable the engine writes is the one the worker reads. Both sides declare
	// it, so that they agree is asserted here rather than assumed — a drift would
	// leave a mock worker reporting into the void and a Console empty with nothing to
	// say why.
	if adMockViewURLEnv != worker.ADMockViewURLEnv {
		t.Errorf("the engine renders %q and the worker reads %q", adMockViewURLEnv, worker.ADMockViewURLEnv)
	}
}

// Mock mode off means no reporting address: a worker writing to a real directory has
// no forest to show, and pointing it at the endpoint anyway would invite exactly the
// confusion of a "mock directory" view fed by a live one.
func TestNoReportingURLWhenTheMockupIsOff(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://atlas.example", nil, nil))
	var saveErr error
	srv.do(func() { saveErr = srv.settings.saveADMock(adMockSetting{Enabled: false}) })
	if saveErr != nil {
		t.Fatalf("saveADMock: %v", saveErr)
	}
	if got, ok := envOf(t, srv.adWorkerEnv())[adMockViewURLEnv]; ok {
		t.Errorf("%s = %q with the mockup off, want it unset", adMockViewURLEnv, got)
	}
}
