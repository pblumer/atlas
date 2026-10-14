package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A right an order granted, withdrawn in a recertification, goes back through that
// order (ADR-0418).
//
// Before this, the withdrawal started the product's deprovisioning with the
// product and the holder only. A process that finds what it provisioned by the
// order had nothing to look it up by, and it could not report the line returned —
// a line takes "returned" only while it is returning — so the right stayed in the
// inventory, and the next campaign asked about it again.

// aProvisionedOrderedRight places an order for vpn, provisions it and returns the
// order's id: an `ordered` right in the inventory, with its order behind it.
func aProvisionedOrderedRight(t *testing.T, ts *httptest.Server, admin *http.Client) string {
	t.Helper()
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", deprovisionBPMN); code != http.StatusOK {
		t.Fatalf("deploy the revocation: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}
	return orderID
}

// theOrderedRow opens a campaign and returns its id and the row for vpn.
func theOrderedRow(t *testing.T, ts *httptest.Server, admin *http.Client, orderID string) (string, string) {
	t.Helper()
	rep := openCampaign(t, admin, ts, `{"name":"Q3 access review"}`)
	campaign, _ := rep["id"].(string)
	for _, row := range rowsOf(t, rep) {
		if row["itemId"] == "vpn" {
			if row["orderId"] != orderID || row["origin"] != "ordered" {
				t.Fatalf("row = %+v, want the ordered right of order %s", row, orderID)
			}
			id, _ := row["id"].(string)
			return campaign, id
		}
	}
	t.Fatalf("no row for vpn in %+v", rep)
	return "", ""
}

// deprovisioningOf returns the variables the return process started with.
func deprovisioningOf(t *testing.T, ts *httptest.Server, c *http.Client, orderID string) map[string]string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET",
		"/api/v1/instances/search?q="+"orderId%3D"+orderID, "")
	if code != http.StatusOK {
		t.Fatalf("search instances: %d (%s)", code, body)
	}
	var page struct {
		Items []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	for _, i := range page.Items {
		if i.ProcessID != "deprov" {
			continue
		}
		code, raw := cReq(t, c, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", i.Key), "")
		if code != http.StatusOK {
			t.Fatalf("read variables: %d (%s)", code, raw)
		}
		var vars map[string]any
		if err := json.Unmarshal(raw, &vars); err != nil {
			t.Fatalf("decode variables: %v (%s)", err, raw)
		}
		out := map[string]string{}
		for k, v := range vars {
			out[k] = fmt.Sprint(v)
		}
		return out
	}
	t.Fatalf("no deprovisioning started for order %s: %s", orderID, body)
	return nil
}

func TestAWithdrawnOrderedRightGoesBackThroughItsOrder(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	orderID := aProvisionedOrderedRight(t, ts, admin)
	campaign, row := theOrderedRow(t, ts, admin, orderID)

	code, body := cReq(t, admin, ts, "POST",
		"/api/v1/recertification/"+campaign+"/rows/"+row+"/revoke", `{"note":"left the team"}`)
	if code != http.StatusOK {
		t.Fatalf("revoke = %d (%s)", code, body)
	}
	var decided map[string]any
	if err := json.Unmarshal(body, &decided); err != nil {
		t.Fatalf("decode row: %v (%s)", err, body)
	}
	if decided["decision"] != "revoke" || decided["outcome"] != "return-started" {
		t.Errorf("row = %+v, want revoke with outcome return-started", decided)
	}

	// The line is on its way back, as when the orderer gives it back.
	if s := lineStatus(t, ts, admin, orderID, "vpn"); s != "returning" {
		t.Fatalf("line = %q, want returning", s)
	}
	// And the process knows which order and which position it is revoking, which
	// is what it finds the provisioned thing by and reports against.
	vars := deprovisioningOf(t, ts, admin, orderID)
	if vars["orderId"] != orderID || vars["positionId"] != "vpn" || vars["itemId"] != "vpn" {
		t.Errorf("started with %+v, want the order, the position and the product", vars)
	}
	if !strings.Contains(vars["reason"], campaign) {
		t.Errorf("reason = %q, want it to name campaign %s", vars["reason"], campaign)
	}

	// The process reports the line returned, and that ends the right.
	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"returned"}`); code != http.StatusOK {
		t.Fatalf("report returned: %d (%s)", code, b)
	}
	if _, held := inventoryOf(t, ts, admin, ""); len(held.Items) != 0 {
		t.Errorf("inventory = %+v, want the withdrawn right gone", held.Items)
	}
}

func TestAWithdrawalTheOrderRefusesDecidesNothing(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	orderID := aProvisionedOrderedRight(t, ts, admin)
	campaign, row := theOrderedRow(t, ts, admin, orderID)

	// The orderer gives it back first. Two revocations racing against one target
	// system is how a half-deleted account happens, so the withdrawal must not
	// start a second one.
	if code, b := returnLine(t, ts, admin, orderID, "vpn"); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST",
		"/api/v1/recertification/"+campaign+"/rows/"+row+"/revoke", "")
	if code != http.StatusConflict {
		t.Fatalf("revoke = %d (%s), want 409 while the line is already going back", code, body)
	}
	if !strings.Contains(string(body), "already going back") {
		t.Errorf("refusal = %s, want the order's reason", body)
	}
	for _, r := range rowsOf(t, readCampaign(t, admin, ts, campaign, "")) {
		if r["id"] == row && r["decision"] != nil {
			t.Errorf("row = %+v, want it undecided: nothing was carried out", r)
		}
	}
}
