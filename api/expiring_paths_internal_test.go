package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
)

// TestExpiringSeparatesWhatAReturnCanEndFromWhatItCannot: two rights past their end,
// one whose order line is still there to return and one whose order is gone. Both
// are overdue; only the second is unendable, and it is counted apart because no run
// of the expiry process will ever reduce it. The list is worst-first and bounded by
// the budget, with the cut said out loud.
func TestExpiringSeparatesWhatAReturnCanEndFromWhatItCannot(t *testing.T) {
	l := limits.Default()
	l.ExpiringReport = 1
	srv := newServerWithOptions(t, WithLimits(l))
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{{ItemID: "vpn"}}})

	now := time.Now()
	returnable := now.Add(-48 * time.Hour).UnixNano()
	orphaned := now.Add(-96 * time.Hour).UnixNano()
	conflictsPathsGrant(t, srv,
		model.EntitlementValue{Principal: "usr_1", ItemID: "vpn", OrderID: "ord-1", Since: 1, Until: returnable},
		model.EntitlementValue{Principal: "usr_2", ItemID: "vpn", OrderID: "ord-gone", Since: 1, Until: orphaned},
	)

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/entitlements/expiring", "", "")
	if code != http.StatusOK {
		t.Fatalf("expiring: %d (%s)", code, body)
	}
	var rep expiringReport
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if rep.Counts.Overdue != 2 || rep.Counts.Unendable != 1 || rep.Counts.Due != 0 || rep.Counts.WithAnEnd != 2 {
		t.Errorf("counts = %+v, want 2 overdue of which 1 unendable", rep.Counts)
	}
	if rep.Omitted != 1 || len(rep.Rights) != 1 {
		t.Fatalf("omitted=%d listed=%d, want one listed and one said to be left out", rep.Omitted, len(rep.Rights))
	}
	if got := rep.Rights[0]; got.Principal != "usr_2" || got.Returnable {
		t.Errorf("first = %+v, want the longest-overdue, unendable right at the top", got)
	}
}

// TestExpiringFailsWhenTheOrdersCannotBeRead: whether an overdue right can still be
// returned is read from the orders. Without them every right would be reported as
// unendable — a backlog that no process can work, conjured out of a read error.
func TestExpiringFailsWhenTheOrdersCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "orders"))
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/entitlements/expiring", "", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "expiring: ") {
		t.Errorf("unreadable orders: %d (%s), want 500", code, body)
	}
}

// TestExpiringDuringShutdown: the orders were never read, so nothing can be said
// about what is returnable, and an empty list would read as "nothing is due".
func TestExpiringDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()
	rec := httptest.NewRecorder()
	srv.handleExpiring(rec, httptest.NewRequest(http.MethodGet, "/api/v1/entitlements/expiring", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "shutting down") {
		t.Errorf("during shutdown: %d (%s), want 503", rec.Code, rec.Body)
	}
}
