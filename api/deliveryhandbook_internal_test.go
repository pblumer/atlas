package api

import (
	"regexp"
	"strings"
	"testing"
)

// The handbook's delivery chapter (api/web/handbuch.html, #ausliefern) teaches the
// routes an operator drives to publish, register a target, promote, and move a
// source tree — with the role each one needs — and it names the two token prefixes
// by which a deploy token and an API token are told apart. All of that is a copy of
// what this package defines, and a copy drifts: a route renamed here but not there
// is a curl command that 404s on its first run in somebody's pipeline, and a role
// that quietly changes turns "admin" in the book into a lie.
//
// So the chapter's own route table is read back and held to the real route set, and
// the prefixes it teaches are held to the constants they mirror.

const deliveryHandbookPath = "web/handbuch.html"

// deliveryRoutesTable is the chapter's <table id="delivery-routes">: one row per
// documented call, as METHOD /path in the first code cell and the boundary role in
// the second.
var deliveryRoutesTable = regexp.MustCompile(
	`<tr><td><code>([A-Z]+) (/api/v1/[^<]+)</code></td><td><code>([a-z]+)</code></td>`)

func TestTheHandbookDocumentsDeliveryRoutesThatExist(t *testing.T) {
	table := deliverySection(t, `<table id="delivery-routes">`, "</table>")
	rows := deliveryRoutesTable.FindAllStringSubmatch(table, -1)
	// A guard on the guard: if the table's row shape changes and this matches
	// nothing, the test must fail loudly rather than pass on an empty set.
	if len(rows) < 6 {
		t.Fatalf("found %d rows in the handbook's delivery-routes table; the row shape this test reads is gone", len(rows))
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

func TestTheHandbookNamesTheTokenPrefixes(t *testing.T) {
	section := deliverySection(t, `<section id="ausliefern">`, "</section>")
	for _, want := range []string{deployTokenPrefix, apiTokenPrefix} {
		if !strings.Contains(section, want) {
			t.Errorf("the delivery chapter does not name the token prefix %q it must teach to tell the two credentials apart", want)
		}
	}
}

// deliverySection returns the slice of the handbook from the first occurrence of
// open to the next close after it, failing when either is missing so a renamed
// anchor or table id is caught here rather than read past.
func deliverySection(t *testing.T, open, close string) string {
	t.Helper()
	raw, err := webFS.ReadFile(deliveryHandbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", deliveryHandbookPath, err)
	}
	page := string(raw)
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("%s has no %s — the section this test guards is gone", deliveryHandbookPath, open)
	}
	rest := page[i:]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("%s has %s but no closing %s after it", deliveryHandbookPath, open, close)
	}
	return rest[:j]
}
