package catalog

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

// TestACatalogueBodyPastItsBudgetIsRefused: a catalogue document and a catalogue's
// theme are read up to their budgets, answered with 413 past them, and nothing is
// written — the catalogue list is as long as it was and the theme is unset.
func TestACatalogueBodyPastItsBudgetIsRefused(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	s.Limits = limits.Default()
	s.Limits.Definition = 512
	s.Limits.Theme = 128

	big := `{"rank":7,"languages":["de"],"texts":{"de":"Zu gross"},"padding":"` + strings.Repeat("a", 600) + `"}`
	if rec := as(t, s.HandleCreateCatalog, admin(), "POST", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("creating a catalogue past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}
	if got := decode[[]Catalog](t, as(t, s.HandleListCatalogs, admin(), "GET", "")); len(got) != 1 {
		t.Errorf("%d catalogues exist after the refusal, want the one made before it", len(got))
	}

	theme := `{"accent":"#7a1f3d","padding":"` + strings.Repeat("a", 200) + `"}`
	if rec := as(t, s.HandleSetTheme, admin(), "PUT", theme, "id", cat.ID); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a theme past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}
	if got := decode[Catalog](t, as(t, s.HandleGetCatalog, admin(), "GET", "", "id", cat.ID)); got.Theme.Accent != "" {
		t.Errorf("the refused theme was stored: %+v", got.Theme)
	}

	// Within the budgets both are taken as before.
	if rec := as(t, s.HandleSetTheme, admin(), "PUT", `{"accent":"#7a1f3d"}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Errorf("a theme within its budget = %d (%s), want 200", rec.Code, rec.Body)
	}
}
