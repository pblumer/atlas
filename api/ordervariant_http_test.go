package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The variant the orderer chose, end to end.
//
// A variant is one orderable shape of a product — a colour, a licence tier — and
// the catalogue has carried them since it was built. Nothing ever wrote one down.
// The order line had the field, the fulfilment process passed `position.variantId`
// to provisioning, and it arrived empty for every order ever placed: the basket
// never asked, and `POST /api/v1/orders` had nowhere to put the answer.
//
// So provisioning was told "give this person an iPhone" and not which one. That is
// not a missing convenience — it is the order failing to record what was ordered.

// aVariantCatalogue publishes a bundle whose integral part has four variants, and
// returns the release id. It mirrors the shape the defect was found in: the
// product carrying the variants is reached through a composition, so it is never
// named by the orderer directly.
func aVariantCatalogue(t *testing.T, ts *httptest.Server, admin *http.Client) string {
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

	for _, p := range []string{
		`{"id":"paket","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"Paket"},"approval":{"kind":"none"},` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
		`{"id":"phone","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"Telefon"},"approval":{"kind":"none"},` +
			`"variants":[{"id":"black","texts":{"de":"Schwarz"}},` +
			`{"id":"silver","texts":{"de":"Silber"}},` +
			`{"id":"glacier","texts":{"de":"Gletscher"}},` +
			`{"id":"burgundy","texts":{"de":"Burgund"}}],` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
		`{"id":"huelle","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"Hülle"},"approval":{"kind":"none"},` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", p); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["paket","phone","huelle"],"edges":[`+
			`{"from":"paket","to":"phone","kind":"composition"},`+
			`{"from":"paket","to":"huelle","kind":"aggregation"}]}`); code != http.StatusOK {
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

// variantOfLine reads what /next says about one line.
func variantOfLine(t *testing.T, ts *httptest.Server, c *http.Client, order, item string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/orders/"+order+"/next", "")
	if code != http.StatusOK {
		t.Fatalf("read the next lines: %d (%s)", code, body)
	}
	var lines []struct {
		ItemID    string `json:"itemId"`
		VariantID string `json:"variantId"`
	}
	if err := json.Unmarshal(body, &lines); err != nil {
		t.Fatalf("decode lines: %v (%s)", err, body)
	}
	for _, l := range lines {
		if l.ItemID == item {
			return l.VariantID
		}
	}
	t.Fatalf("no line for %s in %s", item, body)
	return ""
}

// TestTheOrderRecordsTheVariantThatWasChosen.
//
// The whole point: what provisioning is handed has to say which shape of the
// product was asked for.
func TestTheOrderRecordsTheVariantThatWasChosen(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	rel := aVariantCatalogue(t, ts, admin)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["paket"],"variants":{"phone":"burgundy"}}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order = %d (%s), want 201", code, body)
	}
	var ord struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v (%s)", err, body)
	}
	if got := variantOfLine(t, ts, admin, ord.ID, "phone"); got != "burgundy" {
		t.Errorf("the line for the phone carries variantId %q, want \"burgundy\" — "+
			"provisioning is told to hand over a phone and not which one", got)
	}
}

// TestAProductWithVariantsIsNotOrderableWithoutOne.
//
// Refused at the order and not only hidden in the basket, because the basket is
// one caller of a route that anybody may call. A rule the UI keeps and the server
// does not is not a rule.
func TestAProductWithVariantsIsNotOrderableWithoutOne(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	rel := aVariantCatalogue(t, ts, admin)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["paket"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("ordering a product with variants and naming none = %d (%s), want 400",
			code, body)
	}
	if !contains(string(body), "phone") {
		t.Errorf("the refusal does not name the product that is missing a variant: %s", body)
	}
}

// TestAVariantTheProductDoesNotOfferIsRefused.
//
// And the answer has to be one of the product's own. A stored variantId nothing
// in the catalogue matches would reach provisioning as a string that looks right
// and names nothing.
func TestAVariantTheProductDoesNotOfferIsRefused(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	rel := aVariantCatalogue(t, ts, admin)

	for _, bad := range []struct{ name, body string }{
		{"a variant the product does not have",
			`{"releaseId":"` + rel + `","items":["paket"],"variants":{"phone":"pink"}}`},
		{"a variant for a product that has none",
			`{"releaseId":"` + rel + `","items":["paket"],"variants":{"phone":"black","huelle":"black"}}`},
	} {
		code, body := cReq(t, admin, ts, "POST", "/api/v1/orders", bad.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s = %d (%s), want 400", bad.name, code, body)
		}
	}
}
