package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
)

var sharePointServerRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z-]+)", l: "[^"]+", area: "([a-z]+)", req: \[([^\]]*)\], opt: \[([^\]]*)\], result: "([a-z]*)"(, anyOf: true)? \},$`)

// TestModelerKnowsEverySharePointServerOperation holds the Modeler's copy of the
// operation table to connector/sharepointserver/catalog, field by field: the panel
// tells an author what a deploy will refuse, and a row that differed would mislead them.
func TestModelerKnowsEverySharePointServerOperation(t *testing.T) {
	src, err := os.ReadFile("web/sharepointserver-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := sharePointServerRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the Worker does not implement", name)
			continue
		}
		result := map[catalog.Result]string{catalog.ResultNone: "", catalog.ResultObject: "object", catalog.ResultList: "array"}[op.Result]
		if m[2] != op.Area || strings.Join(jsList(m[3]), ",") != strings.Join(op.Required, ",") ||
			strings.Join(jsList(m[4]), ",") != strings.Join(op.Optional, ",") || m[5] != result || (m[6] != "") != op.AnyOf {
			t.Errorf("the Modeler's row for %q differs from the catalog: %s", name, strings.TrimSpace(line))
		}
	}
	for _, name := range catalog.Names() {
		if !seen[name] {
			t.Errorf("the Worker implements %q, which the Modeler does not offer", name)
		}
	}
}
