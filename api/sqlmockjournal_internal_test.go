package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/sqldb"
)

const journalReport = `{"worker":"sql-1","seeded":2,"held":2,"statements":[
  {"seq":1,"statement":"SELECT id, mail FROM personen WHERE kuerzel = @p1","params":["abo"]},
  {"seq":2,"statement":"SELECT id FROM personen WHERE kuerzel = @p1","params":["zzz"],"failed":true,"detail":"sqldb: mock: nothing is seeded for the statement"}
]}`

// A worker reports, the Console reads. The round trip is the whole feature, so it is
// one test rather than two halves that could each pass while disagreeing.
func TestAReportedJournalComesBackToTheConsole(t *testing.T) {
	srv, _ := newValidateServer(t)

	code, resp := serveInternal(t, srv, http.MethodPost, "/api/v1/sql/mock-journal", journalReport, "application/json")
	if code != http.StatusNoContent {
		t.Fatalf("report: status=%d body=%s", code, resp)
	}

	code, resp = serveInternal(t, srv, http.MethodGet, "/api/v1/sql/mock-journal", "", "")
	if code != http.StatusOK {
		t.Fatalf("read: status=%d body=%s", code, resp)
	}
	var out struct {
		Workers []sqldb.MockJournalSnapshot `json:"workers"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(out.Workers) != 1 || out.Workers[0].Worker != "sql-1" {
		t.Fatalf("workers = %+v, want the one that reported", out.Workers)
	}
	if len(out.Workers[0].Statements) != 2 {
		t.Fatalf("statements = %+v, want both", out.Workers[0].Statements)
	}
	// The refusal is the entry that matters most: it is how an operator learns what to
	// seed, so it has to arrive marked and with its reason.
	second := out.Workers[0].Statements[1]
	if !second.Failed || !strings.Contains(second.Detail, "nothing is seeded") {
		t.Errorf("the refused statement arrived as %+v", second)
	}
	// And the bound values travel, which is the point of the view and the reason it is
	// admin-gated.
	if len(out.Workers[0].Statements[0].Params) != 1 {
		t.Errorf("the bound values did not survive the round trip: %+v", out.Workers[0].Statements[0])
	}
	// The arrival stamp is the view's, never the reporter's.
	if out.Workers[0].At == 0 {
		t.Error("the report was not stamped on arrival")
	}
}

// The stamp is the server's to assign. A reporter that could choose it could make its
// own report look fresher than another's, and freshness is what decides which worker a
// bounded view keeps.
func TestAReporterCannotChooseItsOwnFreshness(t *testing.T) {
	srv, _ := newValidateServer(t)
	body := `{"worker":"sql-1","at":9223372036854775807,"held":0,"statements":[]}`
	if code, resp := serveInternal(t, srv, http.MethodPost, "/api/v1/sql/mock-journal", body, "application/json"); code != http.StatusNoContent {
		t.Fatalf("report: status=%d body=%s", code, resp)
	}
	got := srv.sqlMockView.Snapshots()
	if len(got) != 1 {
		t.Fatalf("snapshots = %+v", got)
	}
	if got[0].At == 9223372036854775807 {
		t.Error("the reporter's own stamp was kept, so it could outrank every other worker forever")
	}
}

// A malformed report is a bad request, not a 500 — and nothing is filed from it.
func TestAMalformedJournalReportIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	code, resp := serveInternal(t, srv, http.MethodPost, "/api/v1/sql/mock-journal", "{not json", "application/json")
	if code != http.StatusBadRequest || !strings.Contains(string(resp), "invalid mock journal report") {
		t.Fatalf("status = %d (%s), want a 400 naming the body", code, resp)
	}
	if n := len(srv.sqlMockView.Snapshots()); n != 0 {
		t.Errorf("%d snapshots filed from a malformed report, want none", n)
	}
}

// Reading is admin-gated, and for a stronger reason than the mock directory's: the
// journal carries the values a process bound, and nothing here can tell a password on
// its way into a table from an id.
func TestOnlyAnAdminReadsTheJournal(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	srv.sqlMockView.Put(sqldb.MockJournalSnapshot{
		Worker: "sql-1", Held: 1,
		Statements: []sqldb.MockStatement{{Seq: 1, Statement: "SELECT 1", Params: []any{"geheim"}}},
	})

	get := func(roles ...string) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/sql/mock-journal", nil)
		r = r.WithContext(httpapi.WithPrincipal(r.Context(),
			&httpapi.Principal{UserID: "u1", Username: "ben", Roles: roles}))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec.Code
	}
	if code := get("operator"); code == http.StatusOK {
		t.Error("an operator read the journal, which carries whatever a process bound")
	}
	if code := get(RoleAdmin); code != http.StatusOK {
		t.Errorf("an admin was refused the journal: status=%d", code)
	}
}
