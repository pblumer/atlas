package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryMessageTheBPMNEditorFrameAsksForExists holds the frame of the BPMN editor —
// its bar, tabs and menu, the token simulation, the variables panel, the problems bar,
// the deploy and documentation panels, the Playground tab, the chip that says which
// record is shown, the documentation PDF and the start form dialog — to the catalogue
// (ADR-0267): every key these modules name is in every locale. A key is named three
// ways: looked up directly, looked up as a plural (which needs both forms), or held as
// a literal in a table or a ternary and looked up later — the Playground's tables, the
// elements the engine cannot run yet, the process document's type names — which has
// to be one or the other. A typo in any of them otherwise reaches the author as a raw
// id in place of a button, a hint or a page of the PDF.
func TestEveryMessageTheBPMNEditorFrameAsksForExists(t *testing.T) {
	const key = `((?:bpmn|pg|shown|processDoc|sim|idcheck|startvars)\.[A-Za-z0-9.]*[A-Za-z0-9])`
	lookups := regexp.MustCompile(`\b(?:t|tr)\("` + key + `"`)
	plurals := regexp.MustCompile(`\b(?:plural|trPlural)\("` + key + `"`)
	literals := regexp.MustCompile(`"` + key + `"`)

	direct, counted, named := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, file := range []string{
		"web/editor.js", "web/playground.js", "web/shown-version.js", "web/process-doc.js",
		"web/token-simulation.js", "web/idcheck.js", "web/startvars.js",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range lookups.FindAllStringSubmatch(string(src), -1) {
			direct[m[1]] = true
		}
		for _, m := range plurals.FindAllStringSubmatch(string(src), -1) {
			counted[m[1]] = true
		}
		for _, m := range literals.FindAllStringSubmatch(string(src), -1) {
			named[m[1]] = true
		}
	}
	if len(direct)+len(counted) < 300 {
		t.Fatalf("found only %d message keys; the scan no longer matches how the modules look them up", len(direct)+len(counted))
	}

	for _, locale := range consoleLocales() {
		block := localeCatalogue(t, locale)
		has := func(k string) bool { return strings.Contains(block, `"`+k+`":`) }
		for k := range direct {
			if !has(k) {
				t.Errorf("the BPMN editor asks for %q, which locale %q does not have", k, locale)
			}
		}
		for k := range counted {
			if !has(k+".one") || !has(k+".other") {
				t.Errorf("the BPMN editor counts with %q, which locale %q does not have in both forms", k, locale)
			}
		}
		for k := range named {
			if !has(k) && !(has(k+".one") && has(k+".other")) {
				t.Errorf("the BPMN editor names %q, which locale %q has neither as a message nor as a plural", k, locale)
			}
		}
	}
}
