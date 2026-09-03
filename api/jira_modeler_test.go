package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/jira"
)

// jiraOptionRe matches one option of a select field in the Modeler's worker
// catalog, e.g. `{ v: "create-issue", l: "Create issue" }`.
var jiraOptionRe = regexp.MustCompile(`\{ v: "([a-z-]+)", l: "[^"]+" \}`)

// TestModelerOffersEveryJiraOperation is the drift guard for the third copy of the
// operation set.
//
// The rules live twice on purpose — connector/jira.Ops and the compiler's jiraOps,
// with TestJiraOpsMatchTheConnector between them — but the operations are named a
// third time, in the Modeler's own dropdown, which no test read. The two failures
// that gap allows are opposite and both silent: an operation the engine performs but
// the panel does not offer is unreachable to anyone modelling in the Console, and one
// the panel offers but the compiler does not know deploys and then fails at call time
// with "unknown operation", after the model is live.
func TestModelerOffersEveryJiraOperation(t *testing.T) {
	src, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	catalog := string(src)
	start := strings.Index(catalog, `id: "jira"`)
	if start < 0 {
		t.Fatal("the jira worker is missing from SERVICE_TASK_KINDS")
	}
	// The operation dropdown is the first select in the entry, so its options end at
	// the closing bracket of that field's option list.
	opStart := strings.Index(catalog[start:], `key: "operation"`)
	if opStart < 0 {
		t.Fatal("the jira catalog entry has no operation field")
	}
	block := catalog[start+opStart:]
	end := strings.Index(block, "],")
	if end < 0 {
		t.Fatal("could not isolate the jira operation options")
	}
	offered := map[string]bool{}
	for _, m := range jiraOptionRe.FindAllStringSubmatch(block[:end], -1) {
		offered[m[1]] = true
	}
	if len(offered) == 0 {
		t.Fatal("found no jira operation options; the pattern must have changed")
	}

	var unreachable, unknown []string
	for _, op := range jira.OpNames() {
		if !offered[op] {
			unreachable = append(unreachable, op)
		}
	}
	for op := range offered {
		if _, ok := jira.Ops[op]; !ok {
			unknown = append(unknown, op)
		}
	}
	sort.Strings(unknown)
	if len(unreachable) > 0 {
		t.Errorf("the Modeler offers no way to author %d Jira operation(s) the engine performs: %s\n\n"+
			"Add them to the operation dropdown in api/web/editor.js, with the fields each one takes.",
			len(unreachable), strings.Join(unreachable, ", "))
	}
	if len(unknown) > 0 {
		t.Errorf("the Modeler offers %d Jira operation(s) the worker does not implement: %s\n\n"+
			"Such a task deploys and then fails at call time with \"unknown operation\".",
			len(unknown), strings.Join(unknown, ", "))
	}
}
