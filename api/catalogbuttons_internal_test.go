package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The catalogue screen wears the console's buttons.
//
// It was written with a button vocabulary of its own — `primary` for the eight
// actions that commit something, `linkish` for the five that remove a row, and no
// class at all on five more. **None of those three is a thing.** `app.css` declares
// neither `.primary` nor `.linkish`, so all eighteen rendered as the browser's
// default button: grey, square, a different size, on a page where every other
// screen draws the accent-filled `.btn`.
//
// Nobody reported it for the same reason nobody reported the missing hairlines: a
// plain button looks like a plain button. It only reads as wrong beside the
// Modeler, and the two are never on screen together.
//
// The guard is on the class and not on the appearance, because appearance is what
// a stylesheet decides and a class is what this file promises.

// buttonTag matches an opening <button> tag and captures its attributes.
var buttonTag = regexp.MustCompile(`<button([^>]*)>`)

// TestEveryCatalogueButtonIsAConsoleButton.
//
// `.btn` is the console's button. Every modifier beside it — ghost, neutral,
// danger — is declared next to it in app.css, so a button that carries `btn`
// cannot be unstyled, and one that does not is a button nothing styles.
func TestEveryCatalogueButtonIsAConsoleButton(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	tags := buttonTag.FindAllStringSubmatch(src, -1)
	if len(tags) < 15 {
		t.Fatalf("found %d buttons on the catalogue screen; this guard has lost its subject", len(tags))
	}
	for _, m := range tags {
		attrs := m[1]
		if !strings.Contains(attrs, `class="btn`) {
			t.Errorf("a button carries no console class and renders as the browser's own: <button%s>", attrs)
		}
	}
	// The two spellings this screen invented. Asserted by name rather than only
	// through the rule above, because that is what a reader of the diff is looking
	// for — and because either could come back on a button that also carries btn,
	// where the rule above would not notice.
	for _, gone := range []string{`class="primary"`, `class="linkish"`} {
		if strings.Contains(src, gone) {
			t.Errorf("%s is back; app.css declares no such class, so it styles nothing", gone)
		}
	}
}

// TestTheCatalogueUsesNoButtonClassTheStylesheetDoesNotDeclare.
//
// The rule one level up from the one above: a class on a button is either
// something app.css draws or something JavaScript selects on, and a third kind —
// a class that was meant to style and does not — is what this screen was full of.
//
// Selector hooks are allowed and are why this is not a blanket rule: a button may
// carry `btn ghost danger` for the eye and `data-act` for the handler. What it may
// not carry is a styling class nothing declares.
func TestTheCatalogueUsesNoButtonClassTheStylesheetDoesNotDeclare(t *testing.T) {
	css := stripComments(readWeb(t, "app.css"))
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`\.([a-zA-Z][a-zA-Z0-9_-]*)`).FindAllStringSubmatch(css, -1) {
		declared[m[1]] = true
	}

	src := readWeb(t, "catalog-admin.js")
	classAttr := regexp.MustCompile(`<button[^>]*\bclass="([^"$]*)"`)
	unknown := map[string]bool{}
	for _, m := range classAttr.FindAllStringSubmatch(src, -1) {
		for _, c := range strings.Fields(m[1]) {
			if !declared[c] {
				unknown[c] = true
			}
		}
	}
	if len(unknown) == 0 {
		return
	}
	names := make([]string, 0, len(unknown))
	for k := range unknown {
		names = append(names, k)
	}
	sort.Strings(names)
	t.Errorf("the catalogue screen styles buttons with %s, which app.css does not declare",
		strings.Join(names, ", "))
}
