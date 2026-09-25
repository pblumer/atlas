package catalog

import "testing"

// The heading and the group, read in the language the portal is read in.
//
// ADR-0360 shipped both as one string and recorded "no translation" as a stated
// cost. This is that cost being paid off, and the shape it is paid off in is what
// these tests hold: the string stays, as the key everything groups by, and the
// translations sit beside it. Nothing regroups, no stored record changes meaning,
// and a catalogue that declares four languages can say the heading in four.

// translated builds a product carrying a heading, a group and the translations of
// both — the shape a multilingual catalogue writes.
func translated(id string) Item {
	it := item(id)
	it.Texts = map[string]string{"de": id, "fr": id}
	it.Category = "Arbeitsplatz"
	it.CategoryTexts = map[string]string{"de": "Arbeitsplatz", "fr": "Poste de travail"}
	it.ProductGroup = "Mobile Geräte"
	it.ProductGroupTexts = map[string]string{"de": "Mobile Geräte", "fr": "Appareils mobiles"}
	return it
}

// twoLanguages publishes against a catalogue declaring German and French, which is
// the only shape in which a half-translated heading is a statement at all.
func twoLanguages(items ...Item) ([]Problem, Release) {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1,
			Languages: []string{"de", "fr"}, Items: ids}},
		Items: items,
	})
	return problems, rel
}

// TestTheHeadingsTravelIntoTheRelease.
//
// The portal reads the release and nothing else, so a translation that did not
// travel is a translation nobody ever sees — the same defect as a name that did
// not travel, one column over.
func TestTheHeadingsTravelIntoTheRelease(t *testing.T) {
	problems, rel := twoLanguages(translated("laptop"))
	if len(problems) != 0 {
		t.Fatalf("publish refused a translated heading: %+v", problems)
	}
	if len(rel.Items) != 1 {
		t.Fatalf("the release carries %d items, want 1", len(rel.Items))
	}
	got := rel.Items[0]
	if got.CategoryTexts["fr"] != "Poste de travail" {
		t.Errorf("the release carries categoryTexts %v; a French reader is shown the "+
			"German heading, which is the thing this field exists to stop", got.CategoryTexts)
	}
	if got.ProductGroupTexts["fr"] != "Appareils mobiles" {
		t.Errorf("the release carries productGroupTexts %v; the second column stayed "+
			"German", got.ProductGroupTexts)
	}
	// The key is untouched by the translations, because everything groups by it.
	if got.Category != "Arbeitsplatz" || got.ProductGroup != "Mobile Geräte" {
		t.Errorf("the release carries category %q and group %q; the key is what the "+
			"portal groups by and what an older release already holds, so it may not "+
			"move when a translation is added", got.Category, got.ProductGroup)
	}
}

// TestAHeadingWithNoTranslationsStillPublishes.
//
// Every product written before this field existed is in exactly this state, and so
// is every product in a catalogue that declares one language. Demanding a
// translation would refuse the whole installed base.
func TestAHeadingWithNoTranslationsStillPublishes(t *testing.T) {
	plain := item("laptop")
	plain.Texts = map[string]string{"de": "laptop", "fr": "portable"}
	plain.Category = "Arbeitsplatz"
	plain.ProductGroup = "Mobile Geräte"

	if problems, _ := twoLanguages(plain); len(problems) != 0 {
		t.Errorf("a heading with no translations was refused: %+v.\n"+
			"That is every product written before the field existed", problems)
	}
}

// TestAHalfTranslatedHeadingIsReportedAndNotRefused.
//
// The rule the description follows, in the field one column further left, and
// relaxed with it. A heading worded in German and not French renders the German
// for the French reader, which is a gap and not a blank — so it is said rather
// than blocked. The whole of that argument is in translationgaps_test.go.
func TestAHalfTranslatedHeadingIsReportedAndNotRefused(t *testing.T) {
	half := translated("laptop")
	delete(half.CategoryTexts, "fr")
	problems, gaps := twoLanguageProblemsAndGaps(half)
	if len(problems) != 0 {
		t.Errorf("a heading worded in one of two declared languages was refused: %+v", problems)
	}
	contains(t, gaps, "no category in fr")

	halfGroup := translated("screen")
	delete(halfGroup.ProductGroupTexts, "fr")
	problems, gaps = twoLanguageProblemsAndGaps(halfGroup)
	if len(problems) != 0 {
		t.Errorf("a group worded in one of two declared languages was refused: %+v", problems)
	}
	contains(t, gaps, "no product group in fr")
}

// TestABlankTranslationIsNotATranslation.
//
// Three spaces render as an empty column head, which is the defect the blank
// heading check refuses in the key. A field cleared to whitespace must not count
// as the wording that closes the gap — otherwise clearing a box is how a product
// disappears from the list of what still needs translating.
func TestABlankTranslationIsNotATranslation(t *testing.T) {
	blank := translated("laptop")
	blank.CategoryTexts["fr"] = "   "
	_, gaps := twoLanguageProblemsAndGaps(blank)
	contains(t, gaps, "no category in fr")
}

// twoLanguageProblemsAndGaps publishes against a de/fr catalogue and returns both
// answers: what refused, and what was noted.
func twoLanguageProblemsAndGaps(items ...Item) ([]Problem, []Problem) {
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

// TestATranslationWithoutAHeadingIsRefused.
//
// The key is what the portal groups by. A product carrying translations and no key
// sits in the bucket for products with no heading — under a column head reading
// "Ohne Kategorie", while carrying the word for one in four languages. Nothing
// would be wrong at runtime, and the maintainer's intent would be silently lost.
func TestATranslationWithoutAHeadingIsRefused(t *testing.T) {
	orphan := translated("laptop")
	orphan.Category = ""
	problems, _ := twoLanguages(orphan)
	contains(t, problems, "translated category but no category")

	orphanGroup := translated("screen")
	orphanGroup.ProductGroup = ""
	problems, _ = twoLanguages(orphanGroup)
	contains(t, problems, "translated product group but no product group")
}

// TestTheKeyNeedNotBeOneOfTheTranslations.
//
// Deliberately not checked, and worth a test so the absence reads as a decision.
// Where a catalogue translates its headings the key is free to be a stable slug
// that no reader ever meets — which is the shape that survives a maintainer
// deciding the German wording was wrong. Demanding the key equal the first
// language's word would take that away for no gain, because nothing reads the key
// but the grouping.
func TestTheKeyNeedNotBeOneOfTheTranslations(t *testing.T) {
	slug := translated("laptop")
	slug.Category = "arbeitsplatz-v2"
	slug.ProductGroup = "mobile-geraete"
	if problems, _ := twoLanguages(slug); len(problems) != 0 {
		t.Errorf("a heading keyed by a slug was refused: %+v", problems)
	}
}

// TestTwoProductsMayNotWordOneHeadingTwoWays.
//
// Two spellings are two categories and always were — that is the key's identity
// and the cost ADR-0360 recorded. This is the opposite case and it is new: one
// key, two wordings. The portal shows one column head, so one of the two products
// decides what the other's heading says, and nothing on the screen says a choice
// was made. It is provable at publication, so it is refused there (I5).
func TestTwoProductsMayNotWordOneHeadingTwoWays(t *testing.T) {
	one := translated("laptop")
	two := translated("screen")
	two.CategoryTexts = map[string]string{"de": "Arbeitsplatz", "fr": "Bureau"}

	problems, _ := twoLanguages(one, two)
	contains(t, problems, "words the category")

	// The group is held to the same rule, in the column beside it.
	three := translated("dock")
	three.ProductGroupTexts = map[string]string{"de": "Mobile Geräte", "fr": "Mobiles"}
	problems, _ = twoLanguages(one, three)
	contains(t, problems, "words the product group")
}

// TestAgreeingProductsPublish is the other half, and it is the case that matters:
// a catalogue's products normally all carry the same wording for a shared heading,
// and a check that could not tell agreement from disagreement would refuse every
// multilingual catalogue there is.
func TestAgreeingProductsPublish(t *testing.T) {
	if problems, _ := twoLanguages(translated("laptop"), translated("screen")); len(problems) != 0 {
		t.Errorf("two products agreeing on a heading were refused: %+v", problems)
	}
	// A product carrying the key and no translation agrees with one that carries
	// both: it has said nothing to disagree with, and it renders the key.
	plain := item("dock")
	plain.Texts = map[string]string{"de": "dock", "fr": "dock"}
	plain.Category = "Arbeitsplatz"
	if problems, _ := twoLanguages(translated("laptop"), plain); len(problems) != 0 {
		t.Errorf("an untranslated product was refused beside a translated one: %+v.\n"+
			"It renders the key, which is what carrying no translation means", problems)
	}
}
