package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A structured catalogue over the real routes (ADR-0524):
// the switch is a catalogue patch, its headings are held to the capability register
// at publish and worded from it, and an order cannot ask for a service on its own.

func TestAStructuredCatalogueIsHeldToTheRegisterAndToItsMarktleistungen(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","displayName":"Alice Beispiel"}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice: %d (%s)", code, body)
	}
	alice := idOf(t, body)

	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de","en"],"texts":{"de":"Katalog","en":"Catalogue"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)

	product := func(id, kind, extra string) string {
		return `{"id":"` + id + `","homeCatalog":"` + cat + `","state":"active","kind":"` + kind +
			`","texts":{"de":"` + id + `","en":"` + id + `"},"approval":{"kind":"none"},` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"` + extra + `}`
	}
	for _, p := range []string{
		product("laptop", "offering", `,"category":"arbeitsalltag-bewaeltigen","productGroup":"endgeraet-bereitstellen"`),
		product("laptop-hw", "service", ""),
		product("huelle", "service", ""),
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", p); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"structured":true,"items":["laptop","laptop-hw","huelle"],"edges":[`+
			`{"from":"laptop","to":"laptop-hw","kind":"composition"},`+
			`{"from":"laptop","to":"huelle","kind":"aggregation"}]}`); code != http.StatusOK {
		t.Fatalf("structure the catalogue: %d (%s)", code, b)
	} else if !strings.Contains(string(b), `"structured":true`) {
		t.Fatalf("the switch did not stick: %s", b)
	}

	// The register is empty, so both headings name nothing.
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), "which is no capability in the register") {
		t.Fatalf("publish against an empty register = %d (%s), want 422 naming the missing capability", code, body)
	}

	for _, c := range []string{
		`{"key":"endgeraet-bereitstellen","name":"Endgeräte"}`,
		`{"key":"arbeitsalltag-bewaeltigen","name":"Arbeitsalltag bewältigen","requires":["endgeraet-bereitstellen"]}`,
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/capabilities", c); code != http.StatusCreated {
			t.Fatalf("create capability: %d (%s)", code, b)
		}
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct {
		ID         string `json:"id"`
		Structured bool   `json:"structured"`
		Items      []struct {
			ID                string            `json:"id"`
			CategoryTexts     map[string]string `json:"categoryTexts"`
			ProductGroupTexts map[string]string `json:"productGroupTexts"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("decode release: %v (%s)", err, body)
	}
	if !rel.Structured {
		t.Fatalf("the release does not say it is structured: %s", body)
	}
	for _, it := range rel.Items {
		if it.ID != "laptop" {
			continue
		}
		if it.CategoryTexts["en"] != "Arbeitsalltag bewältigen" || it.ProductGroupTexts["de"] != "Endgeräte" {
			t.Fatalf("headings not worded from the register: %+v", it)
		}
	}

	order := func(items string) (int, []byte) {
		return cReq(t, admin, ts, "POST", "/api/v1/orders",
			`{"releaseId":"`+rel.ID+`","items":[`+items+`],"recipient":"`+alice+`"}`)
	}
	if code, b := order(`"huelle"`); code != http.StatusBadRequest ||
		!strings.Contains(string(b), "only as part of a Marktleistung") {
		t.Fatalf("ordering a service alone = %d (%s), want 400", code, b)
	}
	if code, b := order(`"laptop","huelle"`); code != http.StatusCreated {
		t.Fatalf("ordering the Marktleistung with its option = %d (%s)", code, b)
	}
}
