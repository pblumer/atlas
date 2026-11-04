package catalog

import (
	"errors"
	"testing"
)

// In a structured catalogue the two headings are references, not words
// (ADR-draft-marktleistung-and-service): the category is the key of a capability,
// the product group the key of a capability that category requires. These tests
// hold the publish to that, and say where the words then come from.

type register map[string]CapabilityHeading

func (r register) CapabilityHeading(key string) (CapabilityHeading, bool, error) {
	if key == "kaputt" {
		return CapabilityHeading{}, false, errors.New("disk on fire")
	}
	h, ok := r[key]
	return h, ok, nil
}

func workRegister() register {
	return register{
		"arbeitsalltag-bewaeltigen": {Name: "Arbeitsalltag bewältigen",
			Requires: []string{"endgeraet-bereitstellen", "digitale-identitaet-bereitstellen"}},
		"endgeraet-bereitstellen":           {Name: "Endgeräte"},
		"digitale-identitaet-bereitstellen": {Name: "Identität und Zugang"},
		"mobile-kommunikation-ermoeglichen": {Name: "Mobile Kommunikation"},
		"altlast":                           {Name: "Altlast", Deprecated: true},
	}
}

func structuredRelease(items ...Item) Release {
	return Release{Structured: true, Items: items}
}

func TestHeadingsThatNameTheRegisterPass(t *testing.T) {
	if got := HeadingProblems(structuredRelease(marktleistung("laptop"), brick("laptop-hw")), workRegister()); len(got) != 0 {
		t.Fatalf("problems: %v", got)
	}
}

func TestACategoryMustBeACapability(t *testing.T) {
	ml := marktleistung("laptop")
	ml.Category = "Arbeitsplatz"
	contains(t, HeadingProblems(structuredRelease(ml), workRegister()), "category Arbeitsplatz, which is no capability")
}

func TestAProductGroupMustBeACapability(t *testing.T) {
	ml := marktleistung("laptop")
	ml.ProductGroup = "Laptop"
	contains(t, HeadingProblems(structuredRelease(ml), workRegister()), "product group Laptop, which is no capability")
}

func TestAProductGroupIsOneItsCategoryRequires(t *testing.T) {
	// It exists, and it is not below this category: the shop would file the
	// product under a chain the register does not hold.
	ml := marktleistung("iphone")
	ml.ProductGroup = "mobile-kommunikation-ermoeglichen"
	contains(t, HeadingProblems(structuredRelease(ml), workRegister()),
		"which capability arbeitsalltag-bewaeltigen does not require")
}

func TestADeprecatedCapabilityIsNoHeading(t *testing.T) {
	ml := marktleistung("laptop")
	ml.Category = "altlast"
	contains(t, HeadingProblems(structuredRelease(ml), workRegister()), "is deprecated")
}

func TestAnUnreadableRegisterRefusesRatherThanPasses(t *testing.T) {
	ml := marktleistung("laptop")
	ml.Category = "kaputt"
	contains(t, HeadingProblems(structuredRelease(ml), workRegister()), "could not be read")
	contains(t, HeadingProblems(structuredRelease(ml), nil), "cannot read the capability register")
}

func TestAReleaseThatIsNotStructuredIsNotAsked(t *testing.T) {
	ml := marktleistung("laptop")
	ml.Category = "Arbeitsplatz"
	if got := HeadingProblems(Release{Items: []Item{ml}}, nil); len(got) != 0 {
		t.Fatalf("a free-text catalogue was held to the register: %v", got)
	}
}

func TestTheRegisterWordsAHeadingTheProductLeftUnworded(t *testing.T) {
	ml := marktleistung("laptop")
	ml.CategoryTexts = map[string]string{"en": "Getting work done"}
	items := []Item{ml, brick("laptop-hw")}
	WordHeadings(items, []string{"de", "en"}, workRegister())

	if got := items[0].CategoryTexts["de"]; got != "Arbeitsalltag bewältigen" {
		t.Fatalf("de category = %q, want the capability's name", got)
	}
	if got := items[0].CategoryTexts["en"]; got != "Getting work done" {
		t.Fatalf("en category = %q; the product's own wording was overwritten", got)
	}
	if got := items[0].ProductGroupTexts["en"]; got != "Endgeräte" {
		t.Fatalf("en product group = %q, want the capability's name as the fallback", got)
	}
	if items[1].CategoryTexts != nil {
		t.Fatal("a service was given a heading")
	}
}

func TestWordingNeedsARegister(t *testing.T) {
	items := []Item{marktleistung("laptop")}
	WordHeadings(items, []string{"de"}, nil)
	if items[0].CategoryTexts != nil {
		t.Fatal("wording invented with no register to read")
	}
}
