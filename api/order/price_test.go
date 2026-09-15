package order

import (
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// The figure an order was placed at (ADR-0361).
//
// Frozen onto the line for the reason the approval rule and the ceiling are: an
// approver saw a figure and decided on it, and a catalogue edit afterwards must
// not make the record show a different one than the one that was approved.

// TestTheLineKeepsThePriceTheReleaseSaid.
func TestTheLineKeepsThePriceTheReleaseSaid(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	it := catalog.Item{
		ID: "laptop", HomeCatalog: "cat", State: catalog.StateActive,
		Texts: map[string]string{"de": "Notebook"}, Approval: catalog.Approval{Kind: catalog.KindNone},
		ProvisionProcess: "p", DeprovisionProcess: "d", Price: "CHF 1'200.–",
	}
	rel, problems := catalog.Publish(catalog.Input{
		Catalogs: []catalog.Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"laptop"}}},
		Items:    []catalog.Item{it},
	})
	if len(problems) != 0 {
		t.Fatalf("publish: %v", problems)
	}
	rel.ID, rel.CatalogID = "rel_1", "cat"

	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		ignoreGrant, ignoreRevoke, holdsNothing)

	rec := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["laptop"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("place: %d (%s)", rec.Code, rec.Body)
	}
	line := lineOf(t, placed(t, rec.Body), "laptop")
	if line.Price != "CHF 1'200.–" {
		t.Errorf("the line kept %q; without the figure the order cannot say what was "+
			"decided on", line.Price)
	}
}
