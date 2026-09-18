package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api"
)

// An approval of a product ordered twice is still an approval.
//
// What makes a task an approval is the order behind it: the instance carries an
// order id and names a line, the order has that line, and the line says this
// process decides it (ADR-0311). Naming the
// line is what ADR-0384 changed — a
// process now passes positionId beside itemId, because "phone" is two lines when
// somebody ordered a black one and a silver one.
//
// The reader was written for that and never received it: positionId was read out of
// a map that collected every variable *except* positionId. So the lookup fell back
// to the product, the product named two lines, and resolving refused to guess —
// which is right, and here meant the approval was not recognised as one at all. It
// vanished from the approver's inbox, from their page, and from the order's own
// view of where it stands.

// TestAnApprovalOfOneOfTwoShapesIsFound.
func TestAnApprovalOfOneOfTwoShapesIsFound(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]

	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Mobile Geräte"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"phone","homeCatalog":"`+cat+`","state":"active","multipleAllowed":true,`+
			`"texts":{"de":"Telefon"},"approval":{"kind":"fixed","ref":"alice"},`+
			`"variants":[{"id":"black","texts":{"de":"Schwarz"}},`+
			`{"id":"silver","texts":{"de":"Silber"}}],`+
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"items":["phone"]}`); code != http.StatusOK {
		t.Fatalf("offer the product: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)

	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["phone"],"variants":{"phone":["black","silver"]}}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	ord := idOf(t, body)

	// The approval of one of the two, started the way fulfilment starts it: the
	// product in itemId, and the position in positionId.
	start := `{"processId":"atlas-genehmigung-fix","variables":{
		"orderId":"` + ord + `","itemId":"phone","positionId":"phone#black",
		"variantId":"black","approvalRef":"alice","provisionProcess":"prov",
		"recipient":"root","orderer":"root"}}`
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
		t.Fatalf("start the approval: %d (%s)", code, b)
	}

	code, body = cReq(t, alice, ts, "GET", "/api/v1/approvals", "")
	if code != http.StatusOK {
		t.Fatalf("list approvals: %d (%s)", code, body)
	}
	var approvals []struct {
		OrderID    string `json:"orderId"`
		ItemID     string `json:"itemId"`
		PositionID string `json:"positionId"`
		VariantID  string `json:"variantId"`
	}
	if err := json.Unmarshal(listRows(t, body), &approvals); err != nil {
		t.Fatalf("decode approvals: %v (%s)", err, body)
	}
	if len(approvals) != 1 {
		t.Fatalf("the approver holds %d approvals, want 1 — an approval whose product "+
			"the order carries twice is still an approval (%s)", len(approvals), body)
	}
	got := approvals[0]
	if got.PositionID != "phone#black" {
		t.Errorf("the approval decides position %q, want phone#black — an approver "+
			"deciding the wrong one of two phones decides somebody else's", got.PositionID)
	}
	if got.OrderID != ord || got.ItemID != "phone" || got.VariantID != "black" {
		t.Errorf("the approval reads %+v, want order %s, phone, black", got, ord)
	}
}
