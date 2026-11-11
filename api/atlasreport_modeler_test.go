package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/atlasreport/catalog"
)

// atlasReportRowRe reads one row of ATLASREPORT_OPERATIONS in api/web/atlasreport-ops.js.
var atlasReportRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z-]+)", l: "[^"]+", needs: \[([^\]]*)\], takes: \[([^\]]*)\], result: "([a-z]*)" \},$`)

// TestModelerKnowsEveryAtlasReportOperation holds the Modeler's copy of the calendar
// operation table to connector/atlasreport/catalog, field by field: the keys an input
// object needs and may take, and whether the operation returns something. A row that
// differed would show a hint the deploy contradicts.
func TestModelerKnowsEveryAtlasReportOperation(t *testing.T) {
	src, err := os.ReadFile("web/atlasreport-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := atlasReportRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row of ATLASREPORT_OPERATIONS no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the Atlas report Worker does not implement", name)
			continue
		}
		var takes []string
		for _, k := range op.Inputs {
			if !containsStr(op.Required, k) {
				takes = append(takes, k)
			}
		}
		result := map[catalog.Result]string{catalog.ResultReport: "object"}[op.Result]
		for _, c := range []struct {
			what      string
			js, gocat []string
		}{
			{"needed keys", jsList(m[2]), op.Required},
			{"optional keys", jsList(m[3]), takes},
			{"result", []string{m[4]}, []string{result}},
		} {
			if strings.Join(c.js, ",") != strings.Join(c.gocat, ",") {
				t.Errorf("%s: the Modeler says %s are %v, the Worker %v", name, c.what, c.js, c.gocat)
			}
		}
	}
	for _, name := range catalog.Names() {
		if !seen[name] {
			t.Errorf("the Modeler offers no way to author %q, which the Atlas report Worker performs", name)
		}
	}
	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(editor), `from "./atlasreport-ops.js"`) || !strings.Contains(string(editor), "options: atlasReportOperationOptions()") {
		t.Error("the atlasreport entry in editor.js no longer takes its operations from atlasreport-ops.js")
	}
}

// TestSupervisedAtlasReportEnvUsesTheWorkersOwnNames holds the server's rendering of a
// supervised atlasreport worker's environment to the names the worker reads.
func TestSupervisedAtlasReportEnvUsesTheWorkersOwnNames(t *testing.T) {
	src, err := os.ReadFile("../worker/atlasreportconnector.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `atlasReportEnvPrefix = "`+atlasReportEnvPrefix+`"`) {
		t.Fatalf("the worker reads its Atlas servers under another prefix than %q", atlasReportEnvPrefix)
	}
}
