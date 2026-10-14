package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Two phones in two colours, in one order.
//
// The catalogue's own rule is that the same service pulled in twice in different
// variants is a conflict the orderer resolves (ADR-0312), and keeping both is a
// resolution. It was not expressible: the basket held a set of product ids and the
// request named a variant per product, so the second colour replaced the first.
//
// It is allowed exactly where the catalogue says the product may be held more
// than once. `multipleAllowed` is the existing statement of that, and reusing it
// rather than inventing a second flag keeps one answer to one question.

// twoColourCatalogue publishes a phone that may be held more than once, in four
// colours, and a case that may not.
func twoColourCatalogue(t *testing.T, ts *httptest.Server, admin *http.Client) string {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Mobile Geräte"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	colours := `"variants":[{"id":"black","texts":{"de":"Schwarz"}},` +
		`{"id":"silver","texts":{"de":"Silber"}},` +
		`{"id":"burgundy","texts":{"de":"Burgund"}}],`
	for _, p := range []string{
		`{"id":"phone","homeCatalog":"` + cat.ID + `","state":"active","multipleAllowed":true,` +
			`"texts":{"de":"Telefon"},"approval":{"kind":"none"},` + colours +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
		`{"id":"huelle","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"Hülle"},"approval":{"kind":"none"},` + colours +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", p); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["phone","huelle"]}`); code != http.StatusOK {
		t.Fatalf("offer products: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("decode release: %v (%s)", err, body)
	}
	return rel.ID
}

// TestOneProductInTwoShapesIsTwoPositions.
func TestOneProductInTwoShapesIsTwoPositions(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	rel := twoColourCatalogue(t, ts, admin)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["phone"],"variants":{"phone":["black","silver"]}}`)
	if code != http.StatusCreated {
		t.Fatalf("ordering one product in two shapes = %d (%s), want 201", code, body)
	}
	var ord struct {
		ID    string `json:"id"`
		Lines []struct {
			ItemID    string `json:"itemId"`
			VariantID string `json:"variantId"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v (%s)", err, body)
	}
	if len(ord.Lines) != 2 {
		t.Fatalf("the order carries %d lines, want 2 (%s)", len(ord.Lines), body)
	}
	seen := map[string]bool{}
	for _, l := range ord.Lines {
		seen[l.VariantID] = true
	}
	if !seen["black"] || !seen["silver"] {
		t.Errorf("the two positions are %v, want one black and one silver", seen)
	}

	// And each is addressable on its own, which is the half that makes two
	// positions worth having: an outcome reported for one must not land on the
	// other.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord.ID+"/next", "")
	if code != http.StatusOK {
		t.Fatalf("read the next lines: %d (%s)", code, body)
	}
	var lines []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &lines); err != nil {
		t.Fatalf("decode lines: %v (%s)", err, body)
	}
	ids := map[string]bool{}
	for _, l := range lines {
		ids[l.ID] = true
	}
	for _, want := range []string{"phone#black", "phone#silver"} {
		if !ids[want] {
			t.Errorf("%q is not offered as a position to work on (%v)", want, ids)
		}
	}

	// Naming the product rather than the position is refused, and says why. Taking
	// the first match instead is how a black phone is marked delivered because a
	// silver one was.
	code, body = cReq(t, admin, ts, "POST",
		"/api/v1/orders/"+ord.ID+"/lines/phone", `{"status":"done"}`)
	if code != http.StatusBadRequest {
		t.Errorf("reporting against the product where two positions of it exist = %d "+
			"(%s), want 400", code, body)
	}
	for _, name := range []string{"phone#black", "phone#silver"} {
		if !contains(string(body), name) {
			t.Errorf("the refusal does not name %s, so the caller is not told what to "+
				"say instead: %s", name, body)
		}
	}

	if code, b := cReq(t, admin, ts, "POST",
		"/api/v1/orders/"+ord.ID+"/lines/phone%23silver", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("reporting one of the two = %d (%s), want 200", code, b)
	}
	code, b := cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord.ID, "")
	if code != http.StatusOK {
		t.Fatalf("read the order: %d (%s)", code, b)
	}
	if err := json.Unmarshal(b, &ord); err != nil {
		t.Fatalf("decode order: %v (%s)", err, b)
	}
	for _, l := range ord.Lines {
		if l.VariantID == "black" && !contains(string(b), `"variantId":"black","status":"pending"`) {
			t.Errorf("reporting the silver phone moved the black one too: %s", b)
		}
	}
}

// TestAProductHeldOnlyOnceIsOrderedOnlyOnce.
//
// The limit the orderer asked for, and the one the catalogue already states. A
// product that may be held once is one position whatever shape is named — two
// would be an order that cannot be satisfied and nobody would find out until
// provisioning.
func TestAProductHeldOnlyOnceIsOrderedOnlyOnce(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	rel := twoColourCatalogue(t, ts, admin)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["huelle"],"variants":{"huelle":["black","silver"]}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("ordering two shapes of a product held once = %d (%s), want 400", code, body)
	}
	if !contains(string(body), "huelle") {
		t.Errorf("the refusal does not name the product: %s", body)
	}
	// One shape of it is ordinary.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["huelle"],"variants":{"huelle":["black"]}}`); code != http.StatusCreated {
		t.Fatalf("ordering one shape = %d (%s), want 201", code, b)
	}
}

// TestTheSameShapeTwiceIsOnePosition.
//
// Two identical positions are indistinguishable by their key, so they could not be
// told apart afterwards — and "two black phones" is a quantity, which this
// catalogue does not have. Refused rather than silently merged.
func TestTheSameShapeTwiceIsOnePosition(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	rel := twoColourCatalogue(t, ts, admin)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["phone"],"variants":{"phone":["black","black"]}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("naming one shape twice = %d (%s), want 400", code, body)
	}
}
