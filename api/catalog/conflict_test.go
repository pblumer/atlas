package catalog

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Saving a product is a full replace: the record that arrives is the record that
// is stored. That is the right shape for a form, which renders every field and
// sends every field back, and the wrong shape for anything that edits one field
// of a record it read earlier — a second maintainer's change is overwritten with
// no sign that it existed.
//
// The precondition below is what makes the replace safe to hand to a caller that
// is not a form. Stating the revision you read is optional, so the Console — which
// builds its body from form fields and knows no revision — keeps working exactly
// as it did.
//
// The guard counts revisions and does not compare timestamps, and that is not a
// style choice: updatedAt is Unix nanoseconds, past the 2^53 where a float64
// stops representing integers exactly, so every client that decodes JSON numbers
// as doubles would hand back a value a few hundred nanoseconds off and be told
// its own read was stale.

// saveItem posts a product and returns the recorder, so each test reads as the
// sequence of writes it is about.
func saveItem(t *testing.T, s *Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, s.HandleSaveItem, "POST", "/api/v1/catalog-products", body)
}

// homeCatalog creates a catalogue for the products a test writes.
func homeCatalog(t *testing.T, s *Service) string {
	t.Helper()
	rec := do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Standard"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create catalogue = %d (%s), want 201", rec.Code, rec.Body)
	}
	return decode[Catalog](t, rec).ID
}

// TestSavingAProductStatingTheRevisionItReadSucceeds is the ordinary
// read-modify-write: the version matches, so the write goes through.
func TestSavingAProductStatingTheRevisionItReadSucceeds(t *testing.T) {
	s := newService(t)
	home := homeCatalog(t, s)

	first := saveItem(t, s, `{"id":"vpn","homeCatalog":"`+home+`","state":"draft",`+
		`"texts":{"de":"VPN"},"keywords":["fernzugriff"]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first save = %d (%s), want 200", first.Code, first.Body)
	}
	stored := decode[Item](t, first)
	if stored.Revision != 1 {
		t.Fatalf("a newly stored product is revision %d, want 1", stored.Revision)
	}

	second := saveItem(t, s, `{"id":"vpn","homeCatalog":"`+home+`","state":"active",`+
		`"texts":{"de":"VPN"},"keywords":["fernzugriff"],"revision":1}`)
	if second.Code != http.StatusOK {
		t.Fatalf("save stating the revision read = %d (%s), want 200", second.Code, second.Body)
	}
	if got := decode[Item](t, second); got.State != StateActive {
		t.Errorf("state = %q, want active", got.State)
	}
}

// TestSavingAProductWithAStaleRevisionIsRefused is the defect this exists to stop:
// two maintainers read the same product, both write, and the second silently
// discards the first one's change.
func TestSavingAProductWithAStaleRevisionIsRefused(t *testing.T) {
	s := newService(t)
	home := homeCatalog(t, s)

	first := saveItem(t, s, `{"id":"vpn","homeCatalog":"`+home+`","texts":{"de":"VPN"}}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first save = %d (%s), want 200", first.Code, first.Body)
	}
	if read := decode[Item](t, first).Revision; read != 1 {
		t.Fatalf("first save is revision %d, want 1", read)
	}

	// Somebody else writes in between, stating the same revision this caller read.
	between := saveItem(t, s, `{"id":"vpn","homeCatalog":"`+home+`","texts":{"de":"VPN"},`+
		`"keywords":["fernzugriff"],"revision":1}`)
	if between.Code != http.StatusOK {
		t.Fatalf("intervening save = %d (%s), want 200", between.Code, between.Body)
	}

	stale := saveItem(t, s, `{"id":"vpn","homeCatalog":"`+home+`","texts":{"de":"VPN neu"},`+
		`"revision":1}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale save = %d (%s), want 409", stale.Code, stale.Body)
	}

	// The refusal wrote nothing: the intervening keywords are still there, and the
	// stale name never landed.
	list := do(t, s.HandleListItems, "GET", "/api/v1/catalog-products", "")
	items := decode[[]Item](t, list)
	if len(items) != 1 {
		t.Fatalf("stored %d products, want 1", len(items))
	}
	if len(items[0].Keywords) != 1 {
		t.Errorf("keywords = %v, want the intervening write's to survive", items[0].Keywords)
	}
	if items[0].Texts["de"] == "VPN neu" {
		t.Error("the refused write landed anyway")
	}
}

// TestSavingAProductWithoutARevisionReplacesUnconditionally keeps the Console
// working: it builds its body from form fields and states no version.
func TestSavingAProductWithoutARevisionReplacesUnconditionally(t *testing.T) {
	s := newService(t)
	home := homeCatalog(t, s)

	if rec := saveItem(t, s, `{"id":"vpn","homeCatalog":"`+home+`","texts":{"de":"VPN"}}`); rec.Code != http.StatusOK {
		t.Fatalf("create = %d (%s), want 200", rec.Code, rec.Body)
	}
	rec := saveItem(t, s, `{"id":"vpn","homeCatalog":"`+home+`","texts":{"de":"VPN neu"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace without a revision = %d (%s), want 200", rec.Code, rec.Body)
	}
	if got := decode[Item](t, rec); got.Texts["de"] != "VPN neu" {
		t.Errorf("texts = %v, want the unconditional write to land", got.Texts)
	}
}

// TestStatingARevisionOfAProductThatDoesNotExistIsRefused: a caller that states a
// revision means "replace what I read". There is nothing to replace, so the write
// is refused rather than quietly turned into a creation — a product appearing
// under an id whose history the caller misremembered is worse than a refusal.
func TestStatingARevisionOfAProductThatDoesNotExistIsRefused(t *testing.T) {
	s := newService(t)
	home := homeCatalog(t, s)

	rec := saveItem(t, s, `{"id":"ghost","homeCatalog":"`+home+`","texts":{"de":"X"},"revision":1}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stating a revision of a missing product = %d (%s), want 409", rec.Code, rec.Body)
	}
}

// TestAStrangerLearnsNothingFromTheRevisionCheck: the precondition is answered
// only for a caller who may write the product. Answering it earlier would turn
// the check into an oracle — a stranger guessing revisions would learn that a
// product they may not see exists, and which revision it is on.
func TestAStrangerLearnsNothingFromTheRevisionCheck(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))

	if rec := as(t, s.HandleSaveItem, user("usr_a"), "POST", productBody("vpn", cat.ID)); rec.Code != http.StatusOK {
		t.Fatalf("owner save = %d (%s), want 200", rec.Code, rec.Body)
	}

	// A revision that is certainly wrong. The stranger is still told only that the
	// catalogue is not theirs, in the same words as for any other write.
	body := `{"id":"vpn","homeCatalog":"` + cat.ID + `","texts":{"de":"X"},"revision":99}`
	rec := as(t, s.HandleSaveItem, user("usr_stranger"), "POST", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger save = %d (%s), want 404", rec.Code, rec.Body)
	}
}

// TestAnImportedProductCarriesARevision: the import files a product directly
// rather than through the save handler, and an imported product at revision zero
// would leave the very next write — filling in the bindings the import
// deliberately leaves empty — unable to state a precondition.
func TestAnImportedProductCarriesARevision(t *testing.T) {
	s := newService(t)
	home := homeCatalog(t, s)

	model, err := os.ReadFile("testdata/arbeitsplatz.archimate.xml")
	if err != nil {
		t.Fatalf("read the import fixture: %v", err)
	}
	rec := do(t, s.HandleImport, "POST", "/api/v1/catalogs/x/import", string(model), "id", home)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d (%s), want 200", rec.Code, rec.Body)
	}

	items := decode[[]Item](t, do(t, s.HandleListItems, "GET", "/api/v1/catalog-products", ""))
	if len(items) == 0 {
		t.Fatalf("the import stored no products: %s", rec.Body)
	}
	for _, it := range items {
		if it.Revision == 0 {
			t.Errorf("imported product %q is at revision 0, so its first change cannot be guarded", it.ID)
		}
	}
}
