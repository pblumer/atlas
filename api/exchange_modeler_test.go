package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// exchangeRowRe reads one row of EXCHANGE_OPERATIONS in api/web/exchange-ops.js.
var exchangeRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z-]+)", l: "[^"]+", area: "([a-z]+)", req: \[([^\]]*)\], opt: \[([^\]]*)\], sets: \[([^\]]*)\], result: "([a-z]*)", targets: \[([^\]]*)\]((?:, [a-zA-Z]+: true)*) \},$`)

func jsList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.Trim(strings.TrimSpace(p), `"`); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// TestModelerKnowsEveryExchangeOperation holds the Modeler's copy of the Exchange
// operation table to connector/exchange/catalog, field by field.
//
// The panel needs its own copy — it runs in a browser — and the copy is what tells an
// author which parameters an operation requires, takes and sets itself. A row that
// differed would say a parameter is optional that the deploy then refuses, or hide one
// the operation takes; an operation missing from it is unreachable from the Console,
// and one it offers that the catalog lacks deploys nowhere.
func TestModelerKnowsEveryExchangeOperation(t *testing.T) {
	src, err := os.ReadFile("web/exchange-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := exchangeRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row of EXCHANGE_OPERATIONS no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the Exchange Worker does not implement", name)
			continue
		}
		var fixed []string
		var targets []string
		for _, v := range []struct {
			target string
			v      *catalog.Variant
		}{{"online", op.Online}, {"onprem", op.OnPrem}} {
			if v.v == nil {
				continue
			}
			targets = append(targets, v.target)
			for k := range v.v.Fixed {
				fixed = append(fixed, k)
			}
		}
		sort.Strings(fixed)
		fixed = uniqueSorted(fixed)
		result := map[catalog.Result]string{catalog.ResultNone: "", catalog.ResultObject: "object", catalog.ResultList: "array"}[op.Result]
		flags := map[string]bool{}
		for _, f := range strings.Split(strings.TrimPrefix(m[8], ", "), ", ") {
			if f != "" {
				flags[strings.TrimSuffix(f, ": true")] = true
			}
		}
		for _, c := range []struct {
			what      string
			js, gocat []string
		}{
			{"area", []string{m[2]}, []string{op.Area}},
			{"required parameters", jsList(m[3]), op.Required},
			{"optional parameters", jsList(m[4]), op.Optional},
			{"parameters it sets itself", jsList(m[5]), fixed},
			{"result", []string{m[6]}, []string{result}},
			{"targets", jsList(m[7]), targets},
			{"open", []string{boolText(flags["open"])}, []string{boolText(op.Open)}},
			{"anyOf", []string{boolText(flags["anyOf"])}, []string{boolText(op.AnyOf)}},
		} {
			if strings.Join(c.js, ",") != strings.Join(c.gocat, ",") {
				t.Errorf("%s: the Modeler says %s are %v, the Worker %v", name, c.what, c.js, c.gocat)
			}
		}
	}
	for _, name := range catalog.Names() {
		if !seen[name] {
			t.Errorf("the Modeler offers no way to author %q, which the Exchange Worker performs", name)
		}
	}

	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(editor), `from "./exchange-ops.js"`) || !strings.Contains(string(editor), "options: exchangeOperationOptions()") {
		t.Error("the Exchange entry in editor.js no longer takes its operations from exchange-ops.js")
	}
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func uniqueSorted(xs []string) []string {
	var out []string
	for i, x := range xs {
		if i == 0 || xs[i-1] != x {
			out = append(out, x)
		}
	}
	return out
}
