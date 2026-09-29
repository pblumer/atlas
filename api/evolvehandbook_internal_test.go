package api

import (
	"regexp"
	"strings"
	"testing"
)

// The handbook's evolve chapter (api/web/handbuch.html, #weiterentwickeln) teaches the
// routes an operator drives to deploy a new version, migrate or fork a running case,
// pause a version, and deploy or delete a decision — with the role each one needs. That
// table is a copy of what this package declares, and a copy drifts: a route renamed
// here but not there is a curl command that 404s, and a role that quietly changes — the
// migration routes are admin, pausing is operator, deploy is modeler — turns the table
// into a lie about who may do what.
//
// So the chapter's route table is read back and every documented METHOD /path is held
// to a registered route with the boundary role the table claims.

const evolveHandbookPath = "web/handbuch.html"

var evolveRoutesTable = regexp.MustCompile(
	`<tr><td><code>([A-Z]+) (/api/v1/[^<]+)</code></td><td><code>([a-z]+)</code></td>`)

func TestTheHandbookDocumentsEvolveRoutesThatExist(t *testing.T) {
	table := evolveSection(t, `<table id="evolve-routes">`, "</table>")
	rows := evolveRoutesTable.FindAllStringSubmatch(table, -1)
	if len(rows) < 6 {
		t.Fatalf("found %d rows in the handbook's evolve-routes table; the row shape this test reads is gone", len(rows))
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

// evolveSection returns the slice of the handbook from open to the next close after it,
// failing when either is missing so a renamed anchor or table id is caught here.
func evolveSection(t *testing.T, open, close string) string {
	t.Helper()
	raw, err := webFS.ReadFile(evolveHandbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", evolveHandbookPath, err)
	}
	page := string(raw)
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("%s has no %s — the section this test guards is gone", evolveHandbookPath, open)
	}
	rest := page[i:]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("%s has %s but no closing %s after it", evolveHandbookPath, open, close)
	}
	return rest[:j]
}
