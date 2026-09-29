package api

import (
	"regexp"
	"strings"
	"testing"
)

// The handbook's "your own worker" subsection (api/web/handbuch.html, #eigener-worker)
// teaches the Job-API routes a custom worker drives — activate, complete, fail — and
// the admin route that mints its token, with the boundary role each one needs. That
// table is a copy of what this package declares, and a copy drifts: a renamed job route
// is a worker loop that 404s, and a role that changes silently misstates who may drive a
// job by hand.
//
// So the subsection's route table is read back and every documented METHOD /path is held
// to a registered route with the boundary role the table claims.

const workerJobsHandbookPath = "web/handbuch.html"

var workerJobsRoutesTable = regexp.MustCompile(
	`<tr><td><code>([A-Z]+) (/api/v1/[^<]+)</code></td><td><code>([a-z]+)</code></td>`)

func TestTheHandbookDocumentsWorkerJobRoutesThatExist(t *testing.T) {
	table := workerJobsSection(t, `<table id="worker-jobs-routes">`, "</table>")
	rows := workerJobsRoutesTable.FindAllStringSubmatch(table, -1)
	if len(rows) < 3 {
		t.Fatalf("found %d rows in the handbook's worker-jobs-routes table; the row shape this test reads is gone", len(rows))
	}

	role := map[string]string{}
	for _, r := range accessTestServer(t).apiRoutes() {
		role[r.method+" "+r.pattern] = r.op.role
	}
	for _, m := range rows {
		route := m[1] + " " + m[2]
		got, ok := role[route]
		if !ok {
			t.Errorf("the handbook documents %q, which is not a registered route", route)
			continue
		}
		if got != m[3] {
			t.Errorf("the handbook says %q needs role %q; the route declares %q", route, m[3], got)
		}
	}
}

// workerJobsSection returns the slice of the handbook from open to the next close after
// it, failing when either is missing so a renamed anchor or table id is caught here.
func workerJobsSection(t *testing.T, open, close string) string {
	t.Helper()
	raw, err := webFS.ReadFile(workerJobsHandbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", workerJobsHandbookPath, err)
	}
	page := string(raw)
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("%s has no %s — the section this test guards is gone", workerJobsHandbookPath, open)
	}
	rest := page[i:]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("%s has %s but no closing %s after it", workerJobsHandbookPath, open, close)
	}
	return rest[:j]
}
