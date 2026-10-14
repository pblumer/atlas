package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Giving a position back: an order that does not exist or cannot be read, and
// the variables a returned variant hands its process.

// TestOrderReturnRefusals. Nothing is started for an order nobody can find, and
// an unreadable one is a fault rather than an absence.
func TestOrderReturnRefusals(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_nope/lines/vpn/return", nil)
	if code != http.StatusNotFound || !strings.Contains(body, "no order ord_nope") {
		t.Errorf("unknown = %d %s, want 404", code, body)
	}

	orderCancelPathsSave(t, srv, order.Order{ID: "ord_r0", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{{ItemID: "vpn", Status: order.StatusDone, DeprovisionProcess: "d"}}})
	orderCancelPathsCorrupt(t, srv, "ord_r0")
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_r0/lines/vpn/return", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "return line") {
		t.Errorf("unreadable = %d %s, want 500", code, body)
	}
}

// TestOrderReturnAVariantTellsItsProcessWhichOne. The product id says what to
// revoke; the variant says which shape of it was granted. A deprovisioning that
// were not told would have to guess between the black phone and the silver one.
func TestOrderReturnAVariantTellsItsProcessWhichOne(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/deployments",
		strings.NewReader(pendingWorkPathsTaskBPMN("return-phone")))
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_r1", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{{ItemID: "phone", VariantID: "silver", Status: order.StatusDone,
			DeprovisionProcess: "return-phone"}}})

	code, body = recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/orders/ord_r1/lines/phone/return", nil)
	if code != http.StatusOK {
		t.Fatalf("return = %d %s", code, body)
	}

	line := orderCancelPathsGet(t, srv, "ord_r1").Lines[0]
	if line.Status != order.StatusReturning {
		t.Errorf("line status = %s, want returning", line.Status)
	}
	var key uint64
	for _, in := range line.Instances {
		if in.Operation == catalog.OpDeprovision {
			key = in.Key
		}
	}
	if key == 0 {
		t.Fatalf("no deprovisioning instance noted on the line: %+v", line.Instances)
	}
	vars := map[string]string{}
	if err := srv.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.VisibleVariablesOfScope(key, func(v *model.VariableValue) error {
			vars[v.Name] = v.Text
			return nil
		})
	}); err != nil {
		t.Fatalf("read variables: %v", err)
	}
	if vars["variantId"] != "silver" || vars["positionId"] != "phone#silver" || vars["itemId"] != "phone" {
		t.Errorf("variables = %v, want itemId phone, positionId phone#silver and variantId silver", vars)
	}
}
