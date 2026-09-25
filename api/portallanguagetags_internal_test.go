package api

import (
	"strings"
	"testing"
)

// A catalogue kept in de-DE and en-EN, read by a portal whose locale is `de`.
//
// This is the defect of ADR-0413 in a new guise, and it survived that fix. The
// portal narrows a browser's language to its base — `de-CH` becomes `de` — because
// its own words exist in two languages and a message catalogue is keyed by them.
// A product's texts are keyed by the tags the CATALOGUE declares, and those are
// whatever a maintainer typed: `de-DE`, `en-GB`, `pt-BR` are all correct BCP 47
// and all refuse to be found by a lookup for `de`, `en`, `pt`.
//
// So a catalogue declared in `de-DE; en-EN; fr-FR` would store every name under a
// key no locale ever asks for, fall through to the first value it had, and show
// the same word in both languages — which is precisely the symptom that started
// this, arrived at down a different road.
//
// The fix is to match a tag by its base rather than by its whole self, and the
// place it has to happen is every read of a text on this page.

// TestATextIsFoundByTheLanguageAndNotByTheWholeTag.
func TestATextIsFoundByTheLanguageAndNotByTheWholeTag(t *testing.T) {
	src := readWeb(t, "shop.js")

	body := webRegion(t, src, "function pickText(", "\n}")
	if !strings.Contains(body, "baseOf(tag) === base") {
		t.Error("a text is still selected by its whole tag, so a catalogue declared " +
			"in de-DE stores every name under a key this page never asks for")
	}
	// The exact tag first. A catalogue carrying both `de` and `de-CH` means the
	// two deliberately, and answering with whichever came first in the document
	// would be a coin toss.
	if !strings.Contains(body, "texts[base]") {
		t.Error("the exact tag is no longer preferred over a regional one, so a " +
			"catalogue carrying both gets whichever the release happened to list first")
	}

	// Every read goes through it. textOf is the name, descriptionOf the paragraph;
	// the two headings reach it through textOf and are covered by that.
	for _, fn := range []string{"function textOf(", "function descriptionOf("} {
		region := webRegion(t, src, fn, "\n}")
		if !strings.Contains(region, "pickText(") {
			t.Errorf("%s reads the map itself instead of going through pickText, so "+
				"it finds a regional tag only by accident", fn)
		}
	}
}

// TestTheBaseOfATagIsTheLanguage: `de-CH` is German, `zh-Hans` is Chinese, and a
// bare `de` is its own base. Held here because the whole fix above rests on it.
func TestTheBaseOfATagIsTheLanguage(t *testing.T) {
	body := webRegion(t, readWeb(t, "shop.js"), "function baseOf(", "\n}")
	if !strings.Contains(body, "toLowerCase()") || !strings.Contains(body, "split('-')[0]") {
		t.Error("baseOf no longer takes the primary subtag, lowercased — which is " +
			"what pickLocale does to the browser's own list, and the two have to agree")
	}
}

// TestTheLanguageListIsSplitOnWhatPeopleType.
//
// The Console's language box takes a list of tags and used to split it on commas
// alone. A language tag can contain neither a comma, a semicolon nor a space, so
// all three are separators and none of them is ambiguous — and a maintainer who
// types the wrong one now gets three languages rather than one refusal naming a
// tag they never meant to write.
//
// This is not the normalisation ADR-0413 refused. That one was about a single
// stored ENTRY that might be one tag or two, where only the author knew; this is
// about how a human's line is cut into entries in the first place, which has one
// reading.
func TestTheLanguageListIsSplitOnWhatPeopleType(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")

	body := webRegion(t, src, "const languageList = ", "\n")
	for _, sep := range []string{",", ";"} {
		if !strings.Contains(body, sep) {
			t.Errorf("the language list is not split on %q, which is a separator a "+
				"maintainer types and no language tag can contain", sep)
		}
	}
	if !strings.Contains(body, `\s`) {
		t.Error("the language list is not split on whitespace, so \"de en\" is read " +
			"as one tag that is not one")
	}

	// And both boxes use it. The catalogue is created on one screen and edited on
	// another, and the live defect was written on the second.
	if n := strings.Count(src, `languageList(f.get("languages"))`); n != 2 {
		t.Errorf("the language box is read through languageList in %d places, want 2 "+
			"(creating a catalogue and editing one)", n)
	}
	// Keywords keep the comma-only rule: a keyword may legitimately contain a
	// semicolon, and splitting one would invent a search term nobody typed.
	if strings.Contains(src, `languageList(f.get("keywords"))`) {
		t.Error("the keywords are split like languages; a keyword is free text and " +
			"may carry a semicolon, so splitting it invents a term")
	}
}

// The switch offers the languages the catalogue is kept in.
//
// It used to offer this page's own two, always. A catalogue kept only in German
// therefore carried an EN button that turned the furniture English and left every
// product name German — a half-translated screen offered by the page itself,
// which is the state ADR-0267 refuses to reach by guessing and ADR-0313 sets the
// condition against.
//
// Narrowed to what this page can render, and that narrowing IS ADR-0313 applied:
// a catalogue may be kept in French, and until the furniture is French too an FR
// button would promise a French portal and deliver a half-translated one.

// TestTheSwitchIsBuiltFromTheCatalogueAndNotFromThePage.
func TestTheSwitchIsBuiltFromTheCatalogueAndNotFromThePage(t *testing.T) {
	src := readWeb(t, "shop.js")
	body := webRegion(t, src, "function offeredLocales(", "\n}")

	if !strings.Contains(body, "state.catalog") {
		t.Error("the switch does not read the catalogue, so it offers the same two " +
			"languages whatever the catalogue is actually kept in")
	}
	// The condition ADR-0313 sets: a language is offered only where every string
	// this page renders exists in it.
	if !strings.Contains(body, "STRINGS[baseOf(l)]") {
		t.Error("a language the page has no strings for can be offered, so a visitor " +
			"can land on the half-translated screen ADR-0313 exists to prevent")
	}
	// And the sign-in screen still has a switch: it is drawn before there is a
	// catalogue, and a German-speaking visitor meeting an English form is what the
	// whole message catalogue exists to avoid.
	if !strings.Contains(body, "Object.keys(STRINGS)") {
		t.Error("there is no fallback for the case with no catalogue, so the switch " +
			"disappears from the sign-in screen")
	}
}

// TestTheChoiceIsSettledOntoALanguageTheCatalogueHas.
//
// The choice is made before the catalogue is read — from the address, from this
// browser, or from the visitor's own list — so it is a language and not yet one
// of this catalogue's tags. Left unsettled, a reader on `en` meeting a catalogue
// kept in `en-EN` reads neither: the switch shows nothing as chosen and the texts
// fall through.
func TestTheChoiceIsSettledOntoALanguageTheCatalogueHas(t *testing.T) {
	src := readWeb(t, "shop.js")
	body := webRegion(t, src, "function settleLocale(", "\n}")

	if !strings.Contains(body, "baseOf(l) === baseOf(locale)") {
		t.Error("the choice is not carried onto the same language in another tag, so " +
			"a reader on en meeting a catalogue kept in en-EN gets its first language")
	}
	// Called once the catalogue is known and before anything is drawn.
	load := webRegion(t, src, "async function load(", "\n}")
	if !strings.Contains(load, "settleLocale();") {
		t.Error("the choice is never settled, so the switch cannot show the language " +
			"the page is actually being read in")
	}
	if strings.Index(load, "settleLocale();") < strings.Index(load, "state.catalog = await") {
		t.Error("the choice is settled before the catalogue is read, against a list " +
			"that is not there yet")
	}
}
