package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
)

// scopePurge decides what a purge removes from the stores' contents alone; these
// are the edges of that decision the HTTP tests do not reach.

func purgeFixture() ([]catalog.Catalog, []catalog.Item, []catalog.Release, []order.Order) {
	cats := []catalog.Catalog{
		{ID: "home", Items: []string{"vpn", "laptop"}},
		{ID: "other", Items: []string{"vpn", "phone"}, Edges: []catalog.Edge{{From: "phone", To: "vpn", Kind: catalog.EdgeRequires}}},
	}
	items := []catalog.Item{
		{ID: "vpn", HomeCatalog: "home", ProvisionProcess: "prov", DeprovisionProcess: "deprov"},
		{ID: "laptop", HomeCatalog: "home", LifecycleProcess: "life", ProvisionProcess: catalog.FulfilmentProcess},
		{ID: "phone", HomeCatalog: "other", ProvisionProcess: "prov-phone", DeprovisionProcess: "deprov-phone"},
	}
	rels := []catalog.Release{
		{ID: "rel-home", CatalogID: "home", CreatedAt: 1, Items: []catalog.Item{{ID: "vpn"}, {ID: "laptop"}}},
		{ID: "rel-other-old", CatalogID: "other", CreatedAt: 1, Items: []catalog.Item{{ID: "phone"}}},
		{ID: "rel-other-new", CatalogID: "other", CreatedAt: 2, Items: []catalog.Item{{ID: "phone"}, {ID: "vpn"}}},
	}
	orders := []order.Order{
		{ID: "o-home", ReleaseID: "rel-home", Lines: []order.Line{{ItemID: "laptop"}}},
		{ID: "o-mixed", ReleaseID: "rel-other-new", Lines: []order.Line{{ItemID: "phone"}, {ItemID: "vpn"}, {ItemID: "vpn"}}},
		{ID: "o-vpn", ReleaseID: "rel-other-new", Lines: []order.Line{{ItemID: "vpn"}}},
		{ID: "o-phone", ReleaseID: "rel-other-new", Lines: []order.Line{{ItemID: "phone"}}},
		{ID: "o-empty", ReleaseID: "rel-other-new"},
	}
	return cats, items, rels, orders
}

func TestScopingACatalogueTakesItsOwnProductsAndTheOrdersOfThem(t *testing.T) {
	cats, items, rels, orders := purgeFixture()
	sc, ok := scopePurge(purgeCatalog, "home", cats, items, rels, orders)
	if !ok {
		t.Fatal("the catalogue was not found")
	}
	p := sc.plan
	if !slices.Equal(p.Catalogs, []string{"home"}) || !slices.Equal(p.Releases, []string{"rel-home"}) {
		t.Errorf("catalogs %v, releases %v", p.Catalogs, p.Releases)
	}
	if !slices.Equal(p.Products, []string{"laptop", "vpn"}) {
		t.Errorf("products = %v", p.Products)
	}
	// Placed against the catalogue's release, or carrying nothing but its products —
	// and not the order with no lines at all, which carries none of them.
	if !slices.Equal(p.Orders, []string{"o-home", "o-vpn"}) {
		t.Errorf("orders = %v", p.Orders)
	}
	if len(p.KeptOrders) != 1 || p.KeptOrders[0].Order != "o-mixed" ||
		!slices.Equal(p.KeptOrders[0].Products, []string{"vpn"}) {
		t.Errorf("keptOrders = %+v, want o-mixed losing vpn once", p.KeptOrders)
	}
	if len(p.StrippedFrom) != 1 || p.StrippedFrom[0].Catalog != "other" {
		t.Errorf("strippedFrom = %+v", p.StrippedFrom)
	}
	// The newest release is what the shop serves, so that is the one named.
	if len(p.StillOffered) != 1 || p.StillOffered[0].Release != "rel-other-new" ||
		!slices.Equal(p.StillOffered[0].Products, []string{"vpn"}) {
		t.Errorf("stillOffered = %+v", p.StillOffered)
	}
	// The fulfilment orchestration is Atlas's own and never a product's to leave.
	if !slices.Equal(p.Processes, []string{"deprov", "life", "prov"}) {
		t.Errorf("processes = %v", p.Processes)
	}
}

func TestScopingAProductLeavesEveryCatalogueStanding(t *testing.T) {
	cats, items, rels, orders := purgeFixture()
	sc, ok := scopePurge(purgeProduct, "vpn", cats, items, rels, orders)
	if !ok {
		t.Fatal("the product was not found")
	}
	p := sc.plan
	if len(p.Catalogs) != 0 || len(p.Releases) != 0 {
		t.Errorf("a product purge deletes catalogues %v and releases %v", p.Catalogs, p.Releases)
	}
	if !slices.Equal(p.Orders, []string{"o-vpn"}) {
		t.Errorf("orders = %v", p.Orders)
	}
	stripped := []string{}
	for _, s := range p.StrippedFrom {
		stripped = append(stripped, s.Catalog)
	}
	if !slices.Equal(stripped, []string{"home", "other"}) {
		t.Errorf("strippedFrom = %v, want both catalogues that offer it", stripped)
	}
}

func TestScopingSomethingUnknownFindsNothing(t *testing.T) {
	cats, items, rels, orders := purgeFixture()
	for _, k := range []purgeKind{purgeCatalog, purgeProduct, "something"} {
		if _, ok := scopePurge(k, "nirgends", cats, items, rels, orders); ok {
			t.Errorf("%s nirgends was found", k)
		}
	}
	// A catalogue id is not a product id, nor the other way round.
	if _, ok := scopePurge(purgeProduct, "home", cats, items, rels, orders); ok {
		t.Error("a catalogue id was scoped as a product")
	}
}

func TestThePlanSaysWhatItMeansBeforeAnybodyConfirms(t *testing.T) {
	cats, items, rels, orders := purgeFixture()
	sc, _ := scopePurge(purgeCatalog, "home", cats, items, rels, orders)
	sc.plan.explain(purgeWork{
		instances: []uint64{1, 2},
		holds:     []purgeHold{{"alice", "vpn"}, {"alice", "laptop"}, {"bob", "vpn"}},
	})
	if sc.plan.Instances != 2 || sc.plan.Entitlements != 3 || sc.plan.Holders != 2 {
		t.Errorf("instances/entitlements/holders = %d/%d/%d", sc.plan.Instances, sc.plan.Entitlements, sc.plan.Holders)
	}
	all := strings.Join(sc.plan.Warnings, "\n")
	for _, want := range []string{"--catalogue-purge=false", "permanent", "not undone", "deprovisioning",
		"corrected", "rel-other-new", "stay deployed: deprov, life, prov."} {
		if !strings.Contains(all, want) {
			t.Errorf("the warnings do not mention %q:\n%s", want, all)
		}
	}
}
