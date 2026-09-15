package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Taking an order back, end to end.
//
// The likeliest support call a self-service portal receives is somebody who
// ordered the wrong thing a minute ago, and the answer used to be to telephone the
// approver and ask them to refuse it — filing a decision nobody made.

type cancelResult struct {
	Order struct {
		ID    string `json:"id"`
		Lines []struct {
			ItemID    string `json:"itemId"`
			Status    string `json:"status"`
			DecidedBy string `json:"decidedBy"`
			DecidedAt int64  `json:"decidedAt"`
		} `json:"lines"`
	} `json:"order"`
	Cancelled []string `json:"cancelled"`
	Kept      []string `json:"kept"`
}

func cancelOrder(t *testing.T, ts *httptest.Server, c *http.Client, id, body string) (int, cancelResult) {
	t.Helper()
	code, raw := cReq(t, c, ts, "POST", "/api/v1/orders/"+id+"/cancel", body)
	var out cancelResult
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, raw)
		}
	}
	return code, out
}

func TestWithdrawingAnOrderTakesBackWhatHasNotHappened(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)

	code, got := cancelOrder(t, ts, admin, orderID, `{"reason":"falsch bestellt"}`)
	if code != http.StatusOK {
		t.Fatalf("cancel = %d", code)
	}
	if len(got.Cancelled) != 1 || got.Cancelled[0] != "vpn" {
		t.Fatalf("cancelled = %v, want the one line", got.Cancelled)
	}
	if len(got.Kept) != 0 {
		t.Errorf("kept = %v, want nothing held back", got.Kept)
	}
	l := got.Order.Lines[0]
	if l.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled — not rejected: nobody refused it", l.Status)
	}
	// An author and a moment, like every other way a line settles without being
	// provisioned. A status kept for years that says somebody decided, without
	// saying who, is a decision nobody made.
	if l.DecidedBy == "" || l.DecidedAt == 0 {
		t.Errorf("line = %+v, want it to name who withdrew it and when", l)
	}

	// And a second attempt says there is nothing left, rather than pretending.
	if code, _ := cancelOrder(t, ts, admin, orderID, `{}`); code != http.StatusConflict {
		t.Errorf("cancelling twice = %d, want 409", code)
	}
}

// TestWithdrawingLeavesWhatIsAlreadyUnderWay: "your order is cancelled" when a
// laptop is already on its way is the sentence that produces the second support
// call, so the answer names what it could not take back.
func TestWithdrawingLeavesWhatIsAlreadyUnderWay(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)

	// Report the line as running: it is with a provisioning process now, which is
	// a conversation with a system this server does not control.
	if code, b := cReq(t, admin, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/vpn", orderID), `{"status":"running"}`); code != http.StatusOK {
		t.Fatalf("report running: %d (%s)", code, b)
	}

	code, _ := cancelOrder(t, ts, admin, orderID, `{}`)
	if code != http.StatusConflict {
		t.Fatalf("cancel = %d, want 409: nothing in it can still be withdrawn", code)
	}
}

// TestAnOrderIsWithdrawnByWhoeverPlacedIt, and by nobody else who merely happens
// to be signed in. An order placed *for* somebody is not theirs to withdraw.
func TestAnOrderIsWithdrawnByWhoeverPlacedIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	stranger := twoUsers(t, ts, admin, "mallory")[0]
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)

	// 404 and not 403: whose orders exist is not something this endpoint answers.
	if code, _ := cancelOrder(t, ts, stranger, orderID, `{}`); code != http.StatusNotFound {
		t.Errorf("a stranger's cancel = %d, want 404", code)
	}
	if code, _ := cancelOrder(t, ts, admin, orderID, `{}`); code != http.StatusOK {
		t.Errorf("the orderer's own cancel = %d", code)
	}
}

// TestWithdrawingClosesTheApprovalItWasWaitingOn.
//
// An approval left standing on a withdrawn line is a task asking somebody to
// decide a request that no longer exists — and eventually they do, which records a
// refusal against something nobody ever considered.
func TestWithdrawingClosesTheApprovalItWasWaitingOn(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice")
	orderID := anApprovalWaitingOn(t, ts, admin)

	if got := assigneeOfTheOnlyTask(t, ts, admin); got != "alice" {
		t.Fatalf("the approval is with %q", got)
	}

	if code, _ := cancelOrder(t, ts, admin, orderID, `{}`); code != http.StatusOK {
		t.Fatalf("cancel = %d", code)
	}

	code, body := cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d (%s)", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(tasks) != 0 {
		t.Errorf("%d approval task(s) still open on a withdrawn order (%s)", len(tasks), body)
	}
}
