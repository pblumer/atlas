package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
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

// stringsCatalogue reads one page's message catalogue: locale to sorted keys. It
// is shared by the portal and the approval page because both offer the browser's
// language under the same condition, and a second copy of the parser is a second
// thing to keep pointed at the right place.
func stringsCatalogue(t *testing.T, file string) map[string][]string {
	t.Helper()
	src := readWeb(t, file)

	start := strings.Index(src, "const STRINGS = {")
	if start < 0 {
		t.Fatalf("%s declares no STRINGS catalogue — if it moved, this test now "+
			"passes vacuously and must be pointed at the new place", file)
	}
	end := strings.Index(src[start:], "\n};")
	if end < 0 {
		t.Fatalf("%s: the STRINGS catalogue is not closed where this test expects", file)
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

// assertCatalogueIsComplete holds the condition the language record names: every
// string exists in every locale the page offers.
func assertCatalogueIsComplete(t *testing.T, page string, got map[string][]string) {
	t.Helper()
	if len(got) < 2 {
		t.Fatalf("%s declares %d locale(s); with one there is nothing to guess "+
			"between and the browser need not be consulted at all", page, len(got))
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
			t.Errorf("%s: locale %q is missing %v.\n"+
				"The page reads the browser's language, and it may only do that while "+
				"every string exists in every locale it offers. Translate them, or drop "+
				"the locale.", page, locale, missing)
		}
	}
}

func TestPortalCatalogueIsComplete(t *testing.T) {
	assertCatalogueIsComplete(t, "portal.js", stringsCatalogue(t, "portal.js"))
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
	if !strings.Contains(page, `type="module" src="/portal.js"`) {
		t.Error("portal.html does not load portal.js as a module, so its import of " +
			"theme.js's palette derivation cannot resolve and the page would show the " +
			"stock blue whatever brand was set")
	}
}

// TestPortalDerivesNoPaletteOfItsOwn: the accent's hover, soft and ink shades are
// computed in one place. The ink in particular decides whether a label stays
// readable on a brand colour, and a second implementation of that is a second
// place for it to be wrong (ADR-0263).
func TestPortalDerivesNoPaletteOfItsOwn(t *testing.T) {
	src := readWeb(t, "portal.js")
	if !strings.Contains(src, `from './theme.js'`) {
		t.Fatal("portal.js does not import theme.js — if it now derives the palette " +
			"itself, that derivation exists twice")
	}
	// Naming a derived token in a comment is how the rule is explained; assigning
	// one is how it gets broken. Only the assignment is the defect.
	for _, token := range []string{"--accent-hover", "--accent-soft", "--accent-ink"} {
		for _, form := range []string{
			`setProperty("` + token, `setProperty('` + token, token + `:`,
		} {
			if strings.Contains(src, form) {
				t.Errorf("portal.js assigns %s, which theme.js derives. Setting it here "+
					"means computing it here.", token)
			}
		}
	}
}

// TestPortalTypefacesMatchTheServer: the page paints with a stack, the server
// refuses a name that is not one, and a catalogue naming a face the page has no
// stack for would silently render in the default one.
func TestPortalTypefacesMatchTheServer(t *testing.T) {
	src := readWeb(t, "portal.js")
	for name := range catalog.Typefaces {
		if !strings.Contains(src, name+":") {
			t.Errorf("the server offers the typeface %q and portal.js has no stack for it", name)
		}
	}
}

// TestPortalMarkCascadeStopsAtTheOperator holds the one decision in the brand
// mark that is not obvious from reading the code: what the portal shows when
// nobody has set one.
//
// The console falls back to the built-in Atlas glyph, which is right there — it is
// the operator's own tool. The portal is a customer-facing page, and branding
// somebody's service catalogue with the name of the engine underneath it is a
// disclosure nobody asked for. So the cascade is catalogue, then operator, then
// nothing, and a future edit that "fixes the empty box" by importing the glyph
// fails here instead of shipping.
func TestPortalMarkCascadeStopsAtTheOperator(t *testing.T) {
	src := readWeb(t, "portal.js")

	if !strings.Contains(src, "/logo`") {
		t.Error("portal.js asks for no catalogue mark, so a catalogue's own logo is never shown")
	}
	if !strings.Contains(src, "/api/v1/settings/logo") {
		t.Error("portal.js does not fall back to the operator's mark")
	}
	// Matched as code and not as a mention: the comment above renderMark names
	// logo.js as the thing it is deliberately *not* importing, and a check that
	// could not tell the two apart would forbid explaining the decision.
	for _, forbidden := range []string{"BUILTIN_MARK", "'./logo.js'", `"./logo.js"`} {
		if strings.Contains(src, forbidden) {
			t.Errorf("portal.js reaches for %s: the portal is a customer-facing page and "+
				"the engine's own glyph does not belong on it", forbidden)
		}
	}
	// An uploaded SVG is scriptable. It is safe in an <img> and in nothing else, so
	// the page must never put those bytes into the document.
	for _, forbidden := range []string{"innerHTML", "insertAdjacentHTML"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("portal.js uses %s; a mark is rendered through an <img> and never "+
				"inlined, or an uploaded SVG's script runs in the page", forbidden)
		}
	}
}

// TestPageBuildersDropAnUnsetAttribute.
//
// `disabled: busy ? 'disabled' : null` is the natural way to write a conditional
// attribute, and setAttribute has no falsy handling: it renders disabled="null",
// which a browser reads as disabled. On the approval page that meant both buttons
// dead from the first paint, found by reading rather than by running — there is no
// browser in this test suite, so the guard is asserted in the source instead.
func TestPageBuildersDropAnUnsetAttribute(t *testing.T) {
	for _, page := range []string{"portal.js", "genehmigung.js"} {
		src := readWeb(t, page)
		i := strings.Index(src, "function el(tag, attrs")
		if i < 0 {
			t.Errorf("%s has no el() builder where this test expects one", page)
			continue
		}
		body := src[i:]
		if end := strings.Index(body, "\n}\n"); end > 0 {
			body = body[:end]
		}
		if !strings.Contains(body, "v == null") {
			t.Errorf("%s's el() sets every attribute it is given, including a nullish one. "+
				"A conditional attribute then renders as the string \"null\", which for "+
				"disabled means permanently disabled.", page)
		}
	}
}
