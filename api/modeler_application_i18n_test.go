package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryMessageTheApplicationPageAsksForExists holds the Modeler's application
// landscape and application page, and everything they open — the "Create new" menu,
// the artifact table, the deployed processes, the Deployments and Constants tabs,
// publishing, importing and exporting, sharing and deleting — to the catalogue
// (ADR-0267): every key these modules name is in both locales. A key is named three
// ways: looked up directly, looked up as a plural (which needs both forms), or held as
// a literal in a table or a ternary and looked up later, which has to be one or the
// other. A typo in any of them otherwise reaches the author as a raw id in place of a
// button, a confirm or a warning.
func TestEveryMessageTheApplicationPageAsksForExists(t *testing.T) {
	// A key ends in a letter or digit: a literal ending in a dot is the prefix of a
	// family of ids the server sends ("share.role."), resolved one id at a time.
	const key = `((?:create|application|artifact|dmnref|deployments|constants|import|share|appDelete|transfer|pick|table|home|choices|package|frozen)\.[A-Za-z0-9.]*[A-Za-z0-9])`
	lookups := regexp.MustCompile(`\b(?:t|tr)\("` + key + `"`)
	plurals := regexp.MustCompile(`\b(?:plural|trPlural)\("` + key + `"`)
	literals := regexp.MustCompile(`"` + key + `"`)

	direct, counted, named := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, file := range []string{
		"web/app.js", "web/deletedialog.js", "web/dmnref-impact.js",
		"web/sourceneeds.js", "web/pickmodal.js", "web/table.js",
		"web/importchoices.js", "web/packageimport.js", "web/frozen-decisions.js",
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
				t.Errorf("the application page asks for %q, which locale %q does not have", k, locale)
			}
		}
		for k := range counted {
			if !has(k+".one") || !has(k+".other") {
				t.Errorf("the application page counts with %q, which locale %q does not have in both forms", k, locale)
			}
		}
		for k := range named {
			if !has(k) && !(has(k+".one") && has(k+".other")) {
				t.Errorf("the application page names %q, which locale %q has neither as a message nor as a plural", k, locale)
			}
		}
	}
}
