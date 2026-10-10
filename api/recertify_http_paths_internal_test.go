package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
)

// The recertification routes' refusals and failure answers (ADR-0341).
//
// The HTTP tests in recertify_http_test.go drive the happy paths under
// authentication. These pin what the routes answer when something is wrong — a
// body that cannot be read, a campaign or a row that does not exist, a store that
// cannot be read or written, a server that is going away — and, for every write
// that was refused, that nothing was recorded. A campaign is evidence; a refusal
// that left half of one behind would be evidence of a review nobody held.

// recertifyHTTPPathsCall serves one request through the full handler and returns
// the status and body.
func recertifyHTTPPathsCall(t *testing.T, h http.Handler, method, path string, body io.Reader) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, body))
	return rec.Code, rec.Body.String()
}

// recertifyHTTPPathsBreakDir puts a regular file where a store expects its
// directory. Every read, listing and write through that store then fails on every
// platform (the sidecar store reports ENOTDIR itself where Windows would answer
// "not found"), which is a broken data directory without a fake.
func recertifyHTTPPathsBreakDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove %s: %v", dir, err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("plant file at %s: %v", dir, err)
	}
}

// recertifyHTTPPathsGrant records one right in the inventory the way the
// discrepancy adoption does — a command on the loop, then a drive — so a campaign
// has something to ask about without standing up a catalogue and a load.
func recertifyHTTPPathsGrant(t *testing.T, s *Server, v model.EntitlementValue) {
	t.Helper()
	s.do(func() { s.proc.GrantEntitlement(v) })
	if err := s.drive(); err != nil {
		t.Fatalf("grant %s/%s: %v", v.Principal, v.ItemID, err)
	}
}

// recertifyHTTPPathsOpen opens a campaign and returns its report.
func recertifyHTTPPathsOpen(t *testing.T, h http.Handler, body string) recertifyReport {
	t.Helper()
	code, raw := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification", strings.NewReader(body))
	if code != http.StatusCreated {
		t.Fatalf("open campaign: %d %s", code, raw)
	}
	var rep recertifyReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode campaign: %v (%s)", err, raw)
	}
	return rep
}

// recertifyHTTPPathsCampaigns lists the campaign headers.
func recertifyHTTPPathsCampaigns(t *testing.T, h http.Handler) []recertifyCampaign {
	t.Helper()
	code, raw := recertifyHTTPPathsCall(t, h, http.MethodGet, "/api/v1/recertification", nil)
	if code != http.StatusOK {
		t.Fatalf("list campaigns: %d %s", code, raw)
	}
	var out []recertifyCampaign
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode campaigns: %v (%s)", err, raw)
	}
	return out
}

// TestRecertifyHTTPRefusesABodyItCannotRead. Neither an unreadable body nor one
// that is not JSON opens anything: the name is what an auditor cites, and a
// campaign opened from a body nobody could read would have none.
func TestRecertifyHTTPRefusesABodyItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification", errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("unreadable body = %d %s, want 400 naming the read", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification", strings.NewReader(`{"name":`))
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid JSON body") {
		t.Errorf("truncated JSON = %d %s, want 400 naming the JSON", code, body)
	}
	if got := recertifyHTTPPathsCampaigns(t, h); len(got) != 0 {
		t.Errorf("a refused open left %d campaign(s) behind: %+v", len(got), got)
	}
}

// TestRecertifyHTTPAnEmptyInventorySaysSo. On a fresh installation the whole
// inventory is empty, and the campaign must say that rather than read as an
// estate somebody certified in full.
func TestRecertifyHTTPAnEmptyInventorySaysSo(t *testing.T) {
	srv := newServerForErrors(t)
	rep := recertifyHTTPPathsOpen(t, srv.Handler(), `{"name":"Day one"}`)

	if rep.Counts.Rows != 0 || len(rep.Rows) != 0 {
		t.Fatalf("an empty inventory produced rows: %+v", rep)
	}
	if !strings.Contains(rep.Reason, "inventory is empty") {
		t.Errorf("reason = %q; an empty whole-inventory campaign must say the inventory is "+
			"empty, not that the scope matched nothing", rep.Reason)
	}
}

// TestRecertifyHTTPAReviewerGroupMayBeNamedByItsID. A process that already holds
// the group's id must not have to look its name up first, and the rows must carry
// the id — the thing membership is checked against.
func TestRecertifyHTTPAReviewerGroupMayBeNamedByItsID(t *testing.T) {
	srv := newServerForErrors(t)
	var err error
	srv.do(func() { err = srv.groups.Save(group{ID: "grp_reviewers", Name: "Reviewers"}) })
	if err != nil {
		t.Fatalf("seed group: %v", err)
	}
	recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})

	rep := recertifyHTTPPathsOpen(t, srv.Handler(), `{"name":"By id","reviewerGroup":"grp_reviewers"}`)
	if len(rep.Rows) != 1 {
		t.Fatalf("%d row(s), want the one right: %+v", len(rep.Rows), rep.Rows)
	}
	if rep.Rows[0].ReviewerGroup != "grp_reviewers" {
		t.Errorf("reviewerGroup = %q, want the group id the caller named", rep.Rows[0].ReviewerGroup)
	}
}

// TestRecertifyHTTPACampaignOverItsCeilingIsRefusedWhole. Half a campaign is the
// failure this whole feature exists to avoid, so one row too many refuses all of
// it — and nothing is recorded.
func TestRecertifyHTTPACampaignOverItsCeilingIsRefusedWhole(t *testing.T) {
	budgets := limits.Default()
	budgets.RecertifyRows = 1
	srv := newServerWithOptions(t, WithLimits(budgets))
	for _, item := range []string{"vpn", "sap"} {
		recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{
			Principal: "usr_ada", ItemID: item, Since: 1_000, Origin: model.OriginLegacy})
	}
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification",
		strings.NewReader(`{"name":"Too big"}`))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the ceiling = %d %s, want 413", code, body)
	}
	for _, want := range []string{"would ask 2 questions", "ceiling is 1", "refused whole"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal does not say %q: %s", want, body)
		}
	}
	if got := recertifyHTTPPathsCampaigns(t, h); len(got) != 0 {
		t.Errorf("a refused campaign was recorded anyway: %+v", got)
	}
}

// TestRecertifyHTTPAnUnknownCampaignIsNotFound, for reading and for closing. A
// mistyped id must not read as an empty campaign, and closing one must not create
// a header for it.
func TestRecertifyHTTPAnUnknownCampaignIsNotFound(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodGet, "/api/v1/recertification/cmp_nope", nil)
	if code != http.StatusNotFound || !strings.Contains(body, "no campaign cmp_nope") {
		t.Errorf("read unknown = %d %s, want 404 naming it", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification/cmp_nope/close", nil)
	if code != http.StatusNotFound || !strings.Contains(body, "no campaign cmp_nope") {
		t.Errorf("close unknown = %d %s, want 404 naming it", code, body)
	}
	if got := recertifyHTTPPathsCampaigns(t, h); len(got) != 0 {
		t.Errorf("closing a campaign that does not exist created one: %+v", got)
	}
}

// TestRecertifyHTTPClosingTwiceIsAConflict. The first close is what the record
// says; a second would move the closing time and the closer to whoever pressed
// the button last.
func TestRecertifyHTTPClosingTwiceIsAConflict(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	rep := recertifyHTTPPathsOpen(t, h, `{"name":"Once"}`)

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification/"+rep.ID+"/close", nil)
	if code != http.StatusOK {
		t.Fatalf("first close = %d %s", code, body)
	}
	var first recertifyReport
	if err := json.Unmarshal([]byte(body), &first); err != nil {
		t.Fatalf("decode close: %v", err)
	}

	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification/"+rep.ID+"/close", nil)
	if code != http.StatusConflict || !strings.Contains(body, "already closed") {
		t.Fatalf("second close = %d %s, want 409 already closed", code, body)
	}
	list := recertifyHTTPPathsCampaigns(t, h)
	if len(list) != 1 || list[0].ClosedAt != first.ClosedAt {
		t.Errorf("the refused close changed the record: %+v, want closedAt %d", list, first.ClosedAt)
	}
}

// TestRecertifyHTTPAStoreThatCannotBeReadIsAFault. Each of the reads a campaign
// is built from, and each a campaign is read back by, answers 500 with what
// failed — never an empty list or a 404, which would say there is nothing to
// review.
func TestRecertifyHTTPAStoreThatCannotBeReadIsAFault(t *testing.T) {
	t.Run("the accounts, when opening", func(t *testing.T) {
		srv := newServerForErrors(t)
		recertifyHTTPPathsBreakDir(t, srv.users.Dir())
		code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/recertification",
			strings.NewReader(`{"name":"Q3"}`))
		if code != http.StatusInternalServerError || !strings.Contains(body, "recertify:") {
			t.Errorf("open = %d %s, want 500", code, body)
		}
	})

	t.Run("the open findings, when opening", func(t *testing.T) {
		srv := newServerForErrors(t)
		recertifyHTTPPathsBreakDir(t, srv.discrepancies.Dir())
		code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/recertification",
			strings.NewReader(`{"name":"Q3"}`))
		if code != http.StatusInternalServerError || !strings.Contains(body, "discrepancystore") {
			t.Errorf("open = %d %s, want 500 naming the findings store", code, body)
		}
		if got := recertifyHTTPPathsCampaigns(t, srv.Handler()); len(got) != 0 {
			t.Errorf("a campaign was recorded without its disputes: %+v", got)
		}
	})

	t.Run("the campaign list", func(t *testing.T) {
		srv := newServerForErrors(t)
		recertifyHTTPPathsBreakDir(t, srv.recertifications.campaigns.Dir())
		code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodGet, "/api/v1/recertification", nil)
		if code != http.StatusInternalServerError || !strings.Contains(body, "recertifications:") {
			t.Errorf("list = %d %s, want 500", code, body)
		}
	})

	t.Run("a campaign's rows, when reading and closing it", func(t *testing.T) {
		srv := newServerForErrors(t)
		h := srv.Handler()
		rep := recertifyHTTPPathsOpen(t, h, `{"name":"Q3"}`)
		recertifyHTTPPathsBreakDir(t, srv.recertifications.rows.Dir())

		code, body := recertifyHTTPPathsCall(t, h, http.MethodGet, "/api/v1/recertification/"+rep.ID, nil)
		if code != http.StatusInternalServerError || !strings.Contains(body, "recertifyrowstore") {
			t.Errorf("read = %d %s, want 500 naming the row store", code, body)
		}
		code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification/"+rep.ID+"/close", nil)
		if code != http.StatusInternalServerError || !strings.Contains(body, "recertifyrowstore") {
			t.Errorf("close = %d %s, want 500 naming the row store", code, body)
		}
		if list := recertifyHTTPPathsCampaigns(t, h); len(list) != 1 || list[0].ClosedAt != 0 {
			t.Errorf("a close that could not read the campaign closed it anyway: %+v", list)
		}
	})
}

// TestRecertifyHTTPAFailedWriteRecordsNoCampaign. The rows are written before the
// header, so when the rows cannot be written there is no header pointing at
// nothing — a campaign that looks answerable and has no questions is exactly what
// somebody would close as complete.
func TestRecertifyHTTPAFailedWriteRecordsNoCampaign(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
	recertifyHTTPPathsBreakDir(t, srv.recertifications.rows.Dir())

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification",
		strings.NewReader(`{"name":"Q3"}`))
	if code != http.StatusInternalServerError || !strings.Contains(body, "recertify:") {
		t.Fatalf("open with an unwritable row store = %d %s, want 500", code, body)
	}
	if got := recertifyHTTPPathsCampaigns(t, h); len(got) != 0 {
		t.Errorf("a header was written for a campaign whose rows were not: %+v", got)
	}
}

// TestRecertifyHTTPAServerShuttingDownSaysSo. A request that meets a stopping run
// loop is told so. An empty list, or "no campaign", would be an answer about the
// estate; this is an answer about the server.
func TestRecertifyHTTPAServerShuttingDownSaysSo(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	h := srv.Handler()
	closeSrv()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification",
		strings.NewReader(`{"name":"Q3"}`))
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting down") {
		t.Errorf("open = %d %s, want 503 shutting down", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodGet, "/api/v1/recertification", nil)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting down") {
		t.Errorf("list = %d %s, want 503 shutting down", code, body)
	}
	// Reading, closing or deciding on one campaign reports the shutdown too, and not
	// as "no campaign": the campaign may well exist.
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/recertification/cmp_any"},
		{http.MethodPost, "/api/v1/recertification/cmp_any/close"},
		{http.MethodPost, "/api/v1/recertification/cmp_any/rows/row_any/keep"},
	} {
		code, body = recertifyHTTPPathsCall(t, h, req.method, req.path, nil)
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting down") {
			t.Errorf("%s %s = %d %s, want 503 naming the shutdown", req.method, req.path, code, body)
		}
	}
}

// TestRecertifyHTTPAReportIsCutForReadingNotForCounting. The rows a report shows
// are bounded, the counts are not, and the cut is said: a reader must be able to
// tell a long campaign shown in part from a short one shown whole.
func TestRecertifyHTTPAReportIsCutForReadingNotForCounting(t *testing.T) {
	budgets := limits.Default()
	budgets.RecertifyReport = 2
	cmp := recertifyCampaign{ID: "cmp_x", Rows: []recertifyRow{
		{ID: "r1", Principal: "a", ItemID: "vpn"},
		{ID: "r2", Principal: "b", ItemID: "vpn"},
		{ID: "r3", Principal: "c", ItemID: "vpn", Decision: decisionKeep},
	}}

	rep := recertifyReportOf(cmp, budgets)
	if len(rep.Rows) != 2 || rep.Omitted != 1 {
		t.Errorf("rows = %d omitted = %d, want 2 shown and 1 said to be omitted", len(rep.Rows), rep.Omitted)
	}
	if rep.Counts.Rows != 3 || rep.Counts.Kept != 1 {
		t.Errorf("counts = %+v, want them over the whole campaign", rep.Counts)
	}

	// A campaign with no rows at all still reports an empty list rather than nil,
	// and says why it asks nothing.
	empty := recertifyReportOf(recertifyCampaign{ID: "cmp_y", Items: []string{"vpn"}}, budgets)
	if empty.Rows == nil || len(empty.Rows) != 0 || empty.Omitted != 0 {
		t.Errorf("empty report rows = %#v omitted = %d, want an empty list and nothing omitted",
			empty.Rows, empty.Omitted)
	}
	if !strings.Contains(empty.Reason, "Check the product ids") {
		t.Errorf("reason = %q, want the scoped campaign's explanation", empty.Reason)
	}
}

// TestRecertifyHTTPACloseThatCannotBeWrittenLeavesTheCampaignOpen. Closing is the
// moment a campaign stops taking answers; when that write fails the caller is told
// and the campaign is still open, rather than told it closed and found taking
// answers later.
func TestRecertifyHTTPACloseThatCannotBeWrittenLeavesTheCampaignOpen(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	rep := recertifyHTTPPathsOpen(t, h, `{"name":"Q3"}`)
	reconcileApplyPathsBlockSave(t, srv.recertifications.campaigns.FileFor(rep.ID))

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/recertification/"+rep.ID+"/close", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "close the campaign") {
		t.Fatalf("close = %d %s, want 500", code, body)
	}
	if list := recertifyHTTPPathsCampaigns(t, h); len(list) != 1 || !list[0].Open() {
		t.Errorf("campaigns = %+v, want it still open", list)
	}
}
