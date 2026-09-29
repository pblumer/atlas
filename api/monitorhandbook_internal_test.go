package api

import (
	"regexp"
	"strings"
	"testing"
)

// The handbook's monitoring subsection (api/web/handbuch.html, #ueberwachen) teaches the
// routes an operator drives to read incidents and to back up and restore — with the role
// each needs. That table is a copy of what this package declares, and a copy drifts.
//
// So the subsection's route table is read back and every documented METHOD /path is held
// to a registered route with the boundary role the table claims. (/metrics is not in the
// /api/v1 table — it is mounted beside it and reached through the `metrics` scope — so it
// is documented in prose, not in this bound table.)

const monitorHandbookPath = "web/handbuch.html"

var monitorRoutesTable = regexp.MustCompile(
	`<tr><td><code>([A-Z]+) (/api/v1/[^<]+)</code></td><td><code>([a-z]+)</code></td>`)

func TestTheHandbookDocumentsMonitoringRoutesThatExist(t *testing.T) {
	table := monitorSection(t, `<table id="monitor-routes">`, "</table>")
	rows := monitorRoutesTable.FindAllStringSubmatch(table, -1)
	if len(rows) < 4 {
		t.Fatalf("found %d rows in the handbook's monitor-routes table; the row shape this test reads is gone", len(rows))
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

func monitorSection(t *testing.T, open, close string) string {
	t.Helper()
	raw, err := webFS.ReadFile(monitorHandbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", monitorHandbookPath, err)
	}
	page := string(raw)
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("%s has no %s — the section this test guards is gone", monitorHandbookPath, open)
	}
	rest := page[i:]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("%s has %s but no closing %s after it", monitorHandbookPath, open, close)
	}
	return rest[:j]
}
