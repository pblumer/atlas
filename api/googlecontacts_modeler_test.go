package api

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/googlecontacts/catalog"
)

// googleContactsRowRe reads one row of GOOGLE_CONTACTS_OPERATIONS in
// api/web/googlecontacts-ops.js.
var googleContactsRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z-]+)", l: "[^"]+", area: "([a-z]+)", needs: \[([^\]]*)\], takes: \[([^\]]*)\], result: "([a-z]*)", max: \[(\d+), (\d+)\] \},$`)

// TestModelerKnowsEveryGoogleContactsOperation holds the Modeler's copy of the Google
// Contacts operation table to connector/googlecontacts/catalog, field by field.
//
// The panel needs its own copy — it runs in a browser — and the copy decides which
// fields an author sees. A row that differed would hide a value the deploy then asks
// for, or offer one it refuses; an operation missing from it is unreachable from the
// Modeler, and one it offers that the catalog lacks deploys nowhere.
func TestModelerKnowsEveryGoogleContactsOperation(t *testing.T) {
	src, err := os.ReadFile("web/googlecontacts-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := googleContactsRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row of GOOGLE_CONTACTS_OPERATIONS no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the Google Contacts Worker does not implement", name)
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
			t.Errorf("the Modeler offers no way to author %q, which the Google Contacts Worker performs", name)
		}
	}

	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(editor), `from "./googlecontacts-ops.js"`) || !strings.Contains(string(editor), "options: googleContactsOperationOptions()") {
		t.Error("the Google Contacts entry in editor.js no longer takes its operations from googlecontacts-ops.js")
	}
}

// TestTheGoogleContactsPackageOffersEveryOperation: the Repository package is the other
// place an author picks an operation, and it must not offer fewer than the Worker runs.
func TestTheGoogleContactsPackageOffersEveryOperation(t *testing.T) {
	raw, err := os.ReadFile("repository_catalog/google-contacts.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range catalog.Names() {
		if !strings.Contains(string(raw), `"value": "`+name+`"`) {
			t.Errorf("the Repository package offers no %q", name)
		}
	}
}
