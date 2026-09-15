package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// One decision about one request (ADR-draft-collective-approval).
//
// The approval process runs per order line, so a workplace ordered as three
// products is three tasks in three instances. That is not changed here and must
// not be: a line is what gets provisioned or refused. What is changed is the
// surface — the approver says once what they would otherwise have typed three
// times, and the server completes each task with that same answer.
//
// These hold the four things that make that honest: the answer reaches every
// instance, a refusal still needs a reason, one reason never covers two orders,
// and what did not go through is named rather than rounded off.

// collectiveBPMN is an approval model with a second user task after the decision,
// so the instance is still alive once the approval is completed and its variables
// can be read back. Without it the process ends immediately and the one thing
// worth asserting — that the answer arrived — would be unobservable.
const collectiveBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="sammel-genehmigung" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="Genehmigen" name="Genehmigen">
      <extensionElements>
        <zeebe:assignmentDefinition assignee="alice"/>
      </extensionElements>
    </userTask>
    <userTask id="Nachlauf" name="Nachlauf"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="Genehmigen"/>
    <sequenceFlow id="f2" sourceRef="Genehmigen" targetRef="Nachlauf"/>
    <sequenceFlow id="f3" sourceRef="Nachlauf" targetRef="end"/>
  </process>
</definitions>`

// decideResult is what the collective route answers: per line, never in aggregate.
type decideResult struct {
	Decided []struct {
		TaskKey uint64 `json:"taskKey"`
		OrderID string `json:"orderId"`
		ItemID  string `json:"itemId"`
	} `json:"decided"`
	Skipped []struct {
		TaskKey uint64 `json:"taskKey"`
		ItemID  string `json:"itemId"`
		Error   string `json:"error"`
	} `json:"skipped"`
}

// aCatalogueOffering publishes one catalogue holding the named products, each
// approved by alice through the model above, and returns the release.
func aCatalogueOffering(t *testing.T, ts *httptest.Server, admin *http.Client, items ...string) string {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatzkatalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	for _, id := range items {
		product := fmt.Sprintf(`{"id":%q,"homeCatalog":%q,"state":"active",`+
			`"texts":{"de":%q},"approval":{"kind":"sammel-genehmigung","ref":"alice"},`+
			`"provisionProcess":"prov","deprovisionProcess":"deprov"}`, id, cat.ID, id)
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
			t.Fatalf("save product %s: %d (%s)", id, code, b)
		}
	}
	list, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":`+string(list)+`}`); code != http.StatusOK {
		t.Fatalf("offer products: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat.ID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("decode release: %v (%s)", err, body)
	}
	return rel.ID
}

// aRequestFor places one order carrying the named positions and starts the
// approval process once per line, the way the fulfilment model starts it. Two
// requests against the same release is what an installation actually has, and it
// is what the same-order rule has to be tested against.
func aRequestFor(t *testing.T, ts *httptest.Server, admin *http.Client, release string, items ...string) string {
	t.Helper()
	list, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+release+`","items":`+string(list)+`}`)
	if code != http.StatusCreated {
		t.Fatalf("place order: %d (%s)", code, body)
	}
	var ord struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v (%s)", err, body)
	}
	for _, id := range items {
		start := fmt.Sprintf(`{"processId":"sammel-genehmigung","variables":{
			"orderId":%q,"itemId":%q,"recipient":"usr_kunde","orderer":"root"}}`, ord.ID, id)
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
			t.Fatalf("start approval for %s: %d (%s)", id, code, b)
		}
	}
	return ord.ID
}

// decideAll posts one decision covering the named tasks and returns the answer.
func decideAll(t *testing.T, ts *httptest.Server, c *http.Client, approved bool, reason string, keys []uint64) (int, decideResult) {
	t.Helper()
	ks, err := json.Marshal(keys)
	if err != nil {
		t.Fatalf("marshal keys: %v", err)
	}
	code, body := cReq(t, c, ts, "POST", "/api/v1/approvals/decide",
		fmt.Sprintf(`{"approved":%t,"reason":%q,"taskKeys":%s}`, approved, reason, ks))
	if code != http.StatusOK {
		return code, decideResult{}
	}
	var out decideResult
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode decision: %v (%s)", err, body)
	}
	return code, out
}

func keysOf(as []approvalView) []uint64 {
	out := make([]uint64, 0, len(as))
	for _, a := range as {
		out = append(out, a.Task.Key)
	}
	return out
}

// TestOneDecisionReachesEveryPositionOfTheRequest.
//
// The whole measure, end to end. Three positions, one call, and then the question
// that matters: did the answer arrive in each instance? Completing three tasks
// while writing the decision into one of them would look identical from the list
// — it empties either way — and every process downstream would branch on a
// variable that was never set.
func TestOneDecisionReachesEveryPositionOfTheRequest(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice", "mallory")[0]
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", collectiveBPMN); code != http.StatusOK {
		t.Fatalf("deploy approval model: %d (%s)", code, b)
	}
	ord := aRequestFor(t, ts, admin, aCatalogueOffering(t, ts, admin, "laptop", "telefon", "vpn"),
		"laptop", "telefon", "vpn")

	held := approvalsOf(t, ts, alice)
	if len(held) != 3 {
		t.Fatalf("alice holds %d approvals, want the request's three", len(held))
	}
	instances := map[uint64]string{}
	for _, a := range held {
		instances[a.Task.Key] = a.ItemID
	}

	code, got := decideAll(t, ts, alice, true, "Standardausstattung", keysOf(held))
	if code != http.StatusOK {
		t.Fatalf("one decision covering three positions: %d", code)
	}
	if len(got.Decided) != 3 || len(got.Skipped) != 0 {
		t.Fatalf("decided %d, skipped %d, want 3 and 0: %+v", len(got.Decided), len(got.Skipped), got)
	}
	for _, d := range got.Decided {
		if d.OrderID != ord {
			t.Errorf("a decision names order %s, want %s", d.OrderID, ord)
		}
	}
	if rest := approvalsOf(t, ts, alice); len(rest) != 0 {
		t.Errorf("%d positions are still open after the request was decided", len(rest))
	}

	// The answer, in each instance. Read as the administrator, because the
	// approver's own scope is the task's form and the task is gone.
	for _, a := range held {
		path := fmt.Sprintf("/api/v1/instances/%d/variables", a.Task.ProcessInstanceKey)
		code, body := cReq(t, admin, ts, "GET", path, "")
		if code != http.StatusOK {
			t.Fatalf("read variables of %s: %d (%s)", instances[a.Task.Key], code, body)
		}
		var vars map[string]any
		if err := json.Unmarshal(body, &vars); err != nil {
			t.Fatalf("decode variables: %v (%s)", err, body)
		}
		if vars["genehmigt"] != true {
			t.Errorf("position %s was completed without the decision: genehmigt = %v",
				instances[a.Task.Key], vars["genehmigt"])
		}
		if vars["begruendung"] != "Standardausstattung" {
			t.Errorf("position %s carries reason %v, want the one reason given",
				instances[a.Task.Key], vars["begruendung"])
		}
	}
}

// TestARefusalOfAWholeRequestStillNeedsAReason.
//
// The single approval enforces it in the browser. A rule only the browser knows
// is not a rule, and this route is the one a script would reach first.
func TestARefusalOfAWholeRequestStillNeedsAReason(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice", "mallory")[0]
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", collectiveBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	aRequestFor(t, ts, admin, aCatalogueOffering(t, ts, admin, "laptop", "telefon"), "laptop", "telefon")

	held := approvalsOf(t, ts, alice)
	if code, _ := decideAll(t, ts, alice, false, "   ", keysOf(held)); code != http.StatusBadRequest {
		t.Fatalf("a refusal of two positions went through with no reason: %d", code)
	}
	if rest := approvalsOf(t, ts, alice); len(rest) != 2 {
		t.Errorf("the refused refusal decided %d positions anyway", 2-len(rest))
	}
	// An approval needs none, which is the same asymmetry the single decision has:
	// somebody saying yes is not being asked to justify themselves.
	if code, got := decideAll(t, ts, alice, true, "", keysOf(held)); code != http.StatusOK || len(got.Decided) != 2 {
		t.Errorf("an approval without a reason was refused: %d %+v", code, got)
	}
}

// TestOneReasonCannotCoverTwoRequests.
//
// The reason is what makes a refusal reviewable a year later, and one sentence
// about two people's requests is a sentence about neither. The call is refused
// whole: deciding the first order's lines and stopping would leave the caller
// having decided something they did not mean to.
func TestOneReasonCannotCoverTwoRequests(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice", "mallory")[0]
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", collectiveBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	rel := aCatalogueOffering(t, ts, admin, "laptop", "telefon", "vpn")
	first := aRequestFor(t, ts, admin, rel, "laptop", "telefon")
	second := aRequestFor(t, ts, admin, rel, "vpn")
	if first == second {
		t.Fatal("the fixture made one order, not two")
	}

	held := approvalsOf(t, ts, alice)
	if len(held) != 3 {
		t.Fatalf("alice holds %d approvals across the two requests, want 3", len(held))
	}
	if code, _ := decideAll(t, ts, alice, true, "alles gut", keysOf(held)); code != http.StatusBadRequest {
		t.Fatalf("one decision spanned two orders: %d", code)
	}
	if rest := approvalsOf(t, ts, alice); len(rest) != 3 {
		t.Errorf("the refused call decided %d positions anyway", 3-len(rest))
	}
}

// TestAnApprovalSomebodyElseHoldsIsNotDecidedByNamingIt.
//
// The gate is the listing's and not the task surface's: this page is one person's
// approvals. A stranger naming the keys — which a mail thread or a screenshot
// could give them — decides nothing, and is told so per key rather than by a
// blanket refusal that would not say which of their keys was the problem.
func TestAnApprovalSomebodyElseHoldsIsNotDecidedByNamingIt(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "alice", "mallory")
	alice, mallory := clients[0], clients[1]
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", collectiveBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	aRequestFor(t, ts, admin, aCatalogueOffering(t, ts, admin, "laptop", "telefon"), "laptop", "telefon")

	held := approvalsOf(t, ts, alice)
	code, got := decideAll(t, ts, mallory, true, "", keysOf(held))
	if code != http.StatusOK {
		t.Fatalf("the stranger's call: %d", code)
	}
	if len(got.Decided) != 0 {
		t.Fatalf("a stranger decided %d of somebody else's positions", len(got.Decided))
	}
	if len(got.Skipped) != 2 {
		t.Fatalf("the answer named %d skipped keys, want both", len(got.Skipped))
	}
	if rest := approvalsOf(t, ts, alice); len(rest) != 2 {
		t.Errorf("alice now holds %d of her own approvals, want 2", len(rest))
	}
}

// TestWhatDidNotGoThroughIsNamed.
//
// There is no transaction across three process instances, so "two of three" is a
// thing that happens — a position decided in another tab, an escalation that
// moved one away. The answer says which, because an approver told "decided" while
// a position is still open finds out from the orderer.
func TestWhatDidNotGoThroughIsNamed(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice", "mallory")[0]
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", collectiveBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	aRequestFor(t, ts, admin, aCatalogueOffering(t, ts, admin, "laptop", "telefon", "vpn"),
		"laptop", "telefon", "vpn")

	held := approvalsOf(t, ts, alice)
	if len(held) != 3 {
		t.Fatalf("alice holds %d approvals, want 3", len(held))
	}
	// One of them decided on its own first, which is the other tab.
	gone := held[0]
	if code, b := cReq(t, alice, ts, "POST",
		fmt.Sprintf("/api/v1/tasks/%d/complete", gone.Task.Key),
		`{"variables":{"genehmigt":true,"begruendung":"vorab"}}`); code != http.StatusOK {
		t.Fatalf("decide one alone: %d (%s)", code, b)
	}

	code, got := decideAll(t, ts, alice, true, "der Rest", keysOf(held))
	if code != http.StatusOK {
		t.Fatalf("the collective decision: %d", code)
	}
	if len(got.Decided) != 2 {
		t.Errorf("decided %d positions, want the two still open: %+v", len(got.Decided), got.Decided)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].TaskKey != gone.Task.Key {
		t.Fatalf("the answer does not name the position that was already decided: %+v", got.Skipped)
	}
	if got.Skipped[0].Error == "" {
		t.Error("a skipped position carries no reason, so the page can only say a number")
	}
	if rest := approvalsOf(t, ts, alice); len(rest) != 0 {
		t.Errorf("%d positions are still open", len(rest))
	}
}
