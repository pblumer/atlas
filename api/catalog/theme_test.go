package catalog

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// A theme belongs to a catalogue (ADR-0316). Ten
// catalogues for ten customer groups want ten faces, and ADR-0113's reasoning
// survives that: the brand is an organisation property, and an instance serving
// several customer groups has several.
//
// Only the source accent is stored. The hover, soft and ink shades stay derived
// in theme.js, because eleven copies of that derivation is eleven places for one
// of them to be wrong — and --accent-ink in particular is what keeps text on a
// brand colour legible, whichever colour it is.

func admin() *httpapi.Principal {
	return &httpapi.Principal{UserID: "usr_root", Roles: []string{"admin"}}
}

func TestSettingACatalogueTheme(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	rec := as(t, s.HandleSetTheme, admin(), "PUT",
		`{"accent":"#7A1F3D","typeface":"serif"}`, "id", cat.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("set = %d (%s)", rec.Code, rec.Body)
	}

	got := decode[Catalog](t, as(t, s.HandleGetCatalog, admin(), "GET", "", "id", cat.ID))
	if got.Theme.Accent != "#7a1f3d" {
		t.Errorf("accent = %q, want it canonicalised to lower case", got.Theme.Accent)
	}
	if got.Theme.Typeface != "serif" {
		t.Errorf("typeface = %q, want serif", got.Theme.Typeface)
	}
}

// TestOnlyAnAdministratorSetsTheTheme: decision 12 kept theme maintenance with
// admin, which is the smaller change — the logo upload keeps its gate and no new
// file-validation surface reaches a lesser role.
func TestOnlyAnAdministratorSetsTheTheme(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	// The owner maintains the catalogue and still does not set its face.
	if rec := as(t, s.HandleSetTheme, user("usr_owner"), "PUT",
		`{"accent":"#112233"}`, "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("the owner got %d, want 403", rec.Code)
	}
	// Somebody who cannot see it is told nothing, as everywhere else.
	if rec := as(t, s.HandleSetTheme, user("usr_stranger"), "PUT",
		`{"accent":"#112233"}`, "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("a stranger got %d, want 404", rec.Code)
	}
}

// TestAnUnusableAccentIsRefused: a colour the browser cannot parse would leave
// the page half-branded, with no error anywhere.
func TestAnUnusableAccentIsRefused(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	for _, bad := range []string{`"rot"`, `"#12"`, `"#1122334"`, `"#12z456"`, `"112233"`} {
		rec := as(t, s.HandleSetTheme, admin(), "PUT", `{"accent":`+bad+`}`, "id", cat.ID)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("accent %s = %d, want 400", bad, rec.Code)
		}
	}
	// Three digits is the other legal spelling and must be taken.
	if rec := as(t, s.HandleSetTheme, admin(), "PUT", `{"accent":"#abc"}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Errorf("#abc = %d, want 200", rec.Code)
	}
}

// TestOnlyAShippedTypefaceIsAccepted.
//
// A typeface is a choice from a list rather than a URL on purpose: a web-font URL
// would reach a third party on every portal page load, carrying the visitor's
// address there, on pages that must render when nothing else is reachable.
func TestOnlyAShippedTypefaceIsAccepted(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	if rec := as(t, s.HandleSetTheme, admin(), "PUT",
		`{"typeface":"https://fonts.example.com/x.css"}`, "id", cat.ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("a URL got %d, want 400", rec.Code)
	}
	if rec := as(t, s.HandleSetTheme, admin(), "PUT",
		`{"typeface":"comic"}`, "id", cat.ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown stack got %d, want 400", rec.Code)
	}
	for stack := range Typefaces {
		if rec := as(t, s.HandleSetTheme, admin(), "PUT",
			`{"typeface":"`+stack+`"}`, "id", cat.ID); rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", stack, rec.Code)
		}
	}
}

// TestAnEmptyThemeClearsIt: back to the instance brand, which is what a
// catalogue that never had one shows.
func TestAnEmptyThemeClearsIt(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleSetTheme, admin(), "PUT", `{"accent":"#7a1f3d"}`, "id", cat.ID)

	if rec := as(t, s.HandleSetTheme, admin(), "PUT", `{}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d (%s)", rec.Code, rec.Body)
	}
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, admin(), "GET", "", "id", cat.ID))
	if got.Theme.Accent != "" || got.Theme.Typeface != "" {
		t.Fatalf("theme = %+v, want it cleared", got.Theme)
	}
}

func TestSettingAThemeOnAnUnknownCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	if rec := as(t, s.HandleSetTheme, admin(), "PUT", `{}`, "id", "cat_nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	cat := makeCatalog(t, s, user("usr_owner"))
	if rec := as(t, s.HandleSetTheme, admin(), "PUT", "{not json", "id", cat.ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

// TestEveryTypefaceIsARealStack: a name that maps to nothing would silently
// leave the page in the default face.
func TestEveryTypefaceIsARealStack(t *testing.T) {
	if len(Typefaces) < 2 {
		t.Fatal("offering one typeface is offering none")
	}
	for name, stack := range Typefaces {
		if !strings.Contains(stack, ",") {
			t.Errorf("%q is %q, which names no fallback — a stack of one is a stack "+
				"that fails on the machine without that font", name, stack)
		}
	}
}
