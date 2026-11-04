package catalog

import (
	"testing"
)

// A structured catalogue (ADR-0524) declares what every
// product is: a Marktleistung, the whole a person orders, built from services; or a
// service, one building block with a lifecycle of its own. These tests state the
// rules a publish holds such a catalogue to, and that a catalogue which has not
// opted in is published exactly as before.

// offering builds a publishable Marktleistung carrying both headings.
func marktleistung(id string) Item {
	it := item(id)
	it.Kind = ItemOffering
	it.Category = "arbeitsalltag-bewaeltigen"
	it.ProductGroup = "endgeraet-bereitstellen"
	return it
}

// brick builds a publishable service.
func brick(id string) Item {
	it := item(id)
	it.Kind = ItemService
	return it
}

func compositionEdge(from, to string) Edge { return Edge{From: from, To: to, Kind: EdgeComposition} }
func aggregationEdge(from, to string) Edge { return Edge{From: from, To: to, Kind: EdgeAggregation} }

// structured builds the input of one structured catalogue offering every item.
func structured(items []Item, edges ...Edge) Input {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return Input{
		CatalogID: "cat",
		Catalogs:  []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids, Structured: true}},
		Items:     items,
		Edges:     edges,
	}
}

func TestAStructuredCatalogueOfMarktleistungenAndServicesPublishes(t *testing.T) {
	in := structured(
		[]Item{marktleistung("laptop"), brick("laptop-hw"), brick("laptop-os"), brick("huelle")},
		compositionEdge("laptop", "laptop-hw"),
		compositionEdge("laptop", "laptop-os"),
		aggregationEdge("laptop", "huelle"),
		requires("laptop-os", "laptop-hw"),
	)
	rel := mustPublish(t, in)
	if !rel.Structured {
		t.Fatal("the release of a structured catalogue does not say it is structured; the order could not hold the shop's rule")
	}
}

func TestAStructuredCatalogueRefusesAProductThatIsNeither(t *testing.T) {
	in := structured([]Item{marktleistung("laptop"), item("laptop-hw")}, compositionEdge("laptop", "laptop-hw"))
	_, problems := Publish(in)
	contains(t, problems, "is neither a Marktleistung nor a service")
}

func TestAnUnknownKindIsRefusedEvenOutsideAStructuredCatalogue(t *testing.T) {
	it := item("laptop")
	it.Kind = "bundle"
	_, problems := Publish(Input{Items: []Item{it}})
	contains(t, problems, `kind "bundle"`)
}

func TestAMarktleistungIsBuiltFromAtLeastOneIncludedService(t *testing.T) {
	// An optional part alone does not make a Lego house: ordered without the
	// option, the Marktleistung would provision nothing.
	in := structured([]Item{marktleistung("laptop"), brick("huelle")}, aggregationEdge("laptop", "huelle"))
	_, problems := Publish(in)
	contains(t, problems, "includes no service")
}

func TestAMarktleistungContainsServicesOnly(t *testing.T) {
	inner := marktleistung("monitor")
	in := structured(
		[]Item{marktleistung("arbeitsplatz"), inner, brick("monitor-hw"), brick("laptop-hw")},
		compositionEdge("arbeitsplatz", "laptop-hw"),
		compositionEdge("arbeitsplatz", "monitor"),
		compositionEdge("monitor", "monitor-hw"),
	)
	_, problems := Publish(in)
	contains(t, problems, "contains monitor, which is not a service")
	contains(t, problems, "is contained by arbeitsplatz")
}

func TestAMarktleistungNamesItsCategoryAndProductGroup(t *testing.T) {
	ml := marktleistung("laptop")
	ml.Category, ml.ProductGroup = "", " "
	in := structured([]Item{ml, brick("laptop-hw")}, compositionEdge("laptop", "laptop-hw"))
	_, problems := Publish(in)
	contains(t, problems, "names no category")
	contains(t, problems, "names no product group")
}

func TestAServiceHasNoParts(t *testing.T) {
	in := structured(
		[]Item{marktleistung("laptop"), brick("laptop-hw"), brick("akku")},
		compositionEdge("laptop", "laptop-hw"),
		compositionEdge("laptop-hw", "akku"),
	)
	_, problems := Publish(in)
	contains(t, problems, "is a service and contains akku")
}

func TestAServiceNoMarktleistungContainsIsRefused(t *testing.T) {
	// The shop never offers a service on its own, so a service nothing contains is
	// a product nobody can order — and the catalogue would not say why.
	in := structured(
		[]Item{marktleistung("laptop"), brick("laptop-hw"), brick("vergessen")},
		compositionEdge("laptop", "laptop-hw"),
	)
	_, problems := Publish(in)
	contains(t, problems, "no Marktleistung in this catalogue contains it")
}

func TestAServiceCarriesNoHeading(t *testing.T) {
	hw := brick("laptop-hw")
	hw.ProductGroup = "endgeraet-bereitstellen"
	hw.ProductGroupTexts = map[string]string{"de": "Endgeräte"}
	in := structured([]Item{marktleistung("laptop"), hw}, compositionEdge("laptop", "laptop-hw"))
	_, problems := Publish(in)
	contains(t, problems, "headings belong to the Marktleistung")
}

func TestACatalogueThatHasNotOptedInPublishesAsBefore(t *testing.T) {
	// The legacy shape: products with no kind, a root without parts, a heading on a
	// part. Nothing here is refused, because nothing here was declared.
	hw := item("laptop-hw")
	hw.Category = "Arbeitsplatz"
	in := Input{Items: []Item{item("laptop"), hw, item("konto")},
		Edges: []Edge{compositionEdge("laptop", "laptop-hw")}}
	rel := mustPublish(t, in)
	if rel.Structured {
		t.Fatal("a catalogue that never opted in was released as structured")
	}
}

func TestTheSubjectCatalogueDecidesWhetherTheRulesApply(t *testing.T) {
	// Another catalogue in the set being structured says nothing about this one.
	in := Input{
		CatalogID: "cat",
		Catalogs: []Catalog{
			{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"konto"}},
			{ID: "other", Rank: 2, Structured: true},
		},
		Items: []Item{item("konto")},
	}
	if rel := mustPublish(t, in); rel.Structured {
		t.Fatal("another catalogue's switch was applied to this one")
	}
}
