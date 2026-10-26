package api

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/wikiwikibangbang/catalog"
)

// wikiRowRe reads one row of WIKI_OPERATIONS in api/web/wikiwikibangbang-ops.js.
var wikiRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z-]+)", l: "[^"]+", area: "([a-z]+)", needs: \[([^\]]*)\], takes: \[([^\]]*)\], oneOf: (true|false), result: "([a-z]*)", max: \[(\d+), (\d+)\] \},$`)

// TestModelerKnowsEveryWikiOperation holds the Modeler's copy of the wikiwikibangbang
// operation table to connector/wikiwikibangbang/catalog, field by field.
//
// The panel needs its own copy — it runs in a browser — and the copy decides which
// fields an author sees. A row that differed would hide a value the deploy then asks
// for, or offer one it refuses; an operation missing from it is unreachable from the
// Modeler, and one it offers that the catalog lacks deploys nowhere.
func TestModelerKnowsEveryWikiOperation(t *testing.T) {
	src, err := os.ReadFile("web/wikiwikibangbang-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := wikiRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row of WIKI_OPERATIONS no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the wikiwikibangbang Worker does not implement", name)
			continue
		}
		result := map[catalog.Result]string{
			catalog.ResultNone: "", catalog.ResultItem: "object", catalog.ResultItems: "array",
			catalog.ResultText: "string", catalog.ResultComment: "object",
		}[op.Result]
		for _, c := range []struct {
			what      string
			js, gocat []string
		}{
			{"area", []string{m[2]}, []string{op.Area}},
			{"needed values", jsList(m[3]), op.Needs},
			{"optional values", jsList(m[4]), op.Takes},
			{"one of the optional values needed", []string{m[5]}, []string{strconv.FormatBool(op.NeedsOneTaken)}},
			{"result", []string{m[6]}, []string{result}},
			{"maxResults default and ceiling", []string{m[7], m[8]},
				[]string{strconv.Itoa(int(op.MaxResults.Default)), strconv.Itoa(int(op.MaxResults.Max))}},
		} {
			if strings.Join(c.js, ",") != strings.Join(c.gocat, ",") {
				t.Errorf("%s: the Modeler says %s are %v, the Worker %v", name, c.what, c.js, c.gocat)
			}
		}
	}
	for _, name := range catalog.Names() {
		if !seen[name] {
			t.Errorf("the Modeler offers no way to author %q, which the wikiwikibangbang Worker performs", name)
		}
	}

	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(editor), `from "./wikiwikibangbang-ops.js"`) || !strings.Contains(string(editor), "options: wikiOperationOptions()") {
		t.Error("the wikiwikibangbang entry in editor.js no longer takes its operations from wikiwikibangbang-ops.js")
	}
	// Every value an operation needs or takes has a field in the panel: the pairs
	// properties/propertiesVariable and text/blocks stand for the catalog's properties and
	// body.
	for _, attr := range catalog.Attributes {
		keys := map[string][]string{catalog.AttrProperties: {"properties", "propertiesVariable"}, catalog.AttrBody: {"text", "blocks"}}[attr]
		if keys == nil {
			keys = []string{attr}
		}
		for _, k := range keys {
			if !strings.Contains(string(editor), `key: "`+k+`", label:`) {
				t.Errorf("the Modeler has no field for %s", k)
			}
		}
	}
}

// TestTheWikiPackageOffersEveryOperation: the Repository package is the other place an
// author picks an operation, and it must not offer fewer than the Worker runs.
func TestTheWikiPackageOffersEveryOperation(t *testing.T) {
	raw, err := os.ReadFile("repository_catalog/wikiwikibangbang.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range catalog.Names() {
		if !strings.Contains(string(raw), `"value": "`+name+`"`) {
			t.Errorf("the Repository package offers no %q", name)
		}
	}
}
