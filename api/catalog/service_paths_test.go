package catalog

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestAnEditorMayNotChangeWhoMaintainsTheCatalogue: an editor may change the
// catalogue but not hand editing to anybody else (ADR-0071) — the refusal says so
// plainly and the member list stays the owner's. The admin passes, as everywhere.
func TestAnEditorMayNotChangeWhoMaintainsTheCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))
	share := `{"members":[{"ref":{"type":"user","id":"usr_ed"},"role":"editor"}]}`
	if rec := as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", share, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("owner share = %d (%s)", rec.Code, rec.Body)
	}

	widen := `{"members":[{"ref":{"type":"user","id":"usr_ed"},"role":"editor"},` +
		`{"ref":{"type":"user","id":"usr_friend"},"role":"editor"}]}`
	rec := as(t, s.HandleUpdateCatalog, user("usr_ed"), "PATCH", widen, "id", cat.ID)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "an editor may change the catalogue but not its member list") {
		t.Fatalf("editor widening the members = %d (%s), want 403 saying why", rec.Code, rec.Body)
	}
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, user("usr_a"), "GET", "", "id", cat.ID))
	if len(got.Members) != 1 {
		t.Fatalf("members after the refused change = %+v, want the owner's one grant", got.Members)
	}

	admin := &httpapi.Principal{UserID: "usr_root", Roles: []string{"admin"}}
	if rec := as(t, s.HandleUpdateCatalog, admin, "PATCH", widen, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("admin widening the members = %d (%s), want 200", rec.Code, rec.Body)
	}
}

// TestACatalogueCannotOfferAProductNobodyCreated: the refusal arrives at the write
// that made the mistake, not at the next publish, and the list is left as it was.
func TestACatalogueCannotOfferAProductNobodyCreated(t *testing.T) {
	s := serviceWithAdmin(t)
	owner := user("usr_a")
	cat := makeCatalog(t, s, owner)
	if rec := as(t, s.HandleSaveItem, owner, "POST", productBody("laptop", cat.ID)); rec.Code != http.StatusOK {
		t.Fatalf("save product = %d (%s)", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH", `{"items":["laptop"]}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("offer laptop = %d (%s)", rec.Code, rec.Body)
	}

	rec := as(t, s.HandleUpdateCatalog, owner, "PATCH", `{"items":["laptop","ghost"]}`, "id", cat.ID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot offer ghost because no such product exists") {
		t.Fatalf("offering ghost = %d (%s), want 400 naming it", rec.Code, rec.Body)
	}
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, owner, "GET", "", "id", cat.ID))
	if !reflect.DeepEqual(got.Items, []string{"laptop"}) {
		t.Fatalf("items after the refusal = %v, want [laptop]", got.Items)
	}
}

// TestACatalogueAlreadyOfferingAGhostStaysRepairable: only what a write adds is
// checked. A catalogue stored with a dangling id from before the rule can still be
// written — including the write that removes the ghost — instead of being locked by
// its own old damage.
func TestACatalogueAlreadyOfferingAGhostStaysRepairable(t *testing.T) {
	s := serviceWithAdmin(t)
	owner := user("usr_a")
	cat := makeCatalog(t, s, owner)
	if rec := as(t, s.HandleSaveItem, owner, "POST", productBody("laptop", cat.ID)); rec.Code != http.StatusOK {
		t.Fatalf("save product = %d (%s)", rec.Code, rec.Body)
	}
	stored, _, err := s.store.Catalog(cat.ID)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	stored.Items = []string{"ghost", "laptop"}
	if err := s.store.SaveCatalog(stored); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}

	for _, body := range []string{`{"items":["ghost","laptop"],"rank":2}`, `{"items":["laptop"]}`} {
		if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH", body, "id", cat.ID); rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s = %d (%s), want 200", body, rec.Code, rec.Body)
		}
	}
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, owner, "GET", "", "id", cat.ID))
	if !reflect.DeepEqual(got.Items, []string{"laptop"}) || got.Rank != 2 {
		t.Fatalf("catalogue = items %v rank %d, want [laptop] and rank 2", got.Items, got.Rank)
	}
}
