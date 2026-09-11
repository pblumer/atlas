package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A deadline may move an approval and may never decide it. These are the moves,
// end to end: the hop a clock makes, the one a person makes, and the answer when
// there is nowhere left to go.

type moveResp struct {
	Approver   string `json:"approver"`
	Stalled    bool   `json:"stalled"`
	Reason     string `json:"reason"`
	Assignment struct {
		ItemID      string `json:"itemId"`
		Approver    string `json:"approver"`
		Original    string `json:"original"`
		StalledAt   int64  `json:"stalledAt"`
		Escalations []struct {
			From string `json:"from"`
			To   string `json:"to"`
			By   string `json:"by"`
		} `json:"escalations"`
	} `json:"assignment"`
}

func move(t *testing.T, ts *httptest.Server, c *http.Client, order, item, action, body string) (int, moveResp) {
	t.Helper()
	code, raw := cReq(t, c, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/%s/%s", order, item, action), body)
	var out moveResp
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", action, err, raw)
		}
	}
	return code, out
}

// anApprovalWaitingOn stands up an order whose one line is waiting on alice, and
// returns the order id.
func anApprovalWaitingOn(t *testing.T, ts *httptest.Server, admin *http.Client) string {
	t.Helper()
	_, orderID := aCatalogueWithAnOrder(t, ts, admin)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", ownApprovalBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	start := fmt.Sprintf(`{"processId":"kunden-genehmigung","variables":{
		"orderId":%q,"itemId":"vpn","recipient":"usr_kunde","orderer":"root"}}`, orderID)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
		t.Fatalf("start the approval: %d (%s)", code, b)
	}
	return orderID
}

// assigneeOfTheOnlyTask reads who holds the single open task.
func assigneeOfTheOnlyTask(t *testing.T, ts *httptest.Server, c *http.Client) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d (%s)", code, body)
	}
	var tasks []struct {
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (%s)", len(tasks), body)
	}
	return tasks[0].Assignee
}

func TestADeadlineMovesTheApprovalAndTheTaskWithIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice", "bruno")
	orderID := anApprovalWaitingOn(t, ts, admin)

	if got := assigneeOfTheOnlyTask(t, ts, admin); got != "alice" {
		t.Fatalf("the approval starts with %q, want alice", got)
	}

	code, got := move(t, ts, admin, orderID, "vpn", "escalate", `{"superior":"bruno"}`)
	if code != http.StatusOK {
		t.Fatalf("escalate = %d", code)
	}
	if got.Approver != "bruno" || got.Stalled {
		t.Fatalf("= %+v, want it with bruno and not stalled", got)
	}
	// The task moved too. A recorded hop and a task still in the old inbox is the
	// failure this endpoint exists to avoid.
	if who := assigneeOfTheOnlyTask(t, ts, admin); who != "bruno" {
		t.Errorf("the task is still with %q; the hop moved nothing", who)
	}
	// And the record says where it started, which a decision by a third deputy
	// reads very differently without.
	if got.Assignment.Original != "alice" {
		t.Errorf("original = %q, want alice", got.Assignment.Original)
	}
	if len(got.Assignment.Escalations) != 1 {
		t.Fatalf("escalations = %+v, want one hop", got.Assignment.Escalations)
	}
	if h := got.Assignment.Escalations[0]; h.From != "alice" || h.To != "bruno" || h.By != "" {
		t.Errorf("hop = %+v; a clock's hop carries no author", h)
	}
}

// TestAnEscalationWithNowhereToGoStallsAndSaysSo.
//
// A group approval has no superior and the top of a hierarchy has none either.
// Both answer the question with "nobody", and the approval then has to become
// visible rather than quietly stay where it was — an approval nobody can escalate
// and nobody is looking at is how an order waits forever.
func TestAnEscalationWithNowhereToGoStallsAndSaysSo(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice")
	orderID := anApprovalWaitingOn(t, ts, admin)

	code, got := move(t, ts, admin, orderID, "vpn", "escalate", `{"superior":""}`)
	if code != http.StatusOK {
		t.Fatalf("escalate = %d", code)
	}
	if !got.Stalled || got.Reason == "" {
		t.Fatalf("= %+v, want it stalled with a reason a model can pass on", got)
	}
	// Stalling reports a fact; it does not take the task away.
	if got.Approver != "alice" {
		t.Errorf("approver = %q, want it still with alice", got.Approver)
	}
	if who := assigneeOfTheOnlyTask(t, ts, admin); who != "alice" {
		t.Errorf("the task moved to %q; stalling moves nothing", who)
	}
	if got.Assignment.StalledAt == 0 {
		t.Error("nothing recorded the stall, so nothing makes it visible later")
	}
}

// TestAnEscalationWillNotFollowADirectoryLoop: two colleagues recorded as each
// other's superior is the common shape of a directory loop, and following it would
// hand the approval back and forth until the order is forgotten.
func TestAnEscalationWillNotFollowADirectoryLoop(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice", "bruno")
	orderID := anApprovalWaitingOn(t, ts, admin)

	if code, _ := move(t, ts, admin, orderID, "vpn", "escalate", `{"superior":"bruno"}`); code != http.StatusOK {
		t.Fatalf("first hop = %d", code)
	}
	// Back to alice, who already had it.
	code, got := move(t, ts, admin, orderID, "vpn", "escalate", `{"superior":"alice"}`)
	if code != http.StatusOK {
		t.Fatalf("second hop = %d", code)
	}
	if !got.Stalled {
		t.Fatalf("= %+v, want a stall rather than a hop back", got)
	}
	if who := assigneeOfTheOnlyTask(t, ts, admin); who != "bruno" {
		t.Errorf("the task went back to %q", who)
	}
}

// TestAPersonMayUnstickAnApprovalButNotTakeIt is the one rule in this mechanism
// that is a property of the model rather than a permission: the escalation path
// exists so a stalled approval reaches somebody who will act on it, and whoever
// finds it taking it for themselves turns the mechanism into its own bypass.
func TestAPersonMayUnstickAnApprovalButNotTakeIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice", "bruno")
	orderID := anApprovalWaitingOn(t, ts, admin)

	// root, an operator, hands it to bruno. Allowed: somebody else decides.
	code, got := move(t, ts, admin, orderID, "vpn", "reassign", `{"to":"bruno"}`)
	if code != http.StatusOK {
		t.Fatalf("reassign = %d", code)
	}
	if got.Approver != "bruno" {
		t.Fatalf("approver = %q, want bruno", got.Approver)
	}
	if h := got.Assignment.Escalations[0]; h.By == "" {
		t.Errorf("hop = %+v; an intervention records who made it", h)
	}
	if who := assigneeOfTheOnlyTask(t, ts, admin); who != "bruno" {
		t.Errorf("the task is with %q", who)
	}

	// And root giving it to root is refused, whatever role root holds.
	code, _ = cReqMove(t, ts, admin, orderID, "vpn", "reassign", `{"to":"root"}`)
	if code != http.StatusConflict {
		t.Errorf("taking the approval for oneself = %d, want 409", code)
	}
}

// cReqMove is move without decoding, for the cases that expect a refusal.
func cReqMove(t *testing.T, ts *httptest.Server, c *http.Client, order, item, action, body string) (int, []byte) {
	t.Helper()
	return cReq(t, c, ts, "POST",
		fmt.Sprintf("/api/v1/orders/%s/lines/%s/%s", order, item, action), body)
}

// TestMovingAnApprovalThatIsNotThereIsA404: an approval already decided has no
// task, and moving it would be moving nothing.
func TestMovingAnApprovalThatIsNotThereIsA404(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, _ := cReqMove(t, ts, admin, "ord_nope", "vpn", "escalate", `{"superior":"x"}`); code != http.StatusNotFound {
		t.Errorf("escalating nothing = %d, want 404", code)
	}
}

// TestAStalledApprovalIsSomewhereSomebodyCanFindIt.
//
// A stall records a fact, and a fact nobody queries is not visible. This is the
// one place to ask, and it is the difference between "the mechanism noticed" and
// "somebody will notice".
func TestAStalledApprovalIsSomewhereSomebodyCanFindIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	twoUsers(t, ts, admin, "alice")

	stalled := func() []struct {
		OrderID    string `json:"orderId"`
		Orderer    string `json:"orderer"`
		Assignment struct {
			ItemID    string `json:"itemId"`
			Approver  string `json:"approver"`
			StalledAt int64  `json:"stalledAt"`
		} `json:"assignment"`
	} {
		t.Helper()
		code, body := cReq(t, admin, ts, "GET", "/api/v1/approvals/stalled", "")
		if code != http.StatusOK {
			t.Fatalf("list stalled: %d (%s)", code, body)
		}
		var out []struct {
			OrderID    string `json:"orderId"`
			Orderer    string `json:"orderer"`
			Assignment struct {
				ItemID    string `json:"itemId"`
				Approver  string `json:"approver"`
				StalledAt int64  `json:"stalledAt"`
			} `json:"assignment"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		return out
	}

	if got := stalled(); len(got) != 0 {
		t.Fatalf("a fresh instance lists %d stalled approvals", len(got))
	}

	orderID := anApprovalWaitingOn(t, ts, admin)
	if code, _ := move(t, ts, admin, orderID, "vpn", "escalate", `{"superior":""}`); code != http.StatusOK {
		t.Fatalf("escalate = %d", code)
	}

	got := stalled()
	if len(got) != 1 {
		t.Fatalf("stalled = %d, want 1", len(got))
	}
	if got[0].OrderID != orderID || got[0].Assignment.ItemID != "vpn" {
		t.Errorf("= %+v, want the line that stalled", got[0])
	}
	if got[0].Assignment.Approver != "alice" || got[0].Assignment.StalledAt == 0 {
		t.Errorf("= %+v, want it still with alice and stamped", got[0].Assignment)
	}
	// Who is waiting, so whoever reads this knows who to tell.
	if got[0].Orderer == "" {
		t.Error("the list does not say whose order is waiting")
	}

	// And a person unsticking it takes it off the list: the stall is cleared by the
	// intervention, so the deadline works normally again from the new holder.
	if code, _ := move(t, ts, admin, orderID, "vpn", "reassign", `{"to":"alice2"}`); code != http.StatusOK {
		t.Fatalf("reassign = %d", code)
	}
	if got := stalled(); len(got) != 0 {
		t.Errorf("still listed after somebody took it on: %+v", got)
	}
}
