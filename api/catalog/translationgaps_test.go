package catalog

import (
	"strings"
	"testing"
)

// A missing translation stops nobody, and is not therefore invisible.
//
// Publishing used to refuse a product named in one declared language and not
// another, and the argument was good: a portal that shows one audience a product
// and the other an empty row is worse than no catalogue at all. What the argument
// missed is that the portal never shows an empty row — it falls back to whatever
// language the catalogue does have, because a name somebody cannot read is better
// than no name. So the refusal was not protecting a reader from a blank; it was
// stopping a maintainer from publishing a catalogue that was already usable.
//
// The two questions a publish answers are separated instead. Can this be ordered
// against — which is what a refusal is for — and is this as good as it should be,
// which is a thing to be told rather than blocked by. Only the first one refuses.

// twoLanguageGaps publishes against a catalogue declaring German and French and
// returns both answers: what refused, and what was merely noted.
func twoLanguageGaps(items ...Item) ([]Problem, []Problem) {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1,
			Languages: []string{"de", "fr"}, Items: ids}},
		Items: items,
	}
	_, problems := Publish(in)
	return problems, TranslationGaps(in)
}

// gapAbout finds the note mentioning a fragment, or fails.
func gapAbout(t *testing.T, gaps []Problem, want string) Problem {
	t.Helper()
	for _, g := range gaps {
		if strings.Contains(g.Message, want) {
			return g
		}
	}
	t.Fatalf("no gap mentioning %q; got %v", want, gaps)
	return Problem{}
}

// TestAProductNamedInOneOfTwoLanguagesPublishes.
//
// The change itself. A maintainer filling a catalogue writes the names in their
// own language first and the translations when somebody has time; under the old
// rule the catalogue could not go live until every one of them was done, so the
// first language's readers waited on the second language's translator.
func TestAProductNamedInOneOfTwoLanguagesPublishes(t *testing.T) {
	half := item("vpn")
	half.Texts = map[string]string{"de": "Fernzugriff"}

	problems, _ := twoLanguageGaps(half)
	if len(problems) != 0 {
		t.Errorf("a product named in one of two declared languages was refused: %+v.\n"+
			"The portal falls back to the language it has, so nobody meets a blank", problems)
	}
}

// TestAProductWithNoNameAtAllIsRefused.
//
// The floor the relaxation keeps, and it is not the same rule made smaller. A
// product missing one translation has a name; a product missing all of them has
// none, and the portal would show its id — which is a string nobody chose for a
// reader and which a maintainer cannot even see is wrong from the catalogue
// screen. There is no language to fall back to, so this is the one case the
// fallback cannot answer.
func TestAProductWithNoNameAtAllIsRefused(t *testing.T) {
	nameless := item("vpn")
	nameless.Texts = nil
	problems, _ := twoLanguageGaps(nameless)
	contains(t, problems, "no name in any language")

	// Whitespace is not a name, for the reason it is not a description: it is what
	// a cleared box leaves behind, and read as a name it would publish a product
	// whose row in the portal is blank.
	blank := item("screen")
	blank.Texts = map[string]string{"de": "   "}
	problems, _ = twoLanguageGaps(blank)
	contains(t, problems, "no name in any language")
}

// TestTheGapsAreReportedEvenThoughTheyDoNotRefuse.
//
// The other half, and the half that makes the relaxation honest. Removing a gate
// and putting nothing in its place is how a half-translated catalogue becomes
// invisible again — which is the failure the gate was built against. So what the
// gate used to refuse is now said, in one place, per language and per field.
func TestTheGapsAreReportedEvenThoughTheyDoNotRefuse(t *testing.T) {
	half := item("vpn")
	half.Texts = map[string]string{"de": "Fernzugriff"}
	half.Descriptions = map[string]string{"de": "Verschlüsselter Zugang."}
	half.Category = "Arbeitsplatz"
	half.CategoryTexts = map[string]string{"de": "Arbeitsplatz"}
	half.ProductGroup = "Netzzugang"
	half.ProductGroupTexts = map[string]string{"de": "Netzzugang"}

	_, gaps := twoLanguageGaps(half)

	for _, want := range []string{
		"no name in fr",
		"no description in fr",
		"no category in fr",
		"no product group in fr",
	} {
		g := gapAbout(t, gaps, want)
		if g.Item != "vpn" || g.Catalog != "cat" {
			t.Errorf("the gap %q names item %q of catalogue %q; a maintainer reading "+
				"a list of gaps has to be able to open the one it is about",
				want, g.Item, g.Catalog)
		}
	}

	// And nothing about the language that does have them. A report that names the
	// filled language beside the empty one doubles in length and halves in use.
	// Matched at the end, because the language is the last word: a bare search for
	// " de" also matches "no description in fr", and a guard that can never go
	// green is worse than none.
	for _, g := range gaps {
		if strings.HasSuffix(g.Message, " de") {
			t.Errorf("the language that is translated was reported as a gap: %+v", g)
		}
	}
}

// TestNothingOptionalIsReportedAsAGapWhenItIsAbsentEverywhere.
//
// A description is optional as a whole and always was. A product nobody described
// is not a translation gap — it is a product that needs no paragraph, and
// reporting it would bury the products that really are half-translated under
// every product that is deliberately plain. The same holds for a heading nobody
// wrote and for a product group nobody assigned.
func TestNothingOptionalIsReportedAsAGapWhenItIsAbsentEverywhere(t *testing.T) {
	plain := bilingual("vpn")

	_, gaps := twoLanguageGaps(plain)
	if len(gaps) != 0 {
		t.Errorf("a fully named product carrying no description and no headings was "+
			"reported as having gaps: %+v", gaps)
	}
}

// TestAFullyTranslatedCatalogueHasNoGaps is the case the report exists to stay
// quiet about.
func TestAFullyTranslatedCatalogueHasNoGaps(t *testing.T) {
	whole := bilingual("vpn")
	whole.Descriptions = map[string]string{"de": "Zugang.", "fr": "Accès."}
	whole.Category = "Arbeitsplatz"
	whole.CategoryTexts = map[string]string{"de": "Arbeitsplatz", "fr": "Poste de travail"}
	whole.ProductGroup = "Netzzugang"
	whole.ProductGroupTexts = map[string]string{"de": "Netzzugang", "fr": "Accès réseau"}

	problems, gaps := twoLanguageGaps(whole)
	if len(problems) != 0 {
		t.Fatalf("the fixture is unpublishable: %+v", problems)
	}
	if len(gaps) != 0 {
		t.Errorf("a fully translated product was reported as having gaps: %+v", gaps)
	}
}

// TestAHeadingWithNoWordingsAtAllIsNotAGap.
//
// The key renders in every language where there are no wordings, which is every
// product written before the wordings existed. Reporting those as gaps would name
// the whole installed base on the first screen somebody opens.
func TestAHeadingWithNoWordingsAtAllIsNotAGap(t *testing.T) {
	keyed := bilingual("vpn")
	keyed.Category = "Arbeitsplatz"
	keyed.ProductGroup = "Netzzugang"

	_, gaps := twoLanguageGaps(keyed)
	if len(gaps) != 0 {
		t.Errorf("a heading carrying its key and no wordings was reported as a gap: "+
			"%+v.\nThe key renders in every language, which is what carrying no "+
			"wording means", gaps)
	}
}

// TestAContradictionStillRefuses.
//
// What was relaxed is the *incompleteness*, and nothing else. A wording with no
// key to group by, and one key worded two ways, are not gaps somebody will fill
// in later — they are statements that cannot both be true, and the portal cannot
// render either of them sensibly.
func TestAContradictionStillRefuses(t *testing.T) {
	orphan := bilingual("vpn")
	orphan.CategoryTexts = map[string]string{"de": "Arbeitsplatz"}
	problems, _ := twoLanguageGaps(orphan)
	contains(t, problems, "translated category but no category")

	one := bilingual("laptop")
	one.Category = "Arbeitsplatz"
	one.CategoryTexts = map[string]string{"de": "Arbeitsplatz", "fr": "Poste de travail"}
	two := bilingual("screen")
	two.Category = "Arbeitsplatz"
	two.CategoryTexts = map[string]string{"de": "Arbeitsplatz", "fr": "Bureau"}
	problems, _ = twoLanguageGaps(one, two)
	contains(t, problems, "words the category")
}

// The shapes a product is ordered in, held to the same rule as the product.
//
// They were left out of the first cut of this report and named in its record as
// an open risk: a variant's texts are a map per language like everything else,
// publishing never checked them, and a catalogue whose products were translated
// and whose shapes were not was a state nothing mentioned. The portal draws the
// shapes in the basket, where an orderer has to choose one — so a German word in
// an English basket is not a cosmetic gap, it is the moment somebody picks.

// TestAShapeNamedInOneLanguageIsAGapLikeAnythingElse.
func TestAShapeNamedInOneLanguageIsAGapLikeAnythingElse(t *testing.T) {
	half := bilingual("laptop")
	half.Variants = []Variant{
		{ID: "big", Texts: map[string]string{"de": "Gross", "fr": "Grand"}},
		{ID: "small", Texts: map[string]string{"de": "Klein"}},
	}

	problems, gaps := twoLanguageGaps(half)
	if len(problems) != 0 {
		t.Fatalf("a half-named shape was refused: %+v", problems)
	}
	g := gapAbout(t, gaps, "no name for the shape small in fr")
	if g.Item != "laptop" || g.Catalog != "cat" {
		t.Errorf("the gap names item %q of catalogue %q; a maintainer has to be able "+
			"to open the thing it is about", g.Item, g.Catalog)
	}
	// And nothing about the shape that is named in both.
	for _, got := range gaps {
		if strings.Contains(got.Message, "big") {
			t.Errorf("the shape that is translated was reported: %+v", got)
		}
	}
}

// TestAShapeNamedNowhereIsReportedOnceAndNotPerLanguage.
//
// Its own finding rather than one gap per declared language, because it is a
// different piece of work: this shape needs naming, not translating, and saying
// so four times for a four-language catalogue would bury the shapes that are
// merely half-translated.
//
// Reported and not refused, unlike a product named nowhere. A product's id is
// minted for a catalogue and means nothing to a reader; a shape's is often the
// word itself — "black", "silver", "large" — so the portal falling back to it is
// frequently adequate, and refusing the publish would stop catalogues that read
// perfectly well.
func TestAShapeNamedNowhereIsReportedOnceAndNotPerLanguage(t *testing.T) {
	bare := bilingual("laptop")
	bare.Variants = []Variant{{ID: "black"}}

	problems, gaps := twoLanguageGaps(bare)
	if len(problems) != 0 {
		t.Fatalf("a shape carrying no name was refused: %+v", problems)
	}
	gapAbout(t, gaps, "the shape black is named in no language")

	n := 0
	for _, g := range gaps {
		if strings.Contains(g.Message, "black") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the unnamed shape is reported %d times; it is one piece of work and "+
			"a four-language catalogue would list it four times", n)
	}
}

// TestAProductWithNoShapesHasNoShapeGaps is the ordinary case: most products come
// in one shape and carry no variants at all.
func TestAProductWithNoShapesHasNoShapeGaps(t *testing.T) {
	plain := bilingual("laptop")
	if _, gaps := twoLanguageGaps(plain); len(gaps) != 0 {
		t.Errorf("a product with no shapes was reported as having shape gaps: %+v", gaps)
	}
}
