package api

import (
	"strings"
	"testing"
)

// TestNoRouteDeletesADecisionDeployment is the guard for
// ADR-0329.
//
// A deployed process pins its latest-bound decision references to a decision
// deployment's key at deploy time (ADR-0327), stores that key in its own record,
// and carries no copy of the model behind it. Deleting the record it points at
// would leave a business rule task whose job cannot evaluate — discovered at task
// activation, inside a running instance.
//
// Nothing deletes a decision deployment today, and that is what makes the pin
// safe. This test asserts that absence, so the rule reaches whoever removes it
// rather than waiting to be discovered.
//
// It is not a prohibition. The route may be built; the failure message says what
// has to be true when it is, and where the reasoning lives.
func TestNoRouteDeletesADecisionDeployment(t *testing.T) {
	s := specServer()
	for _, r := range s.apiRoutes() {
		if r.method != "DELETE" || !strings.HasPrefix(r.pattern, "/api/v1/decision-deployments") {
			continue
		}
		t.Fatalf(`%s %s deletes a decision deployment, which nothing did when this test was written.

A deployed process definition pins its latest-bound decision references to a
decision deployment's key and keeps no copy of that model, so removing the record
breaks the pin — at task activation, in a running instance.

Before this route ships it must refuse (409, naming the definitions) while any
deployed definition is pinned to the key: PinnedDecisionKey(decisionId) returns it,
or the persisted record's decisionBindings names it. Superseded versions count as
much as current ones, and a definition with no running instances is still pinned.

Read docs/adr/0329-a-decision-deployment-is-not-deletable.md, implement the
refusal with its own tests, then replace this test with them.`, r.method, r.pattern)
	}
}
