package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/order"
)

// Withdrawing one position and correcting its details, when the request carries
// nobody or the order cannot be read. Both acts are recorded against a person, so
// both refuse a request without one; neither touches an order it could not read.

// TestOrderAmendBehindTheBoundaryNeedsAnIdentity. Each refusal names the act it
// would have recorded, and the order is untouched.
func TestOrderAmendBehindTheBoundaryNeedsAnIdentity(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_a1"))

	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		body    string
		want    string
	}{
		{"withdraw", srv.handleCancelLine, "", "withdrawing a position records who did it"},
		{"correct", srv.handleAmendLine, `{"config":{"size":"L"}}`, "correcting a position's details records who did it"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		req.SetPathValue("id", "ord_a1")
		req.SetPathValue("item", "vpn")
		tc.handler(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s = %d %s, want 400 %q", tc.name, rec.Code, rec.Body.String(), tc.want)
		}
	}
	if got := orderCancelPathsGet(t, srv, "ord_a1").Lines[0]; got.Status != order.StatusPending || len(got.Config) != 0 {
		t.Errorf("line = %+v, want it untouched", got)
	}
}

// TestOrderAmendAnUnreadableOrderIsAFault. A record that does not decode is a
// broken store, answered 500 — not "no order", which would invite placing it
// again.
func TestOrderAmendAnUnreadableOrderIsAFault(t *testing.T) {
	srv := newServerForErrors(t)
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_a2"))
	orderCancelPathsCorrupt(t, srv, "ord_a2")
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_a2/lines/vpn/cancel", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "withdraw position") {
		t.Errorf("withdraw = %d %s, want 500", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/orders/ord_a2/lines/vpn/details",
		strings.NewReader(`{"config":{"size":"L"}}`))
	if code != http.StatusInternalServerError || !strings.Contains(body, "correct details") {
		t.Errorf("correct = %d %s, want 500", code, body)
	}
}
