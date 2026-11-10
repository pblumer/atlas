package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/calendar/catalog"
)

// calendarRowRe reads one row of CALENDAR_OPERATIONS in api/web/calendar-ops.js.
var calendarRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z-]+)", l: "[^"]+", needs: \[([^\]]*)\], takes: \[([^\]]*)\], result: "([a-z]*)" \},$`)

// TestModelerKnowsEveryCalendarOperation holds the Modeler's copy of the calendar
// operation table to connector/calendar/catalog, field by field: the keys an input
// object needs and may take, and whether the operation returns something. A row that
// differed would show a hint the deploy contradicts.
func TestModelerKnowsEveryCalendarOperation(t *testing.T) {
	src, err := os.ReadFile("web/calendar-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := calendarRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row of CALENDAR_OPERATIONS no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the calendar Worker does not implement", name)
			continue
		}
		var takes []string
		for _, k := range op.Inputs {
			if !containsStr(op.Required, k) {
				takes = append(takes, k)
			}
		}
		result := map[catalog.Result]string{catalog.ResultNone: "", catalog.ResultObject: "object"}[op.Result]
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
			t.Errorf("the Modeler offers no way to author %q, which the calendar Worker performs", name)
		}
	}
	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(editor), `from "./calendar-ops.js"`) || !strings.Contains(string(editor), "options: calendarOperationOptions()") {
		t.Error("the calendar entry in editor.js no longer takes its operations from calendar-ops.js")
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestTheCalendarPackageOffersEveryOperation: the Repository package is the other
// place an author picks an operation, and it must not offer fewer than the Worker runs.
func TestTheCalendarPackageOffersEveryOperation(t *testing.T) {
	raw, err := os.ReadFile("repository_catalog/calendar.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range catalog.Names() {
		if !strings.Contains(string(raw), `"value": "`+name+`"`) {
			t.Errorf("the Repository package offers no %q", name)
		}
	}
}

// TestSupervisedCalendarEnvUsesTheWorkersOwnNames holds the server's rendering of a
// supervised calendar worker's environment to the names the worker reads.
func TestSupervisedCalendarEnvUsesTheWorkersOwnNames(t *testing.T) {
	src, err := os.ReadFile("../worker/calendarconnector.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `calendarEnvPrefix = "`+calendarEnvPrefix+`"`) {
		t.Fatalf("the worker reads its calendars under another prefix than %q", calendarEnvPrefix)
	}
}
