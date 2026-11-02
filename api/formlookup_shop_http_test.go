package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// A directory field in a product's order form (ADR-0473, amended): a person in the
// catalogue's audience searches the directory while filling in the basket, the field
// submits a user principal name, and the person chosen is looked up again when the order
// is placed and when its details are corrected — so a value the search would not have
// offered, typed by hand, is refused at both doors. Graph is the deterministic fake of
// formlookup_http_test.go; nothing here needs a tenant.

// shopLookupWorld is a catalogue whose product "team" asks for its owner through a
// directory field limited to the IT department, released, with anna in its audience and
// mallory outside it, and an Entra Worker answering queries.
type shopLookupWorld struct {
	st                   *lookupStack
	admin, anna, mallory *http.Client
	release              string
}

const shopOwnerForm = `{"type":"default","components":[{"type":"select","id":"Field_besitzer","key":"besitzer",` +
	`"label":"Besitzerin","validate":{"required":true},"atlasDataSource":{"version":1,"workerType":"entra",` +
	`"worker":"ucc_entra","resource":"users","searchFields":["displayName","userPrincipalName"],` +
	`"filters":[{"property":"department","value":"IT"}],` +
	`"option":{"value":"userPrincipalName","label":"displayName","description":"mail"},"pageSize":10,"minSearchLength":2}},` +
	`{"type":"textfield","id":"Field_name","key":"bezeichnung","label":"Bezeichnung"}]}`

func openShopLookupWorld(t *testing.T) *shopLookupWorld {
	t.Helper()
	st := openLookupStack(t, t.TempDir())
	ts := st.ts
	w := &shopLookupWorld{st: st, admin: newClient(t)}
	if login(t, w.admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	users := twoUsers(t, ts, w.admin, "anna", "mallory")
	w.anna, w.mallory = users[0], users[1]

	_, raw := cReq(t, w.admin, ts, "GET", "/api/v1/users", "")
	var listed []struct{ ID, Username string }
	if err := json.Unmarshal(raw, &listed); err != nil {
		var page struct {
			Items []struct{ ID, Username string } `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatalf("decode users: %v (%s)", err, raw)
		}
		listed = page.Items
	}
	annaUser := ""
	for _, u := range listed {
		if u.Username == "anna" {
			annaUser = u.ID
		}
	}
	if annaUser == "" {
		t.Fatalf("anna is not listed: %s", raw)
	}
	_, g := cReq(t, w.admin, ts, "POST", "/api/v1/groups", `{"name":"Besteller"}`)
	gid := decodeGroup(t, g).ID
	if code, b := cReq(t, w.admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+annaUser, ""); code != http.StatusOK {
		t.Fatalf("add anna to the audience: %d %s", code, b)
	}

	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/connectors",
		`{"name":"ucc_entra","kind":"entra","credentialsRef":"entra-ucc","formLookups":true}`); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create the Entra Worker: %d (%s)", code, b)
	}
	body, _ := json.Marshal(map[string]any{"id": "form_team", "name": "Team", "schema": json.RawMessage(shopOwnerForm)})
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/forms", string(body)); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save the order form: %d (%s)", code, b)
	}

	code, raw := cReq(t, w.admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"M365"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d %s", code, raw)
	}
	var cat struct{ ID string }
	_ = json.Unmarshal(raw, &cat)
	product := fmt.Sprintf(`{"id":"team","homeCatalog":%q,"state":"active","texts":{"de":"Team"},`+
		`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d","configForm":"form_team"}`, cat.ID)
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
		t.Fatalf("save the product: %d %s", code, b)
	}
	if code, b := cReq(t, w.admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		fmt.Sprintf(`{"items":["team"],"groups":[%q]}`, gid)); code != http.StatusOK {
		t.Fatalf("shape the catalogue: %d %s", code, b)
	}
	code, raw = cReq(t, w.admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d %s", code, raw)
	}
	var rel struct{ ID string }
	_ = json.Unmarshal(raw, &rel)
	w.release = rel.ID

	code, raw = cReq(t, w.admin, ts, "POST", "/api/v1/api-tokens", `{"name":"entra-worker","scope":"worker","expiresInDays":1}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("mint the worker token: %d (%s)", code, raw)
	}
	var minted struct{ Secret, Token string }
	_ = json.Unmarshal(raw, &minted)
	token := minted.Secret
	if token == "" {
		token = minted.Token
	}
	startEntraWorker(t, st, token, "shop-lookup", newFakeGraph(), "ucc_entra")
	return w
}

func (w *shopLookupWorld) search(t *testing.T, c *http.Client, item, body string) (int, lookupReply, string) {
	t.Helper()
	code, raw := cReq(t, c, w.st.ts, "POST",
		fmt.Sprintf("/api/v1/shop/releases/%s/items/%s/form-data/besitzer/query", w.release, item), body)
	var out lookupReply
	_ = json.Unmarshal(raw, &out)
	return code, out, string(raw)
}

func (w *shopLookupWorld) place(t *testing.T, c *http.Client, owner string) (int, string) {
	t.Helper()
	code, raw := cReq(t, c, w.st.ts, "POST", "/api/v1/orders",
		fmt.Sprintf(`{"releaseId":%q,"items":["team"],"config":{"team":{"besitzer":%q,"bezeichnung":"Projekt"}}}`, w.release, owner))
	return code, string(raw)
}

func TestAnOrderFormSearchesTheDirectoryAndItsChoiceIsCheckedAtEveryDoor(t *testing.T) {
	w := openShopLookupWorld(t)

	// The worker comes up: until then the search answers worker_unavailable.
	deadline := time.Now().Add(20 * time.Second)
	var found lookupReply
	for {
		code, out, raw := w.search(t, w.anna, "team", `{"search":"Anna"}`)
		if code == http.StatusOK {
			found = out
			break
		}
		if out.Code != "worker_unavailable" || time.Now().After(deadline) {
			t.Fatalf("the search never answered: %d %s", code, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The field submits principal names, and only IT is offered: the fixed filter holds
	// whatever the request says.
	if len(found.Items) == 0 || found.Items[0].Value != "anna@example.com" || found.Items[0].Label != "Anna Müller" {
		t.Fatalf("searching Anna offered %+v, want anna@example.com first", found.Items)
	}
	if code, out, _ := w.search(t, w.anna, "team", `{"search":"Beat"}`); code != http.StatusOK || len(out.Items) != 0 {
		t.Fatalf("Beat is in Finance, outside the field's filter: %d %+v", code, out.Items)
	}
	if code, _, raw := w.search(t, w.anna, "team", `{"search":"Anna","filters":{"department":"Finance"}}`); code != http.StatusBadRequest {
		t.Fatalf("a request that tries to move the fixed filter = %d, want 400 (%s)", code, raw)
	}

	// Outside the catalogue's audience there is no search; a product the release does
	// not hold has no form to search.
	if code, out, raw := w.search(t, w.mallory, "team", `{"search":"Anna"}`); code != http.StatusForbidden || out.Code != "forbidden" {
		t.Fatalf("mallory searched an order form she cannot order: %d %s", code, raw)
	}
	if code, out, raw := w.search(t, w.anna, "laptop", `{"search":"Anna"}`); code != http.StatusNotFound || out.Code != "not_found" {
		t.Fatalf("a product the release does not hold = %d %s, want 404", code, raw)
	}

	// Placing: a person outside the filter, typed by hand, is refused; so is something
	// that is no principal name at all; the person the search offers is accepted.
	if code, raw := w.place(t, w.anna, "beat@example.com"); code != http.StatusUnprocessableEntity {
		t.Fatalf("an order naming somebody outside the field's filter = %d, want 422 (%s)", code, raw)
	}
	if code, raw := w.place(t, w.anna, "beat"); code != http.StatusBadRequest {
		t.Fatalf("an order naming no principal name = %d, want 400 (%s)", code, raw)
	}
	code, raw := w.place(t, w.anna, "anna@example.com")
	if code != http.StatusCreated {
		t.Fatalf("an order naming the person the search offered = %d, want 201 (%s)", code, raw)
	}
	var ord struct{ ID string }
	_ = json.Unmarshal([]byte(raw), &ord)

	// Correcting the details meets the same check.
	if code, b := cReq(t, w.anna, w.st.ts, "POST", "/api/v1/orders/"+ord.ID+"/lines/team/details",
		`{"config":{"besitzer":"beat@example.com","bezeichnung":"Projekt"}}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("a correction naming somebody outside the filter = %d, want 422 (%s)", code, b)
	}
	if code, b := cReq(t, w.anna, w.st.ts, "POST", "/api/v1/orders/"+ord.ID+"/lines/team/details",
		`{"config":{"besitzer":"claudia@example.com","bezeichnung":"Projekt"}}`); code != http.StatusOK {
		t.Fatalf("a correction naming another IT person = %d, want 200 (%s)", code, b)
	}
	line := lineIn(t, orderNow(t, w.anna, w.st.ts, ord.ID), "team")
	if cfg := line["config"].(map[string]any); cfg["besitzer"] != "claudia@example.com" {
		t.Fatalf("the corrected owner did not take: %v", cfg)
	}
}
