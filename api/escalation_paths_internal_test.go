package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
)

// escalationPathsMove posts one move to the escalation routes through the mux.
func escalationPathsMove(t *testing.T, srv *Server, orderID, item, action, body string) (int, []byte) {
	t.Helper()
	return serveInternal(t, srv, http.MethodPost,
		"/api/v1/orders/"+orderID+"/lines/"+item+"/"+action, body, "application/json")
}

// escalationPathsOrder reads an order back, on the loop.
func escalationPathsOrder(t *testing.T, srv *Server, id string) order.Order {
	t.Helper()
	var (
		o   order.Order
		ok  bool
		err error
	)
	srv.do(func() { o, ok, err = srv.orderStore.Get(id) })
	if err != nil || !ok {
		t.Fatalf("read order %s: ok=%v err=%v", id, ok, err)
	}
	return o
}

// escalationPathsAssignee is who the task is with now, read on the loop.
func escalationPathsAssignee(t *testing.T, srv *Server, key uint64) string {
	t.Helper()
	var who string
	srv.do(func() {
		if jv, ok, err := srv.store.GetJob(key); err == nil && ok {
			who = srv.enrichTask(key, jv).Assignee
		}
	})
	return who
}

// TestEscalationRefusesABodyThatCannotBeRead: a move whose body broke off names
// nobody to move to. Both doors refuse it before looking for the approval, and the
// order records no hop.
func TestEscalationRefusesABodyThatCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("vpn", "")}})

	for _, action := range []string{"escalate", "reassign"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/api/v1/orders/ord-1/lines/vpn/"+action, errReader{}))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
			t.Errorf("%s with a broken body: %d (%s), want 400 'read body'", action, rec.Code, rec.Body)
		}
	}
	if got := escalationPathsOrder(t, srv, "ord-1"); len(got.Assignments) != 0 {
		t.Errorf("a refused move was recorded: %+v", got.Assignments)
	}
}

// TestEscalationReassignNeedsSomebodyToRecord: with authentication off there is no
// identity, and an intervention recorded without the person who made it is worth
// nothing — it would read exactly like a hop a deadline made.
func TestEscalationReassignNeedsSomebodyToRecord(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("vpn", "")}})

	code, body := escalationPathsMove(t, srv, "ord-1", "vpn", "reassign", `{"to":"bruno"}`)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "records who did it") {
		t.Fatalf("anonymous reassign: %d (%s), want 400", code, body)
	}
	if who := escalationPathsAssignee(t, srv, key); who != "alice" {
		t.Errorf("the task moved to %q on a refused reassign", who)
	}
}

// TestEscalationReassignRecordsAnIdentityWithoutAUsername: a principal that carries
// only an id — a service identity, say — is still somebody, and the hop records that
// id rather than refusing as if nobody had acted.
func TestEscalationReassignRecordsAnIdentityWithoutAUsername(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("vpn", "")}})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/ord-1/lines/vpn/reassign", strings.NewReader(`{"to":"bruno"}`))
	req.SetPathValue("id", "ord-1")
	req.SetPathValue("item", "vpn")
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "svc_ops"}))
	rec := httptest.NewRecorder()
	srv.handleReassignApproval(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign: %d (%s)", rec.Code, rec.Body)
	}
	var got approvalMoveResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if got.Approver != "bruno" || len(got.Assignment.Escalations) != 1 || got.Assignment.Escalations[0].By != "svc_ops" {
		t.Errorf("move = %+v, want bruno holding it and the hop made by svc_ops", got)
	}
	if who := escalationPathsAssignee(t, srv, key); who != "bruno" {
		t.Errorf("the task is with %q, want bruno", who)
	}
}

// TestEscalationFindsTheApprovalByItsProduct: a process started before positions had
// names reports only the product, and an order carrying one position of it still
// names one approval. Another open user task — one that decides no order — is passed
// over on the way rather than mistaken for it.
func TestEscalationFindsTheApprovalByItsProduct(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	approvalsPathsStart(t, srv, `{"note":"not an approval"}`)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"phone","positionId":"phone#black"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("phone", "black")}})

	code, body := escalationPathsMove(t, srv, "ord-1", "phone", "escalate", `{"superior":"carla"}`)
	if code != http.StatusOK {
		t.Fatalf("escalate by product: %d (%s)", code, body)
	}
	var got approvalMoveResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if got.Approver != "carla" || got.Assignment.ItemID != "phone#black" {
		t.Errorf("move = %+v, want carla holding the black phone's approval", got)
	}
	if who := escalationPathsAssignee(t, srv, key); who != "carla" {
		t.Errorf("the task is with %q, want carla", who)
	}
	// Recorded against the position, so a second position's history stays its own.
	if o := escalationPathsOrder(t, srv, "ord-1"); len(o.Assignments) != 1 || o.Assignments[0].ItemID != "phone#black" {
		t.Errorf("assignments = %+v, want one for phone#black", o.Assignments)
	}
}

// TestEscalationStalledListFailsWhenTheOrdersCannotBeRead: the stalled list is the
// one place a chain that ran out becomes visible. An empty answer from a store that
// could not be read would say nothing is stuck when nobody can tell.
func TestEscalationStalledListFailsWhenTheOrdersCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "orders"))

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/approvals/stalled", "", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "read orders") {
		t.Errorf("unreadable orders: %d (%s), want 500 'read orders'", code, body)
	}
}

// TestEscalationFailsWhenTheOrdersCannotBeRead: which open task is the approval for
// a line is a question about the orders. With them unreadable the honest answer is
// that the server cannot tell — not "no open approval", which would send a deadline
// model off to report a decided approval while it still sits, unmoved, with its
// approver. Both doors fail the same way and the task stays where it was.
func TestEscalationFailsWhenTheOrdersCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"vpn"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{approvalsPathsLine("vpn", "")}})
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "orders"))

	code, body := escalationPathsMove(t, srv, "ord-1", "vpn", "escalate", `{"superior":"carla"}`)
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "find the approval") {
		t.Errorf("escalate over unreadable orders: %d (%s), want 500 'find the approval'", code, body)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/ord-1/lines/vpn/reassign", strings.NewReader(`{"to":"bruno"}`))
	req.SetPathValue("id", "ord-1")
	req.SetPathValue("item", "vpn")
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "usr_ops", Username: "ops"}))
	rec := httptest.NewRecorder()
	srv.handleReassignApproval(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "find the approval") {
		t.Errorf("reassign over unreadable orders: %d (%s), want 500 'find the approval'", rec.Code, rec.Body)
	}

	if who := escalationPathsAssignee(t, srv, key); who != "alice" {
		t.Errorf("the task moved to %q although the approval could not be found", who)
	}
}

// TestEscalationRefusesAProductTheOrderCarriesTwice: naming the product when the
// order carries two positions of it does not say which position's approval is meant.
// That is the caller's ambiguity, answered as a conflict that names both positions so
// the caller can choose — not a server fault — and nothing is recorded or moved.
func TestEscalationRefusesAProductTheOrderCarriesTwice(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDeploy(t, srv)
	key := approvalsPathsStart(t, srv, `{"orderId":"ord-1","itemId":"phone","positionId":"phone#black"}`)
	approvalsPathsOrder(t, srv, order.Order{ID: "ord-1", Lines: []order.Line{
		approvalsPathsLine("phone", "black"), approvalsPathsLine("phone", "silver")}})

	code, body := escalationPathsMove(t, srv, "ord-1", "phone", "escalate", `{"superior":"carla"}`)
	if code != http.StatusConflict || !strings.Contains(string(body), "phone#black") || !strings.Contains(string(body), "phone#silver") {
		t.Fatalf("escalate by an ambiguous product: %d (%s), want 409 naming both positions", code, body)
	}
	if o := escalationPathsOrder(t, srv, "ord-1"); len(o.Assignments) != 0 {
		t.Errorf("assignments = %+v after a refused move, want none", o.Assignments)
	}
	if who := escalationPathsAssignee(t, srv, key); who != "alice" {
		t.Errorf("the task moved to %q on a refused move", who)
	}
}
