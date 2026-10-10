package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
)

// Withdrawing a whole order, from the sides the HTTP tests do not reach:
// single-user mode, a request without an identity behind the access boundary, an
// order that does not exist, and an order record that cannot be read. Each refusal
// leaves the order as it was.

// orderCancelPathsSave files an order straight into the order store.
func orderCancelPathsSave(t *testing.T, s *Server, o order.Order) {
	t.Helper()
	var err error
	s.do(func() { err = s.orderStore.Save(o) })
	if err != nil {
		t.Fatalf("save order %s: %v", o.ID, err)
	}
}

// orderCancelPathsGet reads an order back from the store.
func orderCancelPathsGet(t *testing.T, s *Server, id string) order.Order {
	t.Helper()
	var (
		o   order.Order
		ok  bool
		err error
	)
	s.do(func() { o, ok, err = s.orderStore.Get(id) })
	if err != nil || !ok {
		t.Fatalf("read order %s: ok=%v err=%v", id, ok, err)
	}
	return o
}

// orderCancelPathsCorrupt replaces an order's record with bytes that do not
// decode, which is what a torn write or a hand edit leaves behind.
func orderCancelPathsCorrupt(t *testing.T, s *Server, id string) {
	t.Helper()
	path := filepath.Join(s.dataDir, "orders", hex.EncodeToString([]byte(id))+".json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt order %s: %v", id, err)
	}
}

// orderCancelPathsPending is an order of one waiting line.
func orderCancelPathsPending(id string) order.Order {
	return order.Order{ID: id, Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{{ItemID: "vpn", Status: order.StatusPending}}}
}

// TestOrderCancelSingleUserModeRecordsAName. Without authentication there is no
// principal, and the withdrawal still records who did it — the transition needs
// a name, and an empty one would read as nobody.
func TestOrderCancelSingleUserModeRecordsAName(t *testing.T) {
	srv := newServerForErrors(t)
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_c1"))

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/orders/ord_c1/cancel",
		strings.NewReader(`{"reason":"ordered twice"}`))
	if code != http.StatusOK {
		t.Fatalf("cancel = %d %s", code, body)
	}
	var resp cancelResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Cancelled) != 1 || resp.Cancelled[0] != "vpn" {
		t.Errorf("cancelled = %v, want the waiting line", resp.Cancelled)
	}
	line := orderCancelPathsGet(t, srv, "ord_c1").Lines[0]
	if line.Status != order.StatusCancelled || line.DecidedBy != "single-user" || line.Reason != "ordered twice" {
		t.Errorf("line = %+v, want it cancelled by single-user with the reason", line)
	}
}

// TestOrderCancelRefusals. A body that is not JSON, an order that does not exist,
// and one whose record cannot be read: none of them withdraws anything.
func TestOrderCancelRefusals(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_c2"))

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_c2/cancel", strings.NewReader(`{"reason":`))
	if code != http.StatusBadRequest || !strings.Contains(body, "malformed JSON body") {
		t.Errorf("malformed = %d %s, want 400", code, body)
	}
	if got := orderCancelPathsGet(t, srv, "ord_c2").Lines[0].Status; got != order.StatusPending {
		t.Errorf("a refused cancel changed the line to %s", got)
	}

	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_nope/cancel", nil)
	if code != http.StatusNotFound || !strings.Contains(body, "no order ord_nope") {
		t.Errorf("unknown = %d %s, want 404", code, body)
	}

	orderCancelPathsCorrupt(t, srv, "ord_c2")
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_c2/cancel", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "cancel order") {
		t.Errorf("unreadable = %d %s, want 500", code, body)
	}
}

// TestOrderCancelBehindTheBoundaryNeedsAnIdentity. A withdrawal is recorded
// against somebody; a request that reaches the handler carrying nobody is refused
// rather than recorded against a placeholder.
func TestOrderCancelBehindTheBoundaryNeedsAnIdentity(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_c3"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/ord_c3/cancel", nil)
	req.SetPathValue("id", "ord_c3")
	srv.handleCancelOrder(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no identity") {
		t.Errorf("cancel = %d %s, want 400 naming the missing identity", rec.Code, rec.Body.String())
	}
	if got := orderCancelPathsGet(t, srv, "ord_c3").Lines[0].Status; got != order.StatusPending {
		t.Errorf("the line became %s", got)
	}

	// The same request carrying the orderer goes through.
	rec = httptest.NewRecorder()
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "usr_ada"}))
	srv.handleCancelOrder(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cancel as the orderer = %d %s, want 200", rec.Code, rec.Body.String())
	}
}
