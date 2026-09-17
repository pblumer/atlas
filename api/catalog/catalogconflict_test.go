package catalog

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// A catalogue's patch is partial — the fields it carries are changed and the rest
// are left alone — so it cannot lose a field nobody mentioned, the way a product's
// full replace could (ADR-0376).
//
// What it can still lose is a list. `items`, `edges` and `members` are replaced
// whole when sent, and the surfaces that send them compute the new value from a
// snapshot: adding one product posts every product plus that one. Two maintainers
// adding a product a second apart, and the second write is the first one's
// disappearance — no error, no trace, and the person who lost the change is the one
// who did nothing wrong.
//
// So the same precondition the product write carries, spelled the same way: state
// the revision you read and the write is refused if the catalogue has moved past
// it. Stating it stays optional, because a form whose every field is on the screen
// in front of somebody has no snapshot problem — the caller with a snapshot is the
// caller that should say so.

// patchCatalog sends a partial update and returns the recorder.
func patchCatalog(t *testing.T, s *Service, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, s.HandleUpdateCatalog, "PATCH", "/api/v1/catalogs/x", body, "id", id)
}

// TestACatalogueStartsAtRevisionOne: there is no revision zero to state, so a
// caller always has something to hold on to from the moment it exists.
func TestACatalogueStartsAtRevisionOne(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s), want 201", rec.Code, rec.Body)
	}
	if got := decode[Catalog](t, rec).Revision; got != 1 {
		t.Errorf("a new catalogue is revision %d, want 1", got)
	}
}

// TestPatchingStatingTheRevisionReadSucceedsAndAdvancesIt is the ordinary
// read-modify-write.
func TestPatchingStatingTheRevisionReadSucceedsAndAdvancesIt(t *testing.T) {
	s := newService(t)
	id := homeCatalog(t, s)

	rec := patchCatalog(t, s, id, `{"items":["vpn"],"revision":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch stating revision 1 = %d (%s), want 200", rec.Code, rec.Body)
	}
	got := decode[Catalog](t, rec)
	if got.Revision != 2 {
		t.Errorf("revision = %d after one patch, want 2", got.Revision)
	}
	if len(got.Items) != 1 {
		t.Errorf("items = %v, want the patch to have landed", got.Items)
	}
}

// TestPatchingWithAStaleRevisionIsRefused is the defect: two maintainers add a
// product from the same snapshot, and the second must not erase the first.
func TestPatchingWithAStaleRevisionIsRefused(t *testing.T) {
	s := newService(t)
	id := homeCatalog(t, s)

	// Both read revision 1. The first one's write lands.
	if rec := patchCatalog(t, s, id, `{"items":["vpn"],"revision":1}`); rec.Code != http.StatusOK {
		t.Fatalf("first patch = %d (%s), want 200", rec.Code, rec.Body)
	}
	// The second still holds revision 1, and its list does not carry "vpn".
	rec := patchCatalog(t, s, id, `{"items":["laptop"],"revision":1}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale patch = %d (%s), want 409", rec.Code, rec.Body)
	}

	// The refusal wrote nothing: the first maintainer's product is still there and
	// the second's did not replace it.
	after := decode[Catalog](t, do(t, s.HandleGetCatalog, "GET", "/api/v1/catalogs/x", "", "id", id))
	if len(after.Items) != 1 || after.Items[0] != "vpn" {
		t.Errorf("items = %v, want only the first maintainer's write to have landed", after.Items)
	}
}

// TestPatchingWithoutARevisionIsUnconditional keeps the surfaces that have no
// snapshot working: a form showing every field it sends is looking at what it
// overwrites.
func TestPatchingWithoutARevisionIsUnconditional(t *testing.T) {
	s := newService(t)
	id := homeCatalog(t, s)

	if rec := patchCatalog(t, s, id, `{"rank":7}`); rec.Code != http.StatusOK {
		t.Fatalf("first unconditional patch = %d (%s), want 200", rec.Code, rec.Body)
	}
	rec := patchCatalog(t, s, id, `{"rank":9}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second unconditional patch = %d (%s), want 200", rec.Code, rec.Body)
	}
	if got := decode[Catalog](t, rec).Rank; got != 9 {
		t.Errorf("rank = %d, want the unconditional write to land", got)
	}
}

// TestEveryWriterAdvancesTheRevision is the test that keeps the guard honest.
//
// A precondition is only worth the writers that respect it: one path that changes
// a catalogue without advancing its revision is a path whose changes a stale
// caller silently overwrites, and it would be found by somebody losing work rather
// than by a test. So every writer is named here, and a new one has to be added
// deliberately.
func TestEveryWriterAdvancesTheRevision(t *testing.T) {
	model, err := os.ReadFile("testdata/arbeitsplatz.archimate.xml")
	if err != nil {
		t.Fatalf("read the import fixture: %v", err)
	}

	for name, write := range map[string]func(t *testing.T, s *Service, id string){
		"patch": func(t *testing.T, s *Service, id string) {
			if rec := patchCatalog(t, s, id, `{"rank":4}`); rec.Code != http.StatusOK {
				t.Fatalf("patch = %d (%s), want 200", rec.Code, rec.Body)
			}
		},
		"theme": func(t *testing.T, s *Service, id string) {
			rec := do(t, s.HandleSetTheme, "PUT", "/api/v1/catalogs/x/theme",
				`{"accent":"#336699"}`, "id", id)
			if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
				t.Fatalf("set theme = %d (%s), want 200 or 204", rec.Code, rec.Body)
			}
		},
		"archimate import": func(t *testing.T, s *Service, id string) {
			rec := do(t, s.HandleImport, "POST", "/api/v1/catalogs/x/import",
				string(model), "id", id)
			if rec.Code != http.StatusOK {
				t.Fatalf("import = %d (%s), want 200", rec.Code, rec.Body)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newService(t)
			id := homeCatalog(t, s)
			before := decode[Catalog](t, do(t, s.HandleGetCatalog, "GET", "/api/v1/catalogs/x", "", "id", id)).Revision

			write(t, s, id)

			after := decode[Catalog](t, do(t, s.HandleGetCatalog, "GET", "/api/v1/catalogs/x", "", "id", id)).Revision
			if after <= before {
				t.Errorf("%s left the revision at %d (was %d), so a caller holding the old "+
					"one would overwrite this change without being told", name, after, before)
			}
		})
	}
}
