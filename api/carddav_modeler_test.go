package api

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/carddav/catalog"
)

// cardDAVRowRe reads one row of CARDDAV_OPERATIONS in api/web/carddav-ops.js.
var cardDAVRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z-]+)", l: "[^"]+", area: "([a-z]+)", needs: \[([^\]]*)\], takes: \[([^\]]*)\], result: "([a-z]*)", max: \[(\d+), (\d+)\] \},$`)

// TestModelerKnowsEveryCardDAVOperation holds the Modeler's copy of the CardDAV
// operation table to connector/carddav/catalog, field by field.
//
// The panel needs its own copy — it runs in a browser — and the copy decides which
// fields an author sees. A row that differed would hide a value the deploy then asks
// for, or offer one it refuses; an operation missing from it is unreachable from the
// Modeler, and one it offers that the catalog lacks deploys nowhere.
func TestModelerKnowsEveryCardDAVOperation(t *testing.T) {
	src, err := os.ReadFile("web/carddav-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := cardDAVRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row of CARDDAV_OPERATIONS no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the CardDAV Worker does not implement", name)
			continue
		}
		var needs, takes []string
		for _, v := range []struct {
			field         string
			needed, taken bool
		}{
			{"contactId", op.NeedsContactID, false},
			{"query", op.NeedsQuery, false},
			{"field", false, op.TakesField},
			{"group", op.NeedsGroup, op.TakesGroup && !op.NeedsGroup},
			{"contact", op.NeedsContact, false},
		} {
			if v.needed {
				needs = append(needs, v.field)
			}
			if v.taken {
				takes = append(takes, v.field)
			}
		}
		result := map[catalog.Result]string{catalog.ResultNone: "", catalog.ResultObject: "object", catalog.ResultList: "array"}[op.Result]
		for _, c := range []struct {
			what      string
			js, gocat []string
		}{
			{"area", []string{m[2]}, []string{op.Area}},
			{"needed values", jsList(m[3]), needs},
			{"optional values", jsList(m[4]), takes},
			{"result", []string{m[5]}, []string{result}},
			{"maxResults default and ceiling", []string{m[6], m[7]},
				[]string{strconv.Itoa(int(op.MaxResults.Default)), strconv.Itoa(int(op.MaxResults.Max))}},
		} {
			if strings.Join(c.js, ",") != strings.Join(c.gocat, ",") {
				t.Errorf("%s: the Modeler says %s are %v, the Worker %v", name, c.what, c.js, c.gocat)
			}
		}
	}
	for _, name := range catalog.Names() {
		if !seen[name] {
			t.Errorf("the Modeler offers no way to author %q, which the CardDAV Worker performs", name)
		}
	}

	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(editor), `from "./carddav-ops.js"`) || !strings.Contains(string(editor), "options: cardDAVOperationOptions()") {
		t.Error("the CardDAV entry in editor.js no longer takes its operations from carddav-ops.js")
	}
}

// TestTheCardDAVPackageOffersEveryOperation: the Repository package is the other
// place an author picks an operation, and it must not offer fewer than the Worker runs.
func TestTheCardDAVPackageOffersEveryOperation(t *testing.T) {
	raw, err := os.ReadFile("repository_catalog/carddav.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range catalog.Names() {
		if !strings.Contains(string(raw), `"value": "`+name+`"`) {
			t.Errorf("the Repository package offers no %q", name)
		}
	}
}
