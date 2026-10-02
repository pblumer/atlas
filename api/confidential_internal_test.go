package api

import (
	"strings"
	"testing"
)

// TestEveryInstanceRouteIsVeiled holds the route table to the confidential-projects
// rule (confidential.go): a route whose {key} opens one instance — or one of its
// jobs, incidents, or the definition it runs — declares the check, or reaches only
// admins, whom no project hides anything from. A route added later without it is a
// failing build rather than a way round the mark.
//
// The one exception is named: the variables read, which answers with the task
// holder's fields where the role no longer reaches and so decides inside the
// handler (instanceAccessFor) rather than in front of it.
func TestEveryInstanceRouteIsVeiled(t *testing.T) {
	decidesInside := map[string]bool{
		"GET /api/v1/instances/{key}/variables": true,
	}
	opens := []string{
		"/api/v1/instances/{key}",
		"/api/v1/jobs/{key}/",
		"/api/v1/incidents/{key}/",
		"/api/v1/processes/{key}/cancel-instances",
		"/api/v1/processes/{key}/runtime",
		"/api/v1/collaborations/{key}/runtime",
	}
	checked := 0
	for _, r := range specServer().apiRoutes() {
		route := r.method + " " + r.pattern
		matches := false
		for _, p := range opens {
			if r.pattern == strings.TrimSuffix(p, "/") || strings.HasPrefix(r.pattern, p) {
				matches = true
			}
		}
		if !matches {
			continue
		}
		checked++
		switch {
		case r.op.role == RoleAdmin, decidesInside[route]:
		case r.op.veil == veilNone:
			t.Errorf("%s opens an instance and declares no veil: an operator of another team reaches a confidential project's instance through it", route)
		}
	}
	if checked < 15 {
		t.Fatalf("matched only %d routes: the patterns above no longer describe the table", checked)
	}
}

// The index is what a request on a server with no confidential project consults,
// and it has to follow the store whichever way a record changed.
func TestTheConfidentialIndexFollowsTheStore(t *testing.T) {
	c := newConfidentialIndex()
	if !c.none() {
		t.Fatal("a new index holds something")
	}
	c.observe("p1", project{ID: "p1", Confidential: true}, true)
	c.observe("p2", project{ID: "p2"}, true)
	if got := c.snapshot(); len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("snapshot = %+v, want p1 only", got)
	}
	c.observe("p1", project{ID: "p1"}, true) // unmarked
	if !c.none() {
		t.Fatal("an unmarked project is still in the index")
	}
	c.observe("p2", project{ID: "p2", Confidential: true}, true)
	c.observe("p2", project{}, false) // deleted
	if !c.none() {
		t.Fatal("a deleted project is still in the index")
	}
	var nilIndex *confidentialIndex
	if !nilIndex.none() || nilIndex.snapshot() != nil {
		t.Fatal("a server without an index must read as having no confidential project")
	}
}
