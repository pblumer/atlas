package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/sqldb"
)

// journalSink is an Atlas that accepts journal reports, recording what arrived.
type journalSink struct {
	mu     sync.Mutex
	got    []sqldb.MockJournalSnapshot
	auth   []string
	status int
}

func (s *journalSink) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snap sqldb.MockJournalSnapshot
		_ = json.NewDecoder(r.Body).Decode(&snap)
		s.mu.Lock()
		s.got = append(s.got, snap)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		code := s.status
		s.mu.Unlock()
		if code == 0 {
			code = http.StatusNoContent
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *journalSink) reports() []sqldb.MockJournalSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sqldb.MockJournalSnapshot(nil), s.got...)
}

// The report carries what the process asked, under this worker's name and its own
// token — the same API, the same credential, no private channel.
func TestTheReporterSendsTheJournalUnderItsOwnName(t *testing.T) {
	sink := &journalSink{}
	srv := sink.server(t)

	db := sqldb.NewMockDatabase(mustProduct(t, "mssql"), sqldb.MockAnswer{
		Statement: "SELECT mail FROM personen WHERE id = @p1", Columns: []string{"mail"}, Rows: [][]any{{"arno@example.com"}},
	})
	client := sqldb.OpenMock(db)
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Query(context.Background(), "SELECT mail FROM personen WHERE id = @p1", []any{int64(42)}, 0); err != nil {
		t.Fatalf("Query: %v", err)
	}

	r := newSQLMockReporter(envMap(map[string]string{
		SQLMockViewURLEnv: srv.URL, "ATLAS_TOKEN": "t0ken", WorkerIDEnv: "sql-1",
	}), db)
	if r == nil {
		t.Fatal("no reporter was built for a worker that was given an address")
	}
	r.report(context.Background())

	got := sink.reports()
	if len(got) != 1 {
		t.Fatalf("%d reports arrived, want 1", len(got))
	}
	if got[0].Worker != "sql-1" {
		t.Errorf("Worker = %q, want the id the Workers view shows", got[0].Worker)
	}
	if len(got[0].Statements) != 1 || got[0].Statements[0].Statement != "SELECT mail FROM personen WHERE id = @p1" {
		t.Errorf("Statements = %+v, want the statement that ran", got[0].Statements)
	}
	if len(got[0].Statements[0].Params) != 1 {
		t.Errorf("the bound values did not travel: %+v", got[0].Statements[0])
	}
	if sink.auth[0] != "Bearer t0ken" {
		t.Errorf("Authorization = %q, want this worker's own token", sink.auth[0])
	}
}

// A worker that answered nothing new says nothing. Otherwise a worker leasing jobs of
// other kinds would post the same journal after every one of them.
func TestTheReporterSaysNothingTwice(t *testing.T) {
	sink := &journalSink{}
	srv := sink.server(t)
	db := sqldb.NewMockDatabase(mustProduct(t, "mssql"), sqldb.MockAnswer{Statement: "SELECT 1", Columns: []string{"n"}, Rows: [][]any{{1}}})
	client := sqldb.OpenMock(db)
	t.Cleanup(func() { _ = client.Close() })

	r := newSQLMockReporter(envMap(map[string]string{SQLMockViewURLEnv: srv.URL, WorkerIDEnv: "sql-1"}), db)
	r.report(context.Background()) // the empty journal: "nothing asked yet"
	r.report(context.Background()) // nothing changed
	if n := len(sink.reports()); n != 1 {
		t.Fatalf("%d reports for an unchanged journal, want 1", n)
	}

	if _, err := client.Query(context.Background(), "SELECT 1", nil, 0); err != nil {
		t.Fatalf("Query: %v", err)
	}
	r.report(context.Background())
	if n := len(sink.reports()); n != 2 {
		t.Errorf("%d reports after a statement ran, want a second", n)
	}
}

// A report is an observation, never part of the work: a server that refuses it costs a
// warning and nothing else. The statement has already been answered.
func TestAFailedReportIsNotAFailedJob(t *testing.T) {
	sink := &journalSink{status: http.StatusInternalServerError}
	srv := sink.server(t)
	db := sqldb.NewMockDatabase(mustProduct(t, "mssql"))
	r := newSQLMockReporter(envMap(map[string]string{SQLMockViewURLEnv: srv.URL, WorkerIDEnv: "sql-1"}), db)

	// report never returns an error at all — there is no channel by which it could fail
	// a job, which is the property under test.
	r.report(context.Background())

	// And having failed, it has not recorded the version as sent: the next statement
	// re-sends the whole journal, so a Console that was unreachable catches up by
	// itself rather than staying wrong.
	sink.mu.Lock()
	sink.status = http.StatusNoContent
	sink.mu.Unlock()
	r.report(context.Background())
	if n := len(sink.reports()); n != 2 {
		t.Errorf("%d attempts, want the failed one retried on the next report", n)
	}
}

// A worker given no address keeps its journal to itself, and every call site treats
// that the same way — the handler calls report() whether or not there is a view.
func TestNoAddressMeansNoReporterAndNoPanic(t *testing.T) {
	if r := newSQLMockReporter(envMap(map[string]string{WorkerIDEnv: "sql-1"}), sqldb.NewMockDatabase(mustProduct(t, "mssql"))); r != nil {
		t.Fatal("a reporter was built for a worker with nowhere to report to")
	}
	var nilReporter *sqlMockReporter
	nilReporter.report(context.Background())          // must not panic
	nilReporter.reportAtStartup(context.Background()) // nor this
}

// The startup report retries while the server is still coming up: a supervised worker
// is spawned by the very Atlas it reports to, so the first attempt is refused as a
// matter of course.
func TestTheStartupReportWaitsForTheServer(t *testing.T) {
	sink := &journalSink{status: http.StatusServiceUnavailable}
	srv := sink.server(t)
	db := sqldb.NewMockDatabase(mustProduct(t, "mssql"))

	r := newSQLMockReporter(envMap(map[string]string{SQLMockViewURLEnv: srv.URL, WorkerIDEnv: "sql-1"}), db)
	r.backoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

	// Answer the third attempt, so the test asserts the retry rather than a sleep.
	go func() {
		for {
			sink.mu.Lock()
			n := len(sink.got)
			if n >= 2 {
				sink.status = http.StatusNoContent
				sink.mu.Unlock()
				return
			}
			sink.mu.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()
	r.reportAtStartup(context.Background())

	if n := len(sink.reports()); n < 2 {
		t.Errorf("%d attempts, want the startup report to have retried", n)
	}
}

// mustProduct is the sqldb product a test drives the mock as.
func mustProduct(t *testing.T, name string) sqldb.Product {
	t.Helper()
	p, ok := sqldb.ProductByName(name)
	if !ok {
		t.Fatalf("no product %q", name)
	}
	return p
}

// A journal report carries values a process bound, which is the whole point and also
// the reason the read is admin-only. This pins that the transport does not quietly drop
// them — a view that showed statements without their values would answer the second
// question an operator has and not the first.
func TestBoundValuesSurviveTheReport(t *testing.T) {
	sink := &journalSink{}
	srv := sink.server(t)
	db := sqldb.NewMockDatabase(mustProduct(t, "mssql"), sqldb.MockAnswer{
		Statement: "UPDATE personen SET aktiv = @aktiv WHERE id = @id", Named: map[string]any{"id": 7, "aktiv": true}, Affected: 1,
	})
	client := sqldb.OpenMock(db)
	t.Cleanup(func() { _ = client.Close() })

	reg := sqldb.NewRegistry()
	reg.Register("hr", client)
	if _, err := sqldb.Run(context.Background(), sqldb.Job{
		Connector: "hr", Product: "mssql", Operation: "execute",
		Statement: "UPDATE personen SET aktiv = @aktiv WHERE id = @id",
		Named:     map[string]any{"id": int64(7), "aktiv": true}, ResultVariable: "n",
	}, reg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	r := newSQLMockReporter(envMap(map[string]string{SQLMockViewURLEnv: srv.URL, WorkerIDEnv: "sql-1"}), db)
	r.report(context.Background())

	got := sink.reports()
	if len(got) != 1 || len(got[0].Statements) != 1 {
		t.Fatalf("reports = %+v", got)
	}
	named := got[0].Statements[0].Named
	if len(named) != 2 {
		t.Fatalf("Named = %#v, want both bound values", named)
	}
	if !strings.Contains(strings.ToLower(got[0].Statements[0].Statement), "update personen") {
		t.Errorf("Statement = %q", got[0].Statements[0].Statement)
	}
}
