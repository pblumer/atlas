package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
)

// A mock worker reports the forest it holds to the Atlas whose Console shows it
// (ADR-0213). These tests are about the reporting
// only: that it says what the directory holds, that it does not repeat itself, and
// above all that it can fail without costing the job anything — the report is an
// observation of work that already succeeded.

// reportSink is an Atlas standing in for the one a mock worker reports to.
type reportSink struct {
	mu   sync.Mutex
	got  []ad.MockSnapshot
	auth []string
	code int
	srv  *httptest.Server
}

func newReportSink(t *testing.T) *reportSink {
	t.Helper()
	s := &reportSink{code: http.StatusNoContent}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snap ad.MockSnapshot
		if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.got = append(s.got, snap)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		code := s.code
		s.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *reportSink) snapshots() []ad.MockSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ad.MockSnapshot(nil), s.got...)
}

// wait blocks until n reports have arrived, or fails the test. The startup report is
// sent off the worker's own goroutine — a worker must start whether or not its server
// is answering — so a test asserting on it has to wait for it.
func (s *reportSink) wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.snapshots()) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("reports = %d after waiting, want %d", len(s.snapshots()), n)
}

func (s *reportSink) fail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = http.StatusInternalServerError
}

func (s *reportSink) recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = http.StatusNoContent
}

// mockEnv is the environment of a worker in mock mode reporting to sink.
func mockEnv(sink *reportSink, extra map[string]string) func(string) string {
	env := map[string]string{
		"ATLAS_AD_MOCK":          "1",
		"ATLAS_AD_MOCK_VIEW_URL": sink.srv.URL,
		"ATLAS_WORKER_ID":        "ad-worker-1",
		"ATLAS_TOKEN":            "s3cret",
	}
	for k, v := range extra {
		env[k] = v
	}
	return envMap(env)
}

// seedFile writes an LDIF seed and returns its path.
func seedFile(t *testing.T, ldif string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "forest.ldif")
	if err := os.WriteFile(path, []byte(ldif), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	return path
}

// createArno is the job a joiner runs: the one that makes somebody ask where the
// account went.
var createArno = map[string]any{
	"url": "ldaps://dc.example.com:636", "operation": "create-user",
	"dn":         "cn=Arno,ou=users,dc=example,dc=com",
	"attributes": map[string]any{"sAMAccountName": []any{"arno"}},
}

// The account a job created is in the next report, under the worker's own id and
// behind the worker's token. This is the whole feature in one test.
func TestADMockReportsTheForestAfterAJob(t *testing.T) {
	sink := newReportSink(t)
	built, err := BuiltinConnectors(mockEnv(sink, nil), "ad")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	sink.wait(t, 1) // the startup report, before the job's own
	if _, err := built.Handlers[compiler.AdJobType].Run(context.Background(), adJob(createArno)); err != nil {
		t.Fatalf("run the AD job: %v", err)
	}

	snaps := sink.snapshots()
	if len(snaps) == 0 {
		t.Fatal("nothing was reported; the Console would show no forest at all")
	}
	last := snaps[len(snaps)-1]
	if last.Worker != "ad-worker-1" {
		t.Errorf("worker = %q, want the id the Workers view shows", last.Worker)
	}
	if len(last.Forests) != 1 || len(last.Forests[0].Entries) != 1 ||
		!strings.EqualFold(last.Forests[0].Entries[0].DN, "cn=Arno,ou=users,dc=example,dc=com") {
		t.Fatalf("reported %+v, want the created account in one forest", last.Forests)
	}
	if got := sink.auth[len(sink.auth)-1]; got != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want the worker's own token", got)
	}
}

// Mock mode is reported before any job runs. Forests exist only once dialled, so a
// worker that has leased nothing holds no directory — and "mock mode is on, 1 starting
// entry, nothing dialled yet" is exactly what the operator who just switched it on
// needs to see instead of an empty page.
func TestADMockReportsItselfAtStartup(t *testing.T) {
	sink := newReportSink(t)
	seed := seedFile(t, "dn: cn=Arno,ou=users,dc=example,dc=com\ncn: Arno\n")
	if _, err := BuiltinConnectors(mockEnv(sink, map[string]string{"ATLAS_AD_MOCK_SEED": seed}), "ad"); err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	sink.wait(t, 1)
	snaps := sink.snapshots()
	if snaps[0].Seeded != 1 || len(snaps[0].Forests) != 0 {
		t.Errorf("reported %+v, want 1 seeded entry and no forest yet", snaps[0])
	}
}

// A report that would say nothing new is not sent. What counts as new is any
// operation the directory performed — a bind included, because the journal the view
// shows is part of the report — so this is not "only writes": it is "only when
// something happened", which is what keeps a second report of an untouched directory
// off the wire.
func TestADMockDoesNotRepeatAnUnchangedDirectory(t *testing.T) {
	sink := newReportSink(t)
	dir := ad.NewMockDirectory()
	r := newADMockReporter(mockEnv(sink, nil), dir)

	r.report(context.Background())
	r.report(context.Background())
	if got := len(sink.snapshots()); got != 1 {
		t.Fatalf("reports = %d for an untouched directory, want 1", got)
	}

	if _, err := RunADJob(context.Background(), adJob(createArno), dir, adSecretFromEnv(envMap(nil)), nil); err != nil {
		t.Fatalf("run the AD job: %v", err)
	}
	r.report(context.Background())
	if got := len(sink.snapshots()); got != 2 {
		t.Fatalf("reports = %d after a create, want the directory sent again", got)
	}
}

// A report that was refused is retried by the next operation rather than being counted
// as delivered — otherwise a Console that was briefly unreachable would stay wrong
// until the directory changed again.
func TestADMockResendsAfterAFailedReport(t *testing.T) {
	sink := newReportSink(t)
	sink.fail()
	dir := ad.NewMockDirectory()
	r := newADMockReporter(mockEnv(sink, nil), dir)
	r.report(context.Background())

	sink.recover()
	r.report(context.Background())
	if got := len(sink.snapshots()); got != 2 {
		t.Errorf("reports = %d, want the failed one retried", got)
	}
}

// A report that does not arrive costs the job nothing. The operation happened; the
// view is an observation of it, and a worker that failed jobs because a Console could
// not be reached would be worse than one with no view at all.
func TestADMockJobSucceedsWhenTheReportFails(t *testing.T) {
	sink := newReportSink(t)
	sink.fail()
	built, err := BuiltinConnectors(mockEnv(sink, nil), "ad")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, err := built.Handlers[compiler.AdJobType].Run(context.Background(), adJob(createArno)); err != nil {
		t.Fatalf("the job failed because the report did: %v", err)
	}
}

// Without an address there is no reporter at all — an external worker nobody pointed
// at an Atlas simply keeps its forest to itself, exactly as before.
func TestADMockWithoutAViewURLReportsNothing(t *testing.T) {
	dir := ad.NewMockDirectory()
	if r := newADMockReporter(envMap(map[string]string{"ATLAS_AD_MOCK": "1"}), dir); r != nil {
		t.Errorf("reporter = %+v with no %s set, want none", r, ADMockViewURLEnv)
	}
}

// Nor is there a reporter without a mock directory to report: a worker writing to a
// real directory has nothing to show, and the address alone must not conjure one.
func TestNoReporterWithoutAMockDirectory(t *testing.T) {
	sink := newReportSink(t)
	if r := newADMockReporter(mockEnv(sink, nil), nil); r != nil {
		t.Errorf("reporter = %+v for a worker with no mock directory, want none", r)
	}
}

// An address that is not a URL costs the job nothing either. It is a misconfiguration
// an operator can only have made by hand, and the place to learn about it is the
// worker's log, not a failed provisioning task.
func TestADMockReportSurvivesAnUnusableAddress(t *testing.T) {
	sink := newReportSink(t)
	dir := ad.NewMockDirectory()
	r := newADMockReporter(mockEnv(sink, map[string]string{"ATLAS_AD_MOCK_VIEW_URL": "://not-a-url"}), dir)
	r.report(context.Background()) // must not panic, and must not reach the sink
	if got := len(sink.snapshots()); got != 0 {
		t.Errorf("reports = %d, want none — the address is unusable", got)
	}
}

// A reporter that is not there is called exactly as one that is: the AD handler must
// not grow a branch per configuration.
func TestADMockReporterIsSafeWhenAbsent(t *testing.T) {
	var absent *adMockReporter
	absent.report(context.Background()) // must not panic
}
