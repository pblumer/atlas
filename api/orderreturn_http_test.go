package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Giving back what an order granted, end to end. The deprovisioning process has
// travelled on every order line since the day orders existed; nothing ever ran it.

// deprovisionBPMN is an installation's own revocation: it parks on nothing and
// reports the line back, which is what a real one does after it has talked to
// whatever system holds the access.
const deprovisionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="deprov" isExecutable="true">
    <startEvent id="start"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </process>
</definitions>`

func returnLine(t *testing.T, ts *httptest.Server, c *http.Client, id, item string) (int, []byte) {
	t.Helper()
	return cReq(t, c, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/%s/return", id, item), "")
}

func lineStatus(t *testing.T, ts *httptest.Server, c *http.Client, id, item string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/orders/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("read the order: %d (%s)", code, body)
	}
	var o struct {
		Lines []struct {
			ItemID string `json:"itemId"`
			Status string `json:"status"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &o); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	for _, l := range o.Lines {
		if l.ItemID == item {
			return l.Status
		}
	}
	t.Fatalf("no line %s in %s", item, body)
	return ""
}

func TestAProvisionedLineCanBeGivenBack(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", deprovisionBPMN); code != http.StatusOK {
		t.Fatalf("deploy the revocation: %d (%s)", code, b)
	}

	// Nothing to give back while nothing is held.
	if code, _ := returnLine(t, ts, admin, orderID, "vpn"); code != http.StatusConflict {
		t.Errorf("returning an unprovisioned line = %d, want 409", code)
	}

	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}

	code, body := returnLine(t, ts, admin, orderID, "vpn")
	if code != http.StatusOK {
		t.Fatalf("return = %d (%s)", code, body)
	}
	var got struct {
		Process string `json:"process"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	// The process the *order* froze, not whatever the catalogue says today.
	if got.Process != "deprov" {
		t.Errorf("process = %q, want the one the order carries", got.Process)
	}
	if s := lineStatus(t, ts, admin, orderID, "vpn"); s != "returning" {
		t.Fatalf("line = %q, want returning while the revocation runs", s)
	}

	// The revocation reports back the way a provisioning does.
	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"returned"}`); code != http.StatusOK {
		t.Fatalf("report returned: %d (%s)", code, b)
	}
	if s := lineStatus(t, ts, admin, orderID, "vpn"); s != "returned" {
		t.Errorf("line = %q, want returned", s)
	}
	// And what was granted and then revoked is not what was never granted.
	if s := lineStatus(t, ts, admin, orderID, "vpn"); s == "cancelled" {
		t.Error("a revoked grant reads as a cancellation; the record lost the weeks somebody held it")
	}
}

// TestAReturnIsRefusedFromUnderSomethingThatNeedsIt: the precedence graph read
// backwards, through the API.
func TestAReturnIsRefusedFromUnderSomethingThatNeedsIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	orderID := aWorkplaceOrder(t, ts, admin)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", deprovisionBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	for _, item := range []string{"account", "laptop"} {
		if code, b := cReq(t, admin, ts, "POST",
			fmt.Sprintf("/api/v1/orders/%s/lines/%s", orderID, item), `{"status":"done"}`); code != http.StatusOK {
			t.Fatalf("provision %s: %d (%s)", item, code, b)
		}
	}

	code, body := returnLine(t, ts, admin, orderID, "account")
	if code != http.StatusConflict {
		t.Fatalf("returning the account = %d, want 409 — the laptop still uses it", code)
	}
	if !strings.Contains(string(body), "laptop") {
		t.Errorf("the refusal does not name what is in the way: %s", body)
	}

	// The laptop first.
	if code, b := returnLine(t, ts, admin, orderID, "laptop"); code != http.StatusOK {
		t.Fatalf("returning the laptop: %d (%s)", code, b)
	}

	// And the account still waits, because a revocation that has been *asked for*
	// has not happened: until the laptop's return confirms, the laptop is there.
	if code, _ := returnLine(t, ts, admin, orderID, "account"); code != http.StatusConflict {
		t.Errorf("the account went while the laptop's own return was only under way: %d", code)
	}

	// Confirmed gone, and now it may follow.
	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/laptop", orderID), `{"status":"returned"}`); code != http.StatusOK {
		t.Fatalf("report the laptop returned: %d (%s)", code, b)
	}
	if code, b := returnLine(t, ts, admin, orderID, "account"); code != http.StatusOK {
		t.Fatalf("returning the account once the laptop is confirmed gone: %d (%s)", code, b)
	}
}

// TestAReturnIsTheOrderersToAskFor, like the withdrawal it sits beside.
func TestAReturnIsTheOrderersToAskFor(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	stranger := twoUsers(t, ts, admin, "mallory")[0]
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)

	if code, _ := returnLine(t, ts, stranger, orderID, "vpn"); code != http.StatusNotFound {
		t.Errorf("a stranger's return = %d, want 404", code)
	}
}

// aWorkplaceOrder builds an order whose laptop needs its account, both ordered.
// It exists for the precedence case: one line that has to go back before another
// can.
func aWorkplaceOrder(t *testing.T, ts *httptest.Server, admin *http.Client) string {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v", err)
	}
	for _, id := range []string{"account", "laptop"} {
		product := `{"id":"` + id + `","homeCatalog":"` + cat.ID + `","state":"active",` +
			`"texts":{"de":"` + id + `"},"approval":{"kind":"none"},` +
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
			t.Fatalf("save %s: %d (%s)", id, code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["account","laptop"],"edges":[{"from":"laptop","to":"account","kind":"requires"}]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel.ID+`","items":["account","laptop"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place order: %d (%s)", code, body)
	}
	var ord struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	return ord.ID
}
