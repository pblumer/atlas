package api_test

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A catalogue cannot offer a product nobody has created.
//
// It could, and the consequence arrived a long way from the cause: the write that
// introduced the dangling id answered 200, and the refusal appeared at the next
// publish, as `unknown item <id>` against a catalogue the person had since stopped
// thinking about. Two symptoms of the same fact, with nothing on screen to connect
// them:
//
//   - publishing refuses a catalogue for an id that looks like a product;
//   - the product behind that id reads as `revision: 0`, because a stored product
//     always carries at least revision 1 — the save that creates one sets it — so
//     zero means the record was never written at all.
//
// This is the one invariant the catalogue's own screens already assume: the
// product table renders such a row as "offered but not defined — publishing will
// refuse this". A rule a screen explains and a route does not enforce is a rule
// that holds until somebody uses the API.
//
// Refused at the write and not only at the publish, which is where I5 puts it: the
// publish still proves it, and proving it earlier costs one lookup per id and
// names the mistake while the person still has it in front of them.

// TestACatalogueRefusesToOfferAProductThatDoesNotExist.
func TestACatalogueRefusesToOfferAProductThatDoesNotExist(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Katalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}

	code, body = cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["gibt-es-nicht"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("offering a product that does not exist = %d (%s), want 400", code, body)
	}
	if !contains(string(body), "gibt-es-nicht") {
		t.Errorf("the refusal does not name the id that cannot be offered: %s", body)
	}

	// And the catalogue is unchanged, so a refused write leaves nothing half-done.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/catalogs/"+cat.ID, "")
	if code != http.StatusOK {
		t.Fatalf("read the catalogue: %d (%s)", code, body)
	}
	if contains(string(body), "gibt-es-nicht") {
		t.Errorf("the refused id is in the catalogue anyway: %s", body)
	}
}

// TestACatalogueAlreadyCarryingBadIdsStaysRepairable.
//
// Only what a write *adds* is checked. A catalogue damaged before this rule
// existed has to stay repairable, and the repair is a write of the list without
// the bad id — which its own other damage would refuse if every entry were
// checked. Removing one of two bad ids would then be impossible, and the only way
// out of the mistake would be the mistake.
func TestACatalogueAlreadyCarryingBadIdsStaysRepairable(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Katalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	// Damage it the way a caller before this rule could: straight into the store,
	// which is what that write amounted to.
	danglingOffers(t, dir, cat.ID, "weg-a", "weg-b")

	// Dropping one of the two is allowed, although the other is still bad.
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["weg-b"]}`); code != http.StatusOK {
		t.Fatalf("removing one bad id while another remains = %d (%s), want 200 — "+
			"otherwise the only way out of the mistake is the mistake", code, b)
	}
	// And dropping the last one leaves a clean catalogue.
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":[]}`); code != http.StatusOK {
		t.Fatalf("removing the last bad id = %d (%s), want 200", code, b)
	}
}

// danglingOffers writes an item list straight into the catalogue's record, behind
// the route that now refuses it.
//
// Going round the API is the point: this is what a record written before the rule
// existed looks like, and there is no longer a way to produce one through the
// server. A test that could only stage this damage through a route that refuses it
// could not test the repair at all.
func danglingOffers(t *testing.T, dir, catID string, ids ...string) {
	t.Helper()
	path := filepath.Join(dir, "catalog", "catalogs",
		hex.EncodeToString([]byte(catID))+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the catalogue record: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("decode the catalogue record: %v", err)
	}
	rec["items"] = ids
	next, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encode the catalogue record: %v", err)
	}
	if err := os.WriteFile(path, next, 0o600); err != nil {
		t.Fatalf("write the catalogue record: %v", err)
	}
}

// TestACatalogueStillOffersAProductAnotherCatalogueOwns.
//
// The check is existence and not visibility. A product is referenced by several
// catalogues and edited through exactly one, so refusing an id whose home is
// elsewhere would forbid the sharing the model is built around — and it would
// leak nothing either way, since the caller supplied the id.
func TestACatalogueStillOffersAProductAnotherCatalogueOwns(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	mk := func(rank string) string {
		code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
			`{"rank":`+rank+`,"languages":["de"],"texts":{"de":"K"}}`)
		if code != http.StatusCreated {
			t.Fatalf("create catalogue: %d (%s)", code, body)
		}
		var c struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatalf("decode catalogue: %v (%s)", err, body)
		}
		return c.ID
	}
	home, other := mk("1"), mk("2")

	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"vpn","homeCatalog":"`+home+`","state":"active","texts":{"de":"VPN"},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+other,
		`{"items":["vpn"]}`); code != http.StatusOK {
		t.Fatalf("offering a product owned elsewhere = %d (%s), want 200 — a product is "+
			"referenced by several catalogues and edited through one", code, b)
	}
}
