package api

import (
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/order"
)

// Starting one position of an order when it may not start yet, when the order
// cannot be read, and when the start succeeds but the order cannot record it.

// TestOrderStartAPositionWaitsForItsPrecondition. The line is found past the one
// before it, and is refused while what it needs is not provisioned: starting it
// anyway would provision a VPN profile for a laptop that does not exist yet.
func TestOrderStartAPositionWaitsForItsPrecondition(t *testing.T) {
	srv := newServerForErrors(t)
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_s1", Orderer: "usr_ada", Recipient: "usr_ada",
		Requires: map[string][]string{"vpn": {"laptop"}},
		Lines: []order.Line{
			{ItemID: "laptop", Status: order.StatusPending, ProvisionProcess: "p"},
			{ItemID: "vpn", Status: order.StatusPending, ProvisionProcess: "p"},
		}})

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/orders/ord_s1/lines/vpn/start", nil)
	if code != http.StatusConflict || !strings.Contains(body, "waits on a precondition") {
		t.Errorf("start = %d %s, want 409", code, body)
	}
	if got := orderCancelPathsGet(t, srv, "ord_s1").Lines[1]; got.Status != order.StatusPending || len(got.Instances) != 0 {
		t.Errorf("line = %+v, want it still waiting with nothing started", got)
	}
}

// TestOrderStartAnUnreadableOrderIsAFault, not "no order with that line".
func TestOrderStartAnUnreadableOrderIsAFault(t *testing.T) {
	srv := newServerForErrors(t)
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_s2"))
	orderCancelPathsCorrupt(t, srv, "ord_s2")

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/orders/ord_s2/lines/vpn/start", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "start line") {
		t.Errorf("start = %d %s, want 500", code, body)
	}
}

// TestOrderStartAStartTheOrderCannotRecordIsSaid. The process is started first and
// the line marked running after; when that second write fails the caller is told
// that the provisioning did start, so nobody starts it a second time believing the
// first never happened.
func TestOrderStartAStartTheOrderCannotRecordIsSaid(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/deployments",
		strings.NewReader(pendingWorkPathsTaskBPMN("provision-vpn")))
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_s3", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{{ItemID: "vpn", Status: order.StatusPending, ProvisionProcess: "provision-vpn"}}})
	reconcileApplyPathsBlockSave(t, filepath.Join(srv.dataDir, "orders", hex.EncodeToString([]byte("ord_s3"))+".json"))

	code, body = recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/orders/ord_s3/lines/vpn/start", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "the provisioning was started") ||
		!strings.Contains(body, "could not be marked running") {
		t.Errorf("start = %d %s, want 500 saying the provisioning started", code, body)
	}
	if got := orderCancelPathsGet(t, srv, "ord_s3").Lines[0].Status; got != order.StatusPending {
		t.Errorf("line status = %s, want the order record unchanged", got)
	}
}
