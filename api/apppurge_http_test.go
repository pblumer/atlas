package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The HTTP side of purging an application
// (ADR-0468): the default delete keeps
// its contents as before, a purge removes them — Panorama and information models
// included — and the preview and the purge answer who may cancel instances.

// TestPlainDeleteStillKeepsTheContents: without purge an application's draft
// survives it and reads as Not assigned (ADR-0034), and a malformed flag is refused
// rather than read as either answer.
func TestPlainDeleteStillKeepsTheContents(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Plain")
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("plain-proc"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodDelete, "/api/v1/applications/"+app+"?purge=maybe", "", ""); code != http.StatusBadRequest {
		t.Fatalf("purge=maybe: %d %s, want 400", code, b)
	}
	if code, b := doReq(t, ts, http.MethodDelete, "/api/v1/applications/"+app, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodGet, "/api/v1/drafts/plain-proc/xml", "", ""); code != http.StatusOK {
		t.Fatalf("the draft did not survive a plain delete: %d %s", code, b)
	}
}

// TestPurgeRemovesPanoramaAndInformationModels: the models a plain delete refuses to
// orphan (Panorama) or leaves behind (information models) go with a purge.
func TestPurgeRemovesPanoramaAndInformationModels(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Architecture")
	payload, _ := json.Marshal(map[string]any{
		"applicationId": app, "name": "Landscape", "notation": "archimate-3.2", "xml": panoramaExchangeXML,
	})
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", string(payload), "application/json"); code != http.StatusCreated {
		t.Fatalf("panorama model: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/infomodel/models",
		fmt.Sprintf(`{"applicationId":%q,"name":"Sales data"}`, app), "application/json"); code != http.StatusCreated {
		t.Fatalf("information model: %d %s", code, b)
	}

	code, b := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+app+"/deletion-preview", "", "")
	if code != http.StatusOK || !strings.Contains(string(b), `"name":"Landscape"`) || !strings.Contains(string(b), `"name":"Sales data"`) {
		t.Fatalf("preview: %d %s, want both models listed", code, b)
	}
	if code, b := doReq(t, ts, http.MethodDelete, "/api/v1/applications/"+app+"?purge=true", "", ""); code != http.StatusNoContent {
		t.Fatalf("purge: %d %s", code, b)
	}
	if _, b := doReq(t, ts, http.MethodGet, "/api/v1/panorama/models", "", ""); strings.Contains(string(b), "Landscape") {
		t.Fatalf("the Panorama model survived the purge: %s", b)
	}
	if _, b := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/models", "", ""); strings.Contains(string(b), "Sales data") {
		t.Fatalf("the information model survived the purge: %s", b)
	}
}

// TestPurgePreviewNamesTheCatalogueProducts: the reported case seen from the
// application — its process is bound by a published catalogue product, and the
// preview says which product will no longer be able to start it.
func TestPurgePreviewNamesTheCatalogueProducts(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/applications", `{"name":"Telefonie"}`)
	if code != http.StatusOK {
		t.Fatalf("application: %d %s", code, body)
	}
	app := idOf(t, body)
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/drafts?projectId="+app, "application/xml", plainProcess("zentrale")); code != http.StatusOK {
		t.Fatalf("draft: %d %s", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/applications/"+app+"/publish", "{}"); code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	// A catalogue never published binds nothing an order could freeze.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":2,"languages":["de"],"texts":{"de":"B"}}`); code != http.StatusCreated {
		t.Fatalf("second catalogue: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"zentrale","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Zentrale"},`+
			`"approval":{"kind":"none"},"provisionProcess":"zentrale","deprovisionProcess":"zentrale"}`); code != http.StatusOK {
		t.Fatalf("product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["zentrale"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	// Published twice: only the newest release says what an order placed now freezes.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", ""); code != http.StatusCreated {
		t.Fatalf("first release: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("release: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	// A line that was provisioned can still be returned, which starts the same
	// process (ADR-0427): the preview counts it.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+rel+`","items":["zentrale"]}`); code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, b)
	}

	code, body = cReq(t, admin, ts, "GET", "/api/v1/applications/"+app+"/deletion-preview", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"catalogueProducts":[{"processId":"zentrale","products":["zentrale"]}]`) {
		t.Fatalf("preview: %d %s, want the product named", code, body)
	}
	if !strings.Contains(string(body), `"orderLines":[{"processId":"zentrale","lines":1`) {
		t.Fatalf("preview: %s, want the order line counted", body)
	}
	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/applications/"+app+"?purge=true", ""); code != http.StatusNoContent {
		t.Fatalf("purge: %d %s", code, b)
	}
	if _, b := cReq(t, admin, ts, "GET", "/api/v1/processes", ""); strings.Contains(string(b), `"processId":"zentrale"`) {
		t.Fatalf("the bound process survived the purge: %s", b)
	}
}

// TestPurgeNeedsOwnerAndOperator: only the application's owner may preview or purge
// it, and an owner who may not cancel instances is told so by the preview and
// refused by the purge while instances run.
func TestPurgeNeedsOwnerAndOperator(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	createUserWithRoles(t, admin, ts.URL, "mod", `["modeler"]`)
	createUserWithRoles(t, admin, ts.URL, "other", `["modeler","operator"]`)
	mod := signInAs(t, ts.URL, "mod", "a-password-that-is-long")
	other := signInAs(t, ts.URL, "other", "a-password-that-is-long")

	code, body := cReq(t, mod, ts, "POST", "/api/v1/applications", `{"name":"Busy"}`)
	if code != http.StatusOK {
		t.Fatalf("application: %d %s", code, body)
	}
	app := idOf(t, body)
	if code, b := cReqTyped(t, mod, ts, "POST", "/api/v1/drafts?projectId="+app, "application/xml",
		strings.Replace(sampleBPMN, `id="order"`, `id="busy"`, 1)); code != http.StatusOK {
		t.Fatalf("draft: %d %s", code, b)
	}
	code, body = cReq(t, mod, ts, "POST", "/api/v1/applications/"+app+"/publish", "{}")
	if code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, body)
	}
	var pub struct {
		Definitions []struct {
			Key uint64 `json:"key"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(body, &pub); err != nil || len(pub.Definitions) != 1 {
		t.Fatalf("decode publish: %v (%s)", err, body)
	}
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", pub.Definitions[0].Key), "{}"); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, b)
	}

	if code, b := cReq(t, other, ts, "GET", "/api/v1/applications/"+app+"/deletion-preview", ""); code != http.StatusNotFound {
		t.Fatalf("preview by a stranger: %d %s, want 404", code, b)
	}
	if code, b := cReq(t, other, ts, "DELETE", "/api/v1/applications/"+app+"?purge=true", ""); code != http.StatusNotFound {
		t.Fatalf("purge by a stranger: %d %s, want 404", code, b)
	}
	code, body = cReq(t, mod, ts, "GET", "/api/v1/applications/"+app+"/deletion-preview", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"needsOperator":true`) {
		t.Fatalf("owner's preview: %d %s, want needsOperator", code, body)
	}
	code, body = cReq(t, mod, ts, "DELETE", "/api/v1/applications/"+app+"?purge=true", "")
	if code != http.StatusForbidden || !strings.Contains(string(body), "operator") {
		t.Fatalf("owner without operator purging: %d %s, want 403 naming the operator role", code, body)
	}
	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/applications/"+app+"?purge=true", ""); code != http.StatusNoContent {
		t.Fatalf("admin purging: %d %s, want 204", code, b)
	}
}
