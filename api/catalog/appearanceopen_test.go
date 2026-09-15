package catalog

import (
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// With enforcement off there is nobody to be, not nobody who may.
//
// The rule is stated in mayRead and answered by the predicate the server hands in:
// `!authEnabled || (p != nil && p.HasRole(admin))`, which is true for everybody
// when nobody is signed in. Every gate in this package calls it plainly — except
// the three that set a catalogue's appearance, which wrote `p != nil && s.admin(p)`
// and so turned "everybody" back into "nobody".
//
// With --auth=false — the documented development and demo mode — a catalogue's
// colour, typeface and brand mark could not be set or removed at all. Every
// attempt was 403, and the message said "a catalogue's appearance is set by an
// administrator" to somebody who was, as far as the server was concerned, nobody.
//
// The nil check was never load-bearing: with enforcement **on** the predicate
// already answers false for a nil principal. It only ever subtracted.
//
// Every test in this package builds its service with the enforcement-on shape of
// that predicate, which is why this survived: the tests never modelled the mode in
// which it bites.

// openService is a service as the server builds one with --auth=false: the
// predicate the real Server hands in, with authEnabled false.
func openService(t *testing.T) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	authEnabled := false
	return New(loop, store, func() int64 { return 1700 },
		func(p *httpapi.Principal) bool {
			return !authEnabled || (p != nil && p.HasRole("admin"))
		})
}

// onePixelPNG is the smallest thing brandimage will accept as a PNG.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
	0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1,
	0x0d, 0x0a, 0x2d, 0xb4,
	0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// TestWithEnforcementOffAnAppearanceCanStillBeSet.
func TestWithEnforcementOffAnAppearanceCanStillBeSet(t *testing.T) {
	s := openService(t)
	cat := decode[Catalog](t, as(t, s.HandleCreateCatalog, nil, "POST",
		`{"rank":1,"languages":["de"],"texts":{"de":"Kundenkatalog"}}`))

	// Nil, because with enforcement off a request carries no principal at all.
	if rec := as(t, s.HandleSetTheme, nil, "PUT",
		`{"accent":"#d52b1e","typeface":"serif"}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("setting a theme with enforcement off = %d, want 200 (%s).\n"+
			"A demo instance could not brand a catalogue at all", rec.Code, rec.Body)
	}
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, nil, "GET", "", "id", cat.ID))
	if got.Theme.Accent != "#d52b1e" || got.Theme.Typeface != "serif" {
		t.Errorf("the catalogue kept %+v", got.Theme)
	}
}

// TestWithEnforcementOffABrandMarkCanStillBeSetAndRemoved: the same gate, twice
// more, on the upload and the removal.
func TestWithEnforcementOffABrandMarkCanStillBeSetAndRemoved(t *testing.T) {
	s := openService(t)
	cat := decode[Catalog](t, as(t, s.HandleCreateCatalog, nil, "POST",
		`{"rank":1,"languages":["de"],"texts":{"de":"Kundenkatalog"}}`))

	if rec := asTyped(t, s.HandleSetLogo, nil, "PUT", "image/png",
		string(onePixelPNG), "id", cat.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("uploading a brand mark with enforcement off = %d, want 204 (%s)",
			rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleGetLogo, nil, "GET", "", "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("reading it back = %d, want 200", rec.Code)
	}
	if rec := as(t, s.HandleDeleteLogo, nil, "DELETE", "", "id", cat.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("removing it with enforcement off = %d, want 204 (%s)", rec.Code, rec.Body)
	}
}

// TestWithEnforcementOnTheAppearanceIsStillTheAdministrators: the half that keeps
// the fix from being a hole. Dropping the nil check must not let an unauthenticated
// request through while enforcement is on.
func TestWithEnforcementOnTheAppearanceIsStillTheAdministrators(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := decode[Catalog](t, as(t, s.HandleCreateCatalog, admin(), "POST",
		`{"rank":1,"languages":["de"],"texts":{"de":"Kundenkatalog"}}`))

	for _, who := range []*httpapi.Principal{
		nil,
		{UserID: "usr_ada", Roles: []string{"user"}},
	} {
		if rec := as(t, s.HandleSetTheme, who, "PUT",
			`{"accent":"#d52b1e"}`, "id", cat.ID); rec.Code == http.StatusOK {
			t.Errorf("%v set a catalogue's appearance while enforcement is on", who)
		}
		if rec := asTyped(t, s.HandleSetLogo, who, "PUT", "image/png",
			string(onePixelPNG), "id", cat.ID); rec.Code == http.StatusNoContent {
			t.Errorf("%v uploaded a brand mark while enforcement is on", who)
		}
		if rec := as(t, s.HandleDeleteLogo, who, "DELETE", "", "id", cat.ID); rec.Code == http.StatusNoContent {
			t.Errorf("%v removed a brand mark while enforcement is on", who)
		}
	}
}
