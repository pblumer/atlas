package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
)

// Changing a held right (ADR-0428, now the action act of ADR-0429) when there is nothing that could take the
// change: a body that cannot be read, an order record that cannot, a position
// that is not held yet, and a held position no instance carries. None of them
// starts or delivers anything.

// orderChangePathsLine is a per-position line whose lifecycle process binds a
// change operation.
func orderChangePathsLine(status order.LineStatus) order.Line {
	return order.Line{ItemID: "vpn", Status: status, LifecycleProcess: "vpn-lifecycle",
		LifecycleForm: catalog.FormPerPosition, Operations: map[string]string{catalog.OpChange: "Change"}}
}

// orderChangePathsPost asks for a change of one position.
func orderChangePathsPost(t *testing.T, s *Server, orderID string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost,
		"/api/v1/orders/"+orderID+"/lines/vpn/change", strings.NewReader(`{"changeId":"c-1"}`))
}

// TestOrderChangeOnlyAHeldRightCarriedByAnInstanceChanges. A waiting position has
// nothing to change yet, and a held one whose instance was never recorded has
// nowhere to deliver the change to — both are conflicts the caller can read,
// rather than a start of something new.
func TestOrderChangeOnlyAHeldRightCarriedByAnInstanceChanges(t *testing.T) {
	srv := newServerForErrors(t)
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_ch1", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{orderChangePathsLine(order.StatusPending)}})
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_ch2", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{orderChangePathsLine(order.StatusDone)}})

	code, body := orderChangePathsPost(t, srv, "ord_ch1")
	if code != http.StatusConflict || !strings.Contains(body, "an action is asked only of a held right") {
		t.Errorf("pending = %d %s, want 409", code, body)
	}
	code, body = orderChangePathsPost(t, srv, "ord_ch2")
	if code != http.StatusConflict || !strings.Contains(body, "no instance carries position vpn") {
		t.Errorf("held without a strand = %d %s, want 409", code, body)
	}
	for _, id := range []string{"ord_ch1", "ord_ch2"} {
		if in := orderCancelPathsGet(t, srv, id).Lines[0].Instances; len(in) != 0 {
			t.Errorf("%s: a refused change noted instances %+v", id, in)
		}
	}
}

// TestOrderChangeRefusalsBeforeTheOrderIsRead. An unreadable body is the caller's
// problem and an unreadable order is the server's; the two answer differently.
func TestOrderChangeRefusalsBeforeTheOrderIsRead(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_ch3/lines/vpn/change", errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("unreadable body = %d %s, want 400", code, body)
	}

	orderCancelPathsSave(t, srv, order.Order{ID: "ord_ch3", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{orderChangePathsLine(order.StatusDone)}})
	orderCancelPathsCorrupt(t, srv, "ord_ch3")
	code, body = orderChangePathsPost(t, srv, "ord_ch3")
	if code != http.StatusInternalServerError || !strings.Contains(body, "read order") {
		t.Errorf("unreadable order = %d %s, want 500", code, body)
	}
}
