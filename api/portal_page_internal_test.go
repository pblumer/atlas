package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The portal offers its interface in the visitor's own language, and it may only
// do that on one condition: every string it renders exists in every locale it
// offers (ADR-draft-portal-language-follows-the-browser).
//
// ADR-0267 refuses to consult the browser precisely because guessing can land
// somebody on a half-translated screen. That is a statement about the state of a
// catalogue, not about browsers — so the condition is what makes the exception
// sound, and a test is what makes the condition a property rather than a promise.
// Without it this record is an intention.

var (
	portalLocaleBlock = regexp.MustCompile(`(?s)\n  (\w+): \{(.*?)\n  \},`)
	portalStringKey   = regexp.MustCompile(`'([^']+)':`)
)

func portalCatalogue(t *testing.T) map[string][]string {
	t.Helper()
	src := readWeb(t, "portal.js")

	start := strings.Index(src, "const STRINGS = {")
	if start < 0 {
		t.Fatal("portal.js declares no STRINGS catalogue — if it moved, this test now " +
			"passes vacuously and must be pointed at the new place")
	}
	end := strings.Index(src[start:], "\n};")
	if end < 0 {
		t.Fatal("the STRINGS catalogue is not closed where this test expects")
	}

	out := map[string][]string{}
	for _, m := range portalLocaleBlock.FindAllStringSubmatch(src[start:start+end+3], -1) {
		var keys []string
		for _, k := range portalStringKey.FindAllStringSubmatch(m[2], -1) {
			keys = append(keys, k[1])
		}
		sort.Strings(keys)
		out[m[1]] = keys
	}
	return out
}

func TestPortalCatalogueIsComplete(t *testing.T) {
	got := portalCatalogue(t)
	if len(got) < 2 {
		t.Fatalf("the portal declares %d locale(s); with one there is nothing to guess "+
			"between and the browser need not be consulted at all", len(got))
	}

	// Every locale against every other: "complete" is not a property of one of
	// them, and comparing each to a chosen first would hide a key the first also
	// lacks.
	all := map[string]bool{}
	for _, keys := range got {
		for _, k := range keys {
			all[k] = true
		}
	}
	for locale, keys := range got {
		have := map[string]bool{}
		for _, k := range keys {
			have[k] = true
		}
		var missing []string
		for k := range all {
			if !have[k] {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("locale %q is missing %v.\n"+
				"The portal reads the browser's language, and it may only do that while "+
				"every string exists in every locale it offers. Translate them, or drop "+
				"the locale.", locale, missing)
		}
	}
}

// TestPortalRendersNoUntranslatedText: the boundary ADR-0267 draws holds here
// too. Interface words live in the catalogue, not in the markup, or a translated
// page would still have German headings in it.
func TestPortalRendersNoUntranslatedText(t *testing.T) {
	page := readWeb(t, "portal.html")
	body := page[strings.Index(page, "<body>"):]

	// The page's only text node is the one the script replaces.
	for _, word := range []string{"Katalog", "Bestellung", "Catalogue", "Order"} {
		if strings.Contains(body, ">"+word) {
			t.Errorf("portal.html renders %q directly; interface words belong in the "+
				"message catalogue, where every locale has them", word)
		}
	}
}

// TestPortalDeclaresTheBrandTokens: the page loads no app.css, so it declares the
// token set itself — the same one the public form declares, and for the same
// reason: theme.js writes the accent onto the root and the page has to have
// somewhere to write.
func TestPortalDeclaresTheBrandTokens(t *testing.T) {
	page := readWeb(t, "portal.html")
	for _, token := range []string{"--accent:", "--accent-hover:", "--accent-soft:", "--accent-ink:"} {
		if !strings.Contains(page, token) {
			t.Errorf("portal.html does not declare %s, so theme.js has nothing to override", token)
		}
	}
	if !strings.Contains(page, "/theme.js") {
		t.Error("portal.html does not load theme.js, so it would show the stock blue " +
			"whatever brand the operator set")
	}
}
