package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A product's description, through the whole stack (#1069).
//
// api/catalog proves the release rule as arithmetic. What it cannot see is whether
// the field survives the wire at all — a product's save is a **full replace**, so
// a description that reaches the store and is dropped on the way back out looks
// exactly like one nobody wrote, and the next save from a screen that read it back
// is what erases it for good.

// TestADescriptionSurvivesTheRoundTrip is that whole concern in one exchange.
func TestADescriptionSurvivesTheRoundTrip(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/catalogs",
		`{"rank":1,"languages":["de","fr"],"texts":{"de":"Arbeitsplatz"}}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}

	save := `{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"draft",` +
		`"texts":{"de":"VPN-Zugang","fr":"Accès VPN"},` +
		`"descriptions":{"de":"Verschlüsselter Zugang ins Firmennetz.","fr":"Accès chiffré au réseau."},` +
		`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/catalog-products", save, "application/json"); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/catalog-products", "", "")
	if code != http.StatusOK {
		t.Fatalf("list products: %d (%s)", code, body)
	}
	var items []struct {
		ID           string            `json:"id"`
		Descriptions map[string]string `json:"descriptions"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode products: %v (%s)", err, body)
	}
	var found bool
	for _, it := range items {
		if it.ID != "vpn" {
			continue
		}
		found = true
		if it.Descriptions["de"] == "" || it.Descriptions["fr"] == "" {
			t.Errorf("the description did not come back: %+v", it.Descriptions)
		}
	}
	if !found {
		t.Fatalf("the product is not in the listing: %s", body)
	}
}

// TestPublishNamesTheLanguageADescriptionIsMissingIn is the rule as a person meets
// it: not a refusal to publish products without descriptions, but a refusal to
// publish one that speaks to half the catalogue's audience.
//
// Through the endpoint rather than the package, because the sentence is what the
// person acts on and it has to arrive where they are looking.
func TestPublishNamesTheLanguageADescriptionIsMissingIn(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/catalogs",
		`{"rank":1,"languages":["de","fr"],"texts":{"de":"Arbeitsplatz"}}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}

	// German only, in a catalogue that promises French too.
	save := `{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active",` +
		`"texts":{"de":"VPN-Zugang","fr":"Accès VPN"},` +
		`"descriptions":{"de":"Verschlüsselter Zugang ins Firmennetz."},` +
		`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/catalog-products", save, "application/json"); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPatch, "/api/v1/catalogs/"+cat.ID,
		`{"items":["vpn"]}`, "application/json"); code != http.StatusOK {
		t.Fatalf("offer the product: %d (%s)", code, b)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/catalogs/"+cat.ID+"/releases", "", "application/json")
	if code == http.StatusOK {
		t.Fatalf("a half-described catalogue published: %s", body)
	}
	if !strings.Contains(string(body), "fr") || !strings.Contains(string(body), "description") {
		t.Errorf("the refusal does not say which language is missing a description: %s", body)
	}
}
