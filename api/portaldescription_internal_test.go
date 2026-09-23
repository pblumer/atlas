package api

import (
	"strings"
	"testing"
)

// What a product is, in a language its reader has.
//
// The description reached the portal and then stopped at one line of it. The
// reader's language was required to be a key on the product, and a missing key
// meant no description at all — not a shorter one, none. That was written
// deliberately, on a reading of the publish rule that does not hold, and the
// note in portal.js said so in as many words.
//
// The two language lists are not the same list. Publishing demands a description
// in every language the CATALOGUE declares (api/catalog/publish.go). The portal
// is read in one of ITS OWN locales, taken from the browser and narrowed to the
// ones the page is translated into. A catalogue offered in German and French is
// complete by the publish rule and had nothing whatever to say to a reader whose
// browser is English: two descriptions stored, neither shown.

// TestADescriptionIsShownEvenWhereTheCatalogueDoesNotSpeakThePagesLanguage.
//
// The defect this file exists for. A strict lookup is one `[locale]` with nothing
// after it, and the giveaway is the function reaching no other value.
func TestADescriptionIsShownEvenWhereTheCatalogueDoesNotSpeakThePagesLanguage(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function descriptionOf(", "\n}")
	if !strings.Contains(body, "Object.values(") {
		t.Error("descriptionOf reaches only the keys it names, so a catalogue that " +
			"declares neither of this page's locales shows no description at all — " +
			"which is the state publishing cannot refuse, because the catalogue's " +
			"languages and the page's are different lists")
	}
}

// TestTheReadersOwnLanguageIsPreferredOverTheRest.
//
// The fall-through must not become "whichever key came first". A paragraph in a
// language somebody does not read is better than nothing and worse than one they
// do, so the locale is asked for before anything else — which is the whole of
// what "in der entsprechenden Sprache" means once more than one exists.
func TestTheReadersOwnLanguageIsPreferredOverTheRest(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function descriptionOf(", "\n}")
	// Through pickText, which selects by the tag's LANGUAGE rather than by the
	// whole tag — a catalogue kept in de-DE is German to a reader on de, and a
	// lookup for the whole tag would find nothing (ADR-0413, as amended).
	at := strings.Index(body, "pickText(d, locale)")
	if at < 0 {
		t.Fatal("descriptionOf does not read the page's locale at all, so a reader " +
			"gets whichever language the catalogue happens to list first")
	}
	// Every other candidate has to come after it.
	for _, later := range []string{"Object.values("} {
		if i := strings.Index(body, later); i >= 0 && i < at {
			t.Errorf("descriptionOf reaches %q before the reader's own locale, so a "+
				"product described in both languages shows the wrong one", later)
		}
	}
}

// TestAnEmptyDescriptionIsNotADescription.
//
// A key present and blank is the shape a half-filled form leaves behind. Taken as
// an answer it ends the search, and the languages that do say something are never
// reached — so the reader gets an empty paragraph where the panel promised an
// explanation, which reads as something that failed to load.
func TestAnEmptyDescriptionIsNotADescription(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function descriptionOf(", "\n}")
	if !strings.Contains(body, "trim()") {
		t.Error("descriptionOf does not measure a candidate before returning it, so a " +
			"blank string in the reader's language hides the one that is written")
	}
}

// TestThePanelActuallyDrawsTheDescriptionAndThePicture.
//
// Reading the description correctly and putting it on screen are two statements,
// and the reason to hold the second is that the panel is now the only place
// either one appears. A row in the cascade is a name; what a product *is* and
// what it looks like are here or nowhere.
//
// The absent case is part of it: most products carry no paragraph, and an empty
// one left in its place is a gap that reads as something that failed to load
// rather than as a product nobody described.
func TestThePanelActuallyDrawsTheDescriptionAndThePicture(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function infoPanel(", "\n}")
	if !strings.Contains(body, "descriptionOf(item)") {
		t.Error("the panel no longer shows what the product is, so a description " +
			"somebody wrote is stored, frozen into the release and never read")
	}
	if !strings.Contains(body, "product-description") {
		t.Error("the description is drawn without the class the page styles it by, " +
			"so it arrives as an unmeasured line of body text")
	}
	if !strings.Contains(body, "? el(") || !strings.Contains(body, ": null") {
		t.Error("the description is drawn unconditionally, so a product nobody " +
			"described leaves an empty paragraph where an explanation was promised")
	}
	if !strings.Contains(body, "productPicture(item.id)") {
		t.Error("the panel no longer shows the product, so a picture the catalogue " +
			"carries is stored and never seen")
	}
}
