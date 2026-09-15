package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Who may receive what, end to end (ADR-0347).
//
// The gap this closes is one of granularity, not of kind. A catalogue has an
// audience and it is fail-closed — but a person sees exactly **one** catalogue,
// the highest-ranked one their groups reach, so moving a product into a stricter
// catalogue does not restrict it: it hides it behind the shop that person already
// has. A product offered to part of a catalogue's audience could not be expressed
// at all.

// aRestrictedCatalogue publishes a catalogue with one open product and one
// restricted to the named group, and returns the release id.
func aRestrictedCatalogue(t *testing.T, ts *httptest.Server, admin *http.Client,
	groupID string) string {

	t.Helper()
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

	for _, p := range []string{
		`{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"VPN"},"approval":{"kind":"none"},` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
		`{"id":"domain-admin","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"Domänenadministration"},"approval":{"kind":"none"},` +
			`"eligible":["` + groupID + `"],` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", p); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["vpn","domain-admin"]}`); code != http.StatusOK {
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

// aGroup creates a group and returns its id.
func aGroup(t *testing.T, ts *httptest.Server, admin *http.Client, name string) string {
	t.Helper()
	code, b := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"`+name+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group %s: %d (%s)", name, code, b)
	}
	var grp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &grp); err != nil {
		t.Fatalf("decode group: %v (%s)", err, b)
	}
	return grp.ID
}

// TestARestrictedProductIsRefusedForARecipientOutsideItsGroups.
//
// And the open product beside it still goes through, which is the half that
// proves this narrows rather than replaces: a rule that also stopped the
// unrestricted product would be a broken catalogue, not a control.
func TestARestrictedProductIsRefusedForARecipientOutsideItsGroups(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	grp := aGroup(t, ts, admin, "it-betrieb")
	rel := aRestrictedCatalogue(t, ts, admin, grp)

	// root is not in the group, so the restricted product is refused for them.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["domain-admin"]}`)
	if code != http.StatusForbidden {
		t.Fatalf("ordering a restricted product = %d (%s), want 403", code, body)
	}
	if !contains(string(body), "domain-admin") {
		t.Errorf("the refusal does not name the product: %s", body)
	}

	// The unrestricted one in the same catalogue is unaffected.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["vpn"]}`); code != http.StatusCreated {
		t.Fatalf("ordering the open product = %d (%s), want 201 — this narrows an "+
			"audience, it does not replace one", code, b)
	}
}

// TestEligibilityIsAboutTheRecipientAndNotTheOrderer.
//
// The case that decides the whole design. A manager ordering a workplace for a
// new hire is the ordinary way the portal is used, and the person who ends up
// holding the thing is the one the restriction is about. Checking the caller's
// groups would refuse exactly that, and would also let an eligible manager order
// a restricted product *for* somebody who may not have it.
func TestEligibilityIsAboutTheRecipientAndNotTheOrderer(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	grp := aGroup(t, ts, admin, "it-betrieb")
	rel := aRestrictedCatalogue(t, ts, admin, grp)

	// Somebody who is not in the group. root, who is, orders for them.
	code, b := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"neuling","password":"neulingpassword1"}`)
	if code != http.StatusCreated {
		t.Fatalf("create the recipient: %d (%s)", code, b)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &created); err != nil {
		t.Fatalf("decode user: %v (%s)", err, b)
	}
	if code, b := cReq(t, admin, ts, "PUT",
		"/api/v1/groups/"+grp+"/members/"+created.ID, ""); code != http.StatusNoContent &&
		code != http.StatusOK {
		t.Fatalf("add root to the group: %d (%s)", code, b)
	}

	// The recipient is in the group; the orderer's own groups are irrelevant.
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["domain-admin"],"recipient":"`+created.ID+`"}`); code != http.StatusCreated {
		t.Fatalf("ordering for an eligible recipient = %d (%s), want 201", code, body)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
