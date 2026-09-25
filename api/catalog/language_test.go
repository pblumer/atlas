package catalog

import (
	"net/http"
	"strings"
	"testing"
)

// A language tag has to be a language tag.
//
// The gap this closes, found in a live installation: a catalogue was saved with
// the language list `de; en` — one tag, containing a semicolon, because the field
// is read comma-separated and somebody typed the separator they had been taught
// for a different field. Nothing refused it, and nothing could have noticed it
// later either, because every layer treats a language tag as an opaque map key.
//
// What followed was invisible and total. The product form draws one box per
// declared language, so it drew one box labelled `de; en`; the names went in
// under that key; and the portal, looking up `texts['de']` and `texts['en']`,
// found neither and fell through to the first value it had. The language switch
// did nothing, for every product, and no screen anywhere said why.
//
// So it is refused where it is written. Never where it is read: a catalogue
// already carrying a tag like that must stay readable, or the installation that
// has the defect is the one that cannot open the screen to fix it.

func TestALanguageTagThatIsNotOneIsRefused(t *testing.T) {
	for _, bad := range []struct{ tag, why string }{
		{"de; en", "the live defect: two tags and a separator read as one tag"},
		{"de en", "a space is not a subtag separator"},
		{"de,en", "the list separator inside a list entry"},
		{"", "an empty tag names no language"},
		{"   ", "whitespace names no language"},
		{"deutsch", "a language name is not a language tag"},
		{"d", "one letter is no language subtag"},
		{"de-", "a trailing hyphen leaves an empty subtag"},
		{"de--CH", "an empty subtag in the middle"},
		{"de_CH", "an underscore is the POSIX spelling, not the BCP 47 one"},
	} {
		if ValidLanguageTag(bad.tag) {
			t.Errorf("%q is accepted as a language tag (%s)", bad.tag, bad.why)
		}
	}
}

func TestTheTagsPeopleActuallyUseAreAccepted(t *testing.T) {
	for _, good := range []string{"de", "en", "fr", "it", "rm", "de-CH", "fr-CH", "pt-BR", "zh-Hans", "en-GB"} {
		if !ValidLanguageTag(good) {
			t.Errorf("%q is refused as a language tag, and it is one", good)
		}
	}
}

// TestACatalogueCannotBeSavedWithATagThatIsNotOne.
//
// At the write, because that is the only place the mistake is still cheap. The
// message names the tag: a refusal reading "invalid languages" against a list of
// four leaves somebody guessing which one.
func TestACatalogueCannotBeSavedWithATagThatIsNotOne(t *testing.T) {
	s := newService(t)

	rec := do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de; en"],"texts":{"de":"Katalog"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("creating a catalogue with the language %q = %d (%s), want 400",
			"de; en", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "de; en") {
		t.Errorf("the refusal does not name the tag it refused: %s", rec.Body)
	}

	// And the same on the way in through an edit, which is how the live one was
	// written: the catalogue existed first and the language was added later.
	cat := decode[Catalog](t, do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":2,"languages":["de"],"texts":{"de":"Katalog"}}`))
	rec = do(t, s.HandleUpdateCatalog, "PATCH", "/x", `{"languages":["de; en"]}`, "id", cat.ID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patching the language list to %q = %d (%s), want 400",
			"de; en", rec.Code, rec.Body)
	}

	// The refused patch changed nothing, which is the same promise every other
	// refusal on this handler makes.
	got := decode[Catalog](t, do(t, s.HandleGetCatalog, "GET", "/x", "", "id", cat.ID))
	if len(got.Languages) != 1 || got.Languages[0] != "de" {
		t.Errorf("the refused patch left languages %v; a refusal applies none of "+
			"the fields rather than some of them", got.Languages)
	}
}

// TestTheSameLanguageTwiceIsRefused.
//
// Not pedantry: every per-language box in the Console is drawn from this list, so
// a duplicate draws two boxes writing the same key, where the second silently
// wins and the first looks like it was ignored.
func TestTheSameLanguageTwiceIsRefused(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de","en","de"],"texts":{"de":"Katalog"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a language list naming de twice = %d (%s), want 400", rec.Code, rec.Body)
	}
}

// TestACatalogueAlreadyCarryingABadTagStaysReadable.
//
// The rule that makes this safe to ship. An installation with the defect must be
// able to open the catalogue, see the tag and correct it — so the refusal is at
// the write and nowhere else. Reading, publishing and ordering all go on exactly
// as before, because the alternative is an installation locked out of its own fix.
func TestACatalogueAlreadyCarryingABadTagStaysReadable(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de; en"}, Items: []string{"a"}}},
		Items:    []Item{item("a")},
	}
	if _, problems := Publish(in); len(problems) != 0 {
		t.Errorf("publishing a catalogue that already carries a bad tag was refused: "+
			"%+v.\nThe installation with the defect would be the one that cannot "+
			"publish the fix", problems)
	}
}
