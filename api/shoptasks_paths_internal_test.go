package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/state"
)

// The shop's task list (ADR-0416), for a line that records one instance under two
// operations, a task addressed to nobody, and an order store that cannot be read.

// shopTasksPathsAsk calls the route as one person.
func shopTasksPathsAsk(t *testing.T, s *Server, userID string) (int, shopTasksResp, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shop/tasks", nil)
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: userID}))
	rec := httptest.NewRecorder()
	s.handleShopTasks(rec, req)
	var out shopTasksResp
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, out, rec.Body.String()
}

// shopTasksPathsInstance starts the user-task process for one order position and
// returns the key of the instance working it.
func shopTasksPathsInstance(t *testing.T, s *Server, orderID string) uint64 {
	t.Helper()
	pendingWorkPathsStart(t, s, "shop-step", `{"orderId":"`+orderID+`","positionId":"vpn"}`)
	var (
		key uint64
		ok  bool
	)
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		var err error
		key, ok, err = positionInstance(rv, orderID, "vpn")
		return err
	}); err != nil || !ok {
		t.Fatalf("no instance works %s/vpn: ok=%v err=%v", orderID, ok, err)
	}
	return key
}

// TestShopTasksAnInstanceRecordedTwiceIsListedOnce. A per-position line notes its
// one instance once per operation it carried; its open task is still one task.
func TestShopTasksAnInstanceRecordedTwiceIsListedOnce(t *testing.T) {
	srv := newServerForErrors(t)
	key := shopTasksPathsInstance(t, srv, "ord_t1")
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_t1", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{{ItemID: "vpn", Status: order.StatusDone, Instances: []order.LineInstance{
			{Key: key, ProcessID: "shop-step", Operation: catalog.OpProvision},
			{Key: key, ProcessID: "shop-step", Operation: catalog.OpChange},
		}}}})

	code, got, body := shopTasksPathsAsk(t, srv, "usr_ada")
	if code != http.StatusOK {
		t.Fatalf("tasks = %d %s", code, body)
	}
	if len(got.Tasks) != 1 {
		t.Errorf("tasks = %+v, want the one open task once", got.Tasks)
	}
}

// TestShopTasksATaskAddressedToNobodyHoldsNoOrder. Behind the access boundary a
// person sees somebody else's order only through a task addressed to them; one the
// model addressed to nobody would otherwise put that order in front of everybody.
func TestShopTasksATaskAddressedToNobodyHoldsNoOrder(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_bo", Orderer: "usr_bo", Recipient: "usr_bo",
		Lines: []order.Line{{ItemID: "vpn", Status: order.StatusRunning}}})
	shopTasksPathsInstance(t, srv, "ord_bo")

	code, got, body := shopTasksPathsAsk(t, srv, "usr_ada")
	if code != http.StatusOK {
		t.Fatalf("tasks = %d %s", code, body)
	}
	if len(got.Orders) != 0 || len(got.Tasks) != 0 {
		t.Errorf("ada sees %+v / %+v, want nothing of Bo's", got.Orders, got.Tasks)
	}
}

// TestShopTasksAnUnreadableOrderStoreIsAFault. "You have no orders" from a store
// that could not be read is the one wrong answer.
func TestShopTasksAnUnreadableOrderStoreIsAFault(t *testing.T) {
	srv := newServerForErrors(t)
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_t2"))
	orderCancelPathsCorrupt(t, srv, "ord_t2")

	if code, _, body := shopTasksPathsAsk(t, srv, "usr_ada"); code != http.StatusInternalServerError ||
		!strings.Contains(body, "read orders") {
		t.Errorf("tasks = %d %s, want 500", code, body)
	}
}
