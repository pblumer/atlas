package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A refused publish has to arrive as its reasons.
//
// Publishing is the moment a catalogue is proved: both graphs acyclic, every
// binding resolved, every product named and bound, ranks unique. The server
// answers 422 with **every** problem at once, each naming the catalogue or item it
// belongs to, and the authoring screen's own opening comment says that list is
// what it renders, because "the problems are the work, and hiding them behind
// 'publish failed' would make the screen useless exactly when it matters".
//
// It did the opposite. The page read `err.message`, which the shared fetch wrapper
// fills from the body's `error` key — a key a 422 does not have — falling back to
// `res.statusText`, which is the empty string over HTTP/2 because HTTP/2 carries
// no reason phrase. So a product manager pressed Publish and got a red card saying
// "Not published. Nothing was frozen" above an empty box, with nothing on screen
// admitting that the reason had been withheld.
//
// This is the wire half: the shape the page reads. The page's half is in
// publishrefusal_internal_test.go, because it is a statement about the source.

// TestARefusedPublishAnswersItsProblemsAndNoErrorKey.
//
// Written against the real response rather than a fixture: if the refusal ever
// grows an `error` key, or renames `problems`, the page quietly goes back to
// showing one line or none, and this is the only place that would notice.
func TestARefusedPublishAnswersItsProblemsAndNoErrorKey(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}

	// A catalogue and one product with no process to revoke it. One problem,
	// named, of the kind somebody actually hits.
	//
	// It used to be a product with a text for only one of two declared languages.
	// That publishes now — the portal falls back to the name the catalogue has, so
	// the missing translation is reported rather than refused — and this test is
	// about the SHAPE of a refusal, not about which rule produced it. So it is a
	// rule that still refuses, and the subject is unchanged.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de","en"],"texts":{"de":"Katalog","en":"Catalogue"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	product := `{"id":"laptop","homeCatalog":"` + cat.ID + `","state":"active",` +
		`"texts":{"de":"Notebook","en":"Notebook"},"approval":{"kind":"none"},` +
		`"provisionProcess":"prov"}`
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["laptop"]}`); code != http.StatusOK {
		t.Fatalf("offer product: %d (%s)", code, b)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("publishing a product with no deprovision process = %d, "+
			"want 422 (%s)", code, body)
	}

	var refusal map[string]any
	if err := json.Unmarshal(body, &refusal); err != nil {
		t.Fatalf("decode refusal: %v (%s)", err, body)
	}
	problems, ok := refusal["problems"].([]any)
	if !ok || len(problems) == 0 {
		t.Fatalf("the refusal carries no \"problems\" list, which is the only thing "+
			"the authoring screen has to render: %s", body)
	}
	if _, has := refusal["error"]; has {
		t.Errorf("the refusal now also carries an \"error\" key. That is not wrong in "+
			"itself, but a page finding a one-line error will stop rendering the list, "+
			"which is the whole reason the list is sent: %s", body)
	}

	first, _ := problems[0].(map[string]any)
	if first["message"] == nil {
		t.Errorf("a problem carries no message; there is nothing to render: %s", body)
	}
	// And it says where. "No text for declared language en" is only actionable if
	// the reader knows which product it is about.
	if first["item"] == nil && first["catalog"] == nil {
		t.Errorf("a problem names neither an item nor a catalogue, so a reader cannot "+
			"tell what to fix: %s", body)
	}
}
