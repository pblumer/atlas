package order

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// In a structured release a service is ordered only as part of a Marktleistung
// (ADR-draft-marktleistung-and-service). The shop never offers one on its own, and
// the order holds the same rule, so a caller of the API cannot place what the shop
// would not show.

func aStructuredRelease() catalog.Release {
	return catalog.Release{
		ID: "rel_1", Structured: true,
		Items: []catalog.Item{
			{ID: "laptop", Kind: catalog.ItemOffering},
			{ID: "laptop-hw", Kind: catalog.ItemService},
			{ID: "huelle", Kind: catalog.ItemService},
		},
		Includes: map[string][]string{"laptop": {"laptop-hw"}},
		Options:  map[string][]string{"laptop": {"huelle"}},
	}
}

func TestAMarktleistungIsOrderedWithOrWithoutItsOption(t *testing.T) {
	rel := aStructuredRelease()
	for _, sel := range [][]string{{"laptop"}, {"laptop", "huelle"}, {"laptop", "laptop-hw"}} {
		if got := directServiceIn(rel, sel, nil); got != "" {
			t.Errorf("%v refused: %s", sel, got)
		}
	}
}

func TestAServiceAloneIsRefused(t *testing.T) {
	rel := aStructuredRelease()
	for _, id := range []string{"laptop-hw", "huelle"} {
		got := directServiceIn(rel, []string{id}, nil)
		if !strings.Contains(got, id) || !strings.Contains(got, "only as part of a Marktleistung") {
			t.Errorf("ordering %s alone = %q, want a refusal naming it", id, got)
		}
	}
}

func TestAnOptionIsOrderedLaterForAHeldMarktleistung(t *testing.T) {
	// What a held bundle lacks is offered again (ADR-0312, amendment of
	// 2026-10-28): the sleeve somebody did not tick with the laptop.
	if got := directServiceIn(aStructuredRelease(), []string{"huelle"}, map[string]bool{"laptop": true}); got != "" {
		t.Errorf("an option of a held Marktleistung was refused: %s", got)
	}
	// An included part is not offered again: it came with the whole.
	if got := directServiceIn(aStructuredRelease(), []string{"laptop-hw"}, map[string]bool{"laptop": true}); got == "" {
		t.Error("an included part was ordered on its own because its whole is held")
	}
}

func TestAReleaseThatIsNotStructuredOrdersAnything(t *testing.T) {
	rel := aStructuredRelease()
	rel.Structured = false
	if got := directServiceIn(rel, []string{"laptop-hw"}, nil); got != "" {
		t.Errorf("a legacy release was held to the structured rule: %s", got)
	}
}
