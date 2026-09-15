package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Reading the catalogue graph backwards, over HTTP (ADR-0353).
//
// The walk itself has its own tests against a release built in memory. This is
// the route: what it answers about a real published catalogue, and what it
// answers about a product that is not in one.

type productUsageResp struct {
	ItemID    string   `json:"itemId"`
	OfferedBy []string `json:"offeredBy"`
	PartOf    []struct {
		ItemID    string `json:"itemId"`
		Kind      string `json:"kind"`
		CatalogID string `json:"catalogId"`
	} `json:"partOf"`
	Needs        []string `json:"needs"`
	NeededBy     []string `json:"neededBy"`
	ExcludedWith []string `json:"excludedWith"`
	Held         struct {
		Total    int            `json:"total"`
		ByOrigin map[string]int `json:"byOrigin"`
	} `json:"held"`
	Note string `json:"note"`
}

// TestUsageCountsHoldersAndNeverNamesThem.
//
// The count is the whole disclosure decision. A maintainer does not need to know
// who Ada is to know that somebody would be affected — and a list of the people
// holding one service is the inventory filtered to the interesting part, which is
// what the inventory routes are gated for.
func TestUsageCountsHoldersAndNeverNamesThem(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	catID, orderID := aCatalogueWithAnOrder(t, ts, admin)

	code, body := cReq(t, admin, ts, "GET", "/api/v1/catalog-products/vpn/usage", "")
	if code != http.StatusOK {
		t.Fatalf("usage = %d (%s)", code, body)
	}
	var before productUsageResp
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(before.OfferedBy) != 1 || before.OfferedBy[0] != catID {
		t.Errorf("offeredBy = %v, want the catalogue that carries it", before.OfferedBy)
	}
	if before.Held.Total != 0 {
		t.Errorf("held = %d before anything is provisioned", before.Held.Total)
	}
	// Nothing carries it and nobody holds it: the note has to say that rather than
	// leaving an empty report to read as "safe to retire".
	if before.Note == "" {
		t.Error("an empty report carries no note, so a maintainer cannot tell " +
			"\"nothing uses this\" from \"this was never published\"")
	}

	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("provision: %d (%s)", code, b)
	}

	_, body = cReq(t, admin, ts, "GET", "/api/v1/catalog-products/vpn/usage", "")
	var after productUsageResp
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if after.Held.Total != 1 {
		t.Fatalf("held = %d after one provisioning, want 1", after.Held.Total)
	}
	if after.Held.ByOrigin["ordered"] != 1 {
		t.Errorf("byOrigin = %v, want the hold counted as ordered — the origin decides "+
			"what a maintainer can do about it", after.Held.ByOrigin)
	}
	if after.Note != "" {
		t.Errorf("a product somebody holds still carries the empty-report note: %q", after.Note)
	}
	// And no name anywhere in the answer.
	if contains(string(body), "root") {
		t.Errorf("the answer names a holder: %s", body)
	}
}

// TestAnUnknownProductIsNotAnEmptyReport.
//
// "Nothing uses this" and "this does not exist" are different answers, and an
// empty report reads as *safe to retire* — precisely the wrong thing to tell
// somebody who mistyped an id.
func TestAnUnknownProductIsNotAnEmptyReport(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	aCatalogueWithAnOrder(t, ts, admin)

	code, body := cReq(t, admin, ts, "GET", "/api/v1/catalog-products/gibt-es-nicht/usage", "")
	if code != http.StatusNotFound {
		t.Fatalf("an unknown product = %d (%s), want 404", code, body)
	}
	if !contains(string(body), "gibt-es-nicht") {
		t.Errorf("the refusal does not name what was asked for: %s", body)
	}
}
