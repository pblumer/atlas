package catalog

import (
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestTheDownloadOffersTheCataloguesAPersonMaintainsMarkedByTheirProcesses: an
// application package's download offers each catalogue the person may maintain, sorted
// by id, marked bound when one of its products runs on a process asked about; a
// catalogue somebody else owns is not offered (ADR-0467).
func TestTheDownloadOffersTheCataloguesAPersonMaintainsMarkedByTheirProcesses(t *testing.T) {
	s := newService(t)
	for _, c := range []Catalog{
		{ID: "cat-b", OwnerID: "usr_owner", Texts: map[string]string{"de": "B"}},
		{ID: "cat-a", OwnerID: "usr_owner", Texts: map[string]string{"de": "A"}, Items: []string{"shared"}},
		{ID: "cat-foreign", OwnerID: "someone-else"},
	} {
		if err := s.store.SaveCatalog(c); err != nil {
			t.Fatal(err)
		}
	}
	bound := newItem("bound")
	bound.HomeCatalog = "cat-b"
	shared := newItem("shared")
	shared.HomeCatalog = "cat-foreign"
	shared.ProvisionProcess, shared.DeprovisionProcess = "other", "other"
	for _, it := range []Item{bound, shared} {
		if err := s.store.SaveItem(it); err != nil {
			t.Fatal(err)
		}
	}

	owner := &httpapi.Principal{UserID: "usr_owner"}
	got, err := s.ExportChoices(owner, []string{"prov"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "cat-a" || got[1].ID != "cat-b" {
		t.Fatalf("choices = %+v, want cat-a and cat-b in that order", got)
	}
	if got[0].Bound || !got[1].Bound {
		t.Errorf("bound = %v/%v, want only cat-b bound (its product runs on prov)", got[0].Bound, got[1].Bound)
	}
	if got[0].Texts["de"] != "A" {
		t.Errorf("texts = %v", got[0].Texts)
	}

	other, err := s.ExportChoices(owner, []string{"other"})
	if err != nil || len(other) != 2 || !other[0].Bound || other[1].Bound {
		t.Fatalf("choices for other = %+v, %v; want cat-a bound through the product it offers", other, err)
	}
	if none, err := s.ExportChoices(&httpapi.Principal{UserID: "nobody"}, nil); err != nil || len(none) != 0 {
		t.Fatalf("a stranger is offered %+v, %v", none, err)
	}
}
