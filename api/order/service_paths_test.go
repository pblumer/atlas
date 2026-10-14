package order

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// Separation of duties at the door (ADR-0342), and the eligibility refusal as a
// placement sees it (ADR-0347). conflictIn had no test of its own; the reason
// sentences did.

// releaseExcluding publishes a catalogue of the given products in which each pair
// must never be held together.
func releaseExcluding(t *testing.T, ids []string, pairs ...[2]string) catalog.Release {
	t.Helper()
	var items []catalog.Item
	for _, id := range ids {
		items = append(items, catalog.Item{
			ID: id, HomeCatalog: "cat", State: catalog.StateActive,
			Texts:            map[string]string{"de": id},
			Approval:         catalog.Approval{Kind: catalog.KindNone},
			ProvisionProcess: "prov", DeprovisionProcess: "deprov",
		})
	}
	var edges []catalog.Edge
	for _, p := range pairs {
		edges = append(edges, catalog.Edge{From: p[0], To: p[1], Kind: catalog.EdgeExcludes})
	}
	rel, problems := catalog.Publish(catalog.Input{
		Catalogs: []catalog.Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
		Edges:    edges,
	})
	if len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
	rel.ID, rel.CatalogID = "rel_1", "cat"
	return rel
}

// TestAConflictWithWhatIsHeldIsFound: ordering one half of a forbidden pair while
// holding the other is the case a detective control would only find afterwards.
func TestAConflictWithWhatIsHeldIsFound(t *testing.T) {
	rel := releaseExcluding(t, []string{"approve-payment", "create-supplier", "vpn"},
		[2]string{"approve-payment", "create-supplier"})

	got := conflictIn(rel, []string{"vpn", "create-supplier"}, map[string]bool{"approve-payment": true})
	want := conflict{Ordered: "create-supplier", Other: "approve-payment", Held: true}
	if got == nil || *got != want {
		t.Fatalf("conflictIn = %+v, want %+v", got, want)
	}
}

// TestBothHalvesInOneBasketAreOneConflict: the walk meets the pair from both sides,
// and the refusal names it once, the same way whichever order the basket arrived in.
func TestBothHalvesInOneBasketAreOneConflict(t *testing.T) {
	rel := releaseExcluding(t, []string{"approve-payment", "create-supplier"},
		[2]string{"create-supplier", "approve-payment"})
	want := conflict{Ordered: "approve-payment", Other: "create-supplier"}
	for _, basket := range [][]string{
		{"approve-payment", "create-supplier"},
		{"create-supplier", "approve-payment"},
	} {
		got := conflictIn(rel, basket, nil)
		if got == nil || *got != want {
			t.Fatalf("conflictIn(%v) = %+v, want %+v", basket, got, want)
		}
	}
}

// TestNoConflictWithoutBothHalves: an exclusion is about holding the two together;
// either one alone is ordinary, and so is a catalogue that excludes nothing.
func TestNoConflictWithoutBothHalves(t *testing.T) {
	rel := releaseExcluding(t, []string{"approve-payment", "create-supplier", "vpn"},
		[2]string{"approve-payment", "create-supplier"})
	if got := conflictIn(rel, []string{"approve-payment", "vpn"}, map[string]bool{"vpn": true}); got != nil {
		t.Fatalf("conflictIn = %+v for a basket holding only one half, want none", got)
	}
	plain := releaseExcluding(t, []string{"a", "b"})
	if got := conflictIn(plain, []string{"a", "b"}, map[string]bool{"a": true}); got != nil {
		t.Fatalf("conflictIn = %+v in a catalogue without exclusions, want none", got)
	}
}

// serviceSelling builds an order service over one release, with the recipient's
// holdings and groups as given.
func serviceSelling(t *testing.T, rel catalog.Release, holds map[string]bool, groups []string) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	return New(loop, store, func() int64 { return 1700 },
		func(id string) (catalog.Release, bool, error) { return rel, id == rel.ID, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(string) ([]string, error) { return groups, nil }, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		func() string { return "http://atlas.test" },
		ignoreGrant, ignoreRevoke,
		func(string) (map[string]bool, error) { return holds, nil })
}

// TestPlacingAForbiddenCombinationIsRefusedAndWritesNothing: 409, because giving one
// half back would resolve it, and no order is left behind to be fulfilled anyway.
func TestPlacingAForbiddenCombinationIsRefusedAndWritesNothing(t *testing.T) {
	rel := releaseExcluding(t, []string{"approve-payment", "create-supplier"},
		[2]string{"approve-payment", "create-supplier"})
	s := serviceSelling(t, rel, map[string]bool{"approve-payment": true}, nil)

	refused := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["create-supplier"]}`)
	if refused.Code != http.StatusConflict {
		t.Fatalf("ordering against a held exclusion = %d (%s), want 409", refused.Code, refused.Body)
	}
	if body := refused.Body.String(); !strings.Contains(body, "already") || !strings.Contains(body, "approve-payment") {
		t.Errorf("the refusal does not say what is already held: %s", body)
	}
	listed := do(t, s.HandleList, someone("usr_1"), "GET", "")
	if strings.Contains(listed.Body.String(), `"releaseId"`) {
		t.Errorf("a refused order was stored: %s", listed.Body)
	}
}

// TestPlacingForAnIneligibleRecipientIsForbidden: 403 rather than 409 — nothing can
// be given back to fix who the recipient is.
func TestPlacingForAnIneligibleRecipientIsForbidden(t *testing.T) {
	rel := releaseExcluding(t, []string{"domain-admin"})
	for i := range rel.Items {
		rel.Items[i].Eligible = []string{"grp_it"}
	}
	s := serviceSelling(t, rel, nil, []string{"grp_sales"})

	refused := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["domain-admin"]}`)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("ordering a restricted product for an outsider = %d (%s), want 403", refused.Code, refused.Body)
	}
	if body := refused.Body.String(); !strings.Contains(body, "not eligible for domain-admin") {
		t.Errorf("the refusal does not name the product: %s", body)
	}

	// The same basket for somebody in the group goes through: the gate narrows, it
	// does not close.
	allowed := serviceSelling(t, rel, nil, []string{"grp_it"})
	if placed := do(t, allowed.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["domain-admin"]}`); placed.Code != http.StatusCreated {
		t.Fatalf("ordering for an eligible recipient = %d (%s), want 201", placed.Code, placed.Body)
	}
}
