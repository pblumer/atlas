package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The open tasks of an order, in the shop (ADR-0416).
//
// The fixture is the case the feature was asked for: alice's order carries a VPN
// that bob has to approve, and a laptop that needs nobody. The approval is started
// the way the fulfilment orchestration starts it — POST /api/v1/instances with the
// order and the position — so what is tested is the link the server records at
// that moment, not a search that happens to find the instance.

type shopTasksBody struct {
	Tasks []struct {
		Key        uint64 `json:"key"`
		OrderID    string `json:"orderId"`
		PositionID string `json:"positionId"`
		Name       string `json:"name"`
		FormID     string `json:"formId"`
		Approval   bool   `json:"approval"`
		Holder     struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"holder"`
		MayWork bool `json:"mayWork"`
	} `json:"tasks"`
	Orders []struct {
		ID    string `json:"id"`
		Lines []struct {
			ItemID    string            `json:"itemId"`
			Config    map[string]string `json:"config"`
			Instances []any             `json:"instances"`
		} `json:"lines"`
	} `json:"orders"`
	Truncated bool `json:"truncated"`
}

// anOrderBobApproves places alice's order and returns the server, the three
// people, and the order id.
func anOrderBobApproves(t *testing.T) (ts *httptest.Server, admin, alice, bob *http.Client, orderID string) {
	t.Helper()
	ts, _ = newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
	admin = newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"bob","password":"password1","displayName":"Bob Muster"}`); code != http.StatusCreated {
		t.Fatalf("create bob: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","displayName":"Alice Beispiel"}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice: %d (%s)", code, body)
	}
	aliceID := idOf(t, body)
	alice, bob = newClient(t), newClient(t)
	for c, u := range map[*http.Client]string{alice: "alice", bob: "bob"} {
		if login(t, c, ts, u, "password1") != http.StatusOK {
			t.Fatalf("%s login failed", u)
		}
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	for _, p := range []string{
		`{"id":"vpn","homeCatalog":"` + cat + `","state":"active","texts":{"de":"VPN"},` +
			`"approval":{"kind":"fixed","ref":"bob"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
		`{"id":"laptop","homeCatalog":"` + cat + `","state":"active","texts":{"de":"Laptop"},` +
			`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", p); code != http.StatusOK {
			t.Fatalf("save product: %d (%s)", code, b)
		}
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"items":["vpn","laptop"]}`); code != http.StatusOK {
		t.Fatalf("offer the products: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["vpn","laptop"],"recipient":"`+aliceID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	return ts, admin, alice, bob, idOf(t, body)
}

// startApproval starts the shipped fixed-approver model for one position, the way
// the orchestration does, and returns the instance key the start answered with.
func startApproval(t *testing.T, ts *httptest.Server, admin *http.Client, orderID, position, approver string) uint64 {
	t.Helper()
	code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", `{"processId":"atlas-genehmigung-fix","variables":{
		"orderId":"`+orderID+`","itemId":"`+position+`","positionId":"`+position+`",
		"approvalRef":"`+approver+`","provisionProcess":"prov","recipient":"alice","orderer":"root"}}`)
	if code != http.StatusOK {
		t.Fatalf("start the approval: %d (%s)", code, b)
	}
	var resp struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(b, &resp); err != nil || resp.InstanceKey == 0 {
		t.Fatalf("the start answered without the instance it started: %s", b)
	}
	return resp.InstanceKey
}

func shopTasks(t *testing.T, c *http.Client, ts *httptest.Server) shopTasksBody {
	t.Helper()
	code, b := cReq(t, c, ts, "GET", "/api/v1/shop/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/shop/tasks = %d (%s)", code, b)
	}
	var out shopTasksBody
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, b)
	}
	return out
}

// TestAStartedProcessIsRecordedOnThePositionItWorks: the start answers with the
// instance it created, and the order's position names it.
func TestAStartedProcessIsRecordedOnThePositionItWorks(t *testing.T) {
	ts, admin, alice, _, ord := anOrderBobApproves(t)
	key := startApproval(t, ts, admin, ord, "vpn", "bob")

	code, b := cReq(t, alice, ts, "GET", "/api/v1/orders/"+ord, "")
	if code != http.StatusOK {
		t.Fatalf("read the order: %d (%s)", code, b)
	}
	var o struct {
		Lines []struct {
			ItemID    string `json:"itemId"`
			Instances []struct {
				Key       uint64 `json:"key"`
				ProcessID string `json:"processId"`
			} `json:"instances"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	for _, l := range o.Lines {
		switch l.ItemID {
		case "vpn":
			if len(l.Instances) != 1 || l.Instances[0].Key != key ||
				l.Instances[0].ProcessID != "atlas-genehmigung-fix" {
				t.Errorf("vpn instances = %+v, want the one approval started (%d)", l.Instances, key)
			}
		case "laptop":
			if len(l.Instances) != 0 {
				t.Errorf("laptop instances = %+v, want none: nothing was started for it", l.Instances)
			}
		}
	}
}

// TestTheRecipientSeesWhomTheirPositionWaitsFor.
//
// The approval is named, it is known by the rule the line is approved under, and
// the approver is named as a person reads the name. The recipient may not answer
// it — approving one's own order is what the task gate exists to stop.
func TestTheRecipientSeesWhomTheirPositionWaitsFor(t *testing.T) {
	ts, admin, alice, _, ord := anOrderBobApproves(t)
	startApproval(t, ts, admin, ord, "vpn", "bob")

	got := shopTasks(t, alice, ts)
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks = %+v, want the one approval", got.Tasks)
	}
	task := got.Tasks[0]
	if task.OrderID != ord || task.PositionID != "vpn" || task.Name != "Genehmigen" {
		t.Errorf("task = %+v, want Genehmigen on %s/vpn", task, ord)
	}
	if !task.Approval || task.Holder.Kind != "fixed" || task.Holder.Name != "Bob Muster" {
		t.Errorf("holder = %+v approval=%v, want the fixed approver by display name",
			task.Holder, task.Approval)
	}
	if task.MayWork {
		t.Error("the recipient is offered their own approval to answer")
	}
	if len(got.Orders) != 0 {
		t.Errorf("orders = %+v, want none: alice's own order is not a held one", got.Orders)
	}
}

// TestTheApproverSeesTheOrderTheyHoldATaskIn.
//
// Bob neither placed nor receives the order. He holds its approval, and that is
// what puts it in front of him — without the answers alice gave on the products'
// forms, and with the task his to answer.
func TestTheApproverSeesTheOrderTheyHoldATaskIn(t *testing.T) {
	ts, admin, _, bob, ord := anOrderBobApproves(t)
	startApproval(t, ts, admin, ord, "vpn", "bob")

	got := shopTasks(t, bob, ts)
	if len(got.Orders) != 1 || got.Orders[0].ID != ord {
		t.Fatalf("orders = %+v, want alice's order", got.Orders)
	}
	for _, l := range got.Orders[0].Lines {
		if l.Config != nil || l.Instances != nil {
			t.Errorf("line %s carries %v / %v: a holder is not shown the orderer's "+
				"answers or the order's bookkeeping", l.ItemID, l.Config, l.Instances)
		}
	}
	if len(got.Tasks) != 1 || !got.Tasks[0].MayWork {
		t.Fatalf("tasks = %+v, want bob's approval, his to answer", got.Tasks)
	}

	// And answering it is the task route's, under its own gate.
	key := strconv.FormatUint(got.Tasks[0].Key, 10)
	if code, b := cReq(t, bob, ts, "POST", "/api/v1/tasks/"+key+"/complete",
		`{"variables":{"genehmigt":true,"begruendung":"ok"}}`); code != http.StatusOK {
		t.Fatalf("bob completes his approval: %d (%s)", code, b)
	}
}

// TestSomebodyWithNoPartInAnOrderSeesNothingOfIt.
func TestSomebodyWithNoPartInAnOrderSeesNothingOfIt(t *testing.T) {
	ts, admin, _, _, ord := anOrderBobApproves(t)
	startApproval(t, ts, admin, ord, "vpn", "bob")
	mallory := twoUsers(t, ts, admin, "mallory")[0]

	got := shopTasks(t, mallory, ts)
	if len(got.Tasks) != 0 || len(got.Orders) != 0 {
		t.Fatalf("a stranger sees tasks %+v and orders %+v", got.Tasks, got.Orders)
	}
}

// TestAProcessNamingNoOrderStartsAsBefore: a process whose orderId names nothing
// this server holds starts, answers 200, and is recorded nowhere.
func TestAProcessNamingNoOrderStartsAsBefore(t *testing.T) {
	ts, admin, _, _, _ := anOrderBobApproves(t)
	startApproval(t, ts, admin, "ord_nobody", "vpn", "bob")
}

// TestAnApprovalNamesWhomItIsFor: the approval an approver reads says for whom by
// name. The order and the process keep the id (ADR-0314); the name is resolved
// when the approval is read.
func TestAnApprovalNamesWhomItIsFor(t *testing.T) {
	ts, admin, _, bob, ord := anOrderBobApproves(t)
	startApproval(t, ts, admin, ord, "vpn", "bob")

	code, b := cReq(t, bob, ts, "GET", "/api/v1/approvals", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/approvals = %d (%s)", code, b)
	}
	var page struct {
		Items []struct {
			Recipient     string `json:"recipient"`
			RecipientName string `json:"recipientName"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("approvals = %s (%v)", b, err)
	}
	if got := page.Items[0].RecipientName; got != "Alice Beispiel" {
		t.Errorf("recipientName = %q, want the recipient's display name", got)
	}
	if page.Items[0].Recipient == "" || page.Items[0].Recipient == "Alice Beispiel" {
		t.Errorf("recipient = %q, want the id kept beside the name", page.Items[0].Recipient)
	}
}

// TestAStartNamingAPositionTheOrderLacksStillStarts: the order is found and the
// position is not. The instance runs; failing the start would make the caller
// retry and start the work twice, so the record is what is given up, with a warning.
func TestAStartNamingAPositionTheOrderLacksStillStarts(t *testing.T) {
	ts, admin, alice, _, ord := anOrderBobApproves(t)
	startApproval(t, ts, admin, ord, "tablet", "bob")
	if got := shopTasks(t, alice, ts); len(got.Tasks) != 0 {
		t.Errorf("tasks = %+v, want none: nothing was recorded for a position the order lacks", got.Tasks)
	}
}
