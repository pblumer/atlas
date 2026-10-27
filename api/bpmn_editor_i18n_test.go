package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryMessageTheBPMNEditorFrameAsksForExists holds the BPMN editor to the catalogue
// (ADR-0267): its frame — the bar, tabs and menu, the token simulation, the variables
// panel, the problems bar, the deploy and documentation panels, the Playground tab, the
// chip that says which record is shown, the documentation PDF and the start form dialog —
// and its properties panel with the modules the panel draws from: the FEEL fields and
// variable references, the collapsible groups, the operation tables of the mail, contact,
// directory and wiki Worker Types, the PowerShell editor, the Worker Type setup docs, the
// Developer View and the decision graph. Every key these modules name is in every
// locale. A key is named three ways: looked up directly, looked up as a plural (which
// needs both forms), or held as a literal in a table or a ternary and looked up later —
// the Playground's tables, the elements the engine cannot run yet, the process
// document's type names, the setup docs' lines — which has to be one or the other. A
// typo in any of them otherwise reaches the author as a raw id in place of a button, a
// hint or a page of the PDF.
func TestEveryMessageTheBPMNEditorFrameAsksForExists(t *testing.T) {
	const key = `((?:bpmn|pg|shown|processDoc|sim|idcheck|startvars|pnl|pgroup|feel|workerDoc|devView|drg)\.[A-Za-z0-9.]*[A-Za-z0-9])`
	lookups := regexp.MustCompile(`\b(?:t|tr)\("` + key + `"`)
	plurals := regexp.MustCompile(`\b(?:plural|trPlural)\("` + key + `"`)
	literals := regexp.MustCompile(`"` + key + `"`)

	direct, counted, named := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, file := range []string{
		"web/editor.js", "web/playground.js", "web/shown-version.js", "web/process-doc.js",
		"web/token-simulation.js", "web/idcheck.js", "web/startvars.js",
		"web/pgroup.js", "web/feel.js", "web/varrefs.js", "web/exchange-ops.js",
		"web/googlecontacts-ops.js", "web/carddav-ops.js", "web/wikiwikibangbang-ops.js", "web/entra-attrmask.js",
		"web/powershell.js", "web/workertypedocs.js", "web/dev-view.js", "web/decision-graph.js",
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
	if len(direct)+len(counted) < 1000 {
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

// TestEveryWorkerTypeTranslationNamesATextTheTablesHold holds the `wt.` messages to the
// Worker Type tables of the BPMN editor. Those tables keep their English, because a
// text there is also data — a field's group is its section's identity — and kindText
// translates it where it is drawn by looking up "wt." followed by the text. A message
// whose text no table holds any more is dead, and one whose English differs from its key
// would show the author something else in English than the table says; both mean the
// table changed under its translation, which then silently stops applying.
func TestEveryWorkerTypeTranslationNamesATextTheTablesHold(t *testing.T) {
	markup := regexp.MustCompile(`&[a-z]+;|&#[0-9]+;|</?[a-z][^>]*>`)
	src, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read the editor: %v", err)
	}
	editor := string(src)
	for _, locale := range consoleLocales() {
		msgs, _ := localeMessages(t, locale)
		n := 0
		for k := range msgs {
			text, ok := strings.CutPrefix(k, "wt.")
			if !ok {
				continue
			}
			n++
			if locale == "en" && msgs[k] != text {
				t.Errorf("%q reads %q in English; a Worker Type message keeps the table's text", k, msgs[k])
			}
			if !strings.Contains(editor, `"`+text+`"`) {
				t.Errorf("locale %q translates the Worker Type text %q, which no table in the editor holds", locale, text)
			}
			// The panel draws these texts escaped, so markup the table's own text does not
			// carry would reach the author as characters: z.&nbsp;B. instead of z. B.
			if got, want := markup.FindAllString(msgs[k], -1), markup.FindAllString(text, -1); strings.Join(got, "") != strings.Join(want, "") {
				t.Errorf("locale %q gives the Worker Type text %q markup it is drawn without: %q", locale, text, got)
			}
		}
		if n < 500 {
			t.Errorf("locale %q has only %d Worker Type messages; the scan no longer matches the catalogue", locale, n)
		}
	}
}
