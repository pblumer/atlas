package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ownApprovalBPMN is an installation's own approval model, which is what a
// catalogue binds when its approval kind is not one of the three built in
// (see order.Line.ApprovalProcess). It is used here rather than the shipped
// atlas-genehmigung-fix because that one addresses its task with `=approvalRef`,
// and Atlas does not evaluate an expression in an assignment definition — the task
// is assigned to the literal string. That is a defect in the platform models and
// not in this page, and it is recorded as such; this case is about the page.
const ownApprovalBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="kunden-genehmigung" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="Genehmigen" name="Genehmigen">
      <extensionElements>
        <zeebe:assignmentDefinition assignee="alice"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="Genehmigen"/>
    <sequenceFlow id="f2" sourceRef="Genehmigen" targetRef="end"/>
  </process>
</definitions>`

// The approver's page, end to end: a catalogue with a brand, a product that needs
// approving, an order placed against a release of it, the approval process
// started the way the fulfilment model starts it — and then the one call the page
// makes.
//
// It is written as one long case on purpose. Each step is a precondition of the
// next, and the chain is exactly what was broken twice while this was being built:
// the approval process id named nothing deployed, and the route that starts a
// process by id did not exist. A test that mocked either would have passed
// through both.

type approvalView struct {
	Task struct {
		Key uint64 `json:"key"`
	} `json:"task"`
	OrderID      string            `json:"orderId"`
	ItemID       string            `json:"itemId"`
	Recipient    string            `json:"recipient"`
	Texts        map[string]string `json:"texts"`
	CatalogID    string            `json:"catalogId"`
	CatalogTexts map[string]string `json:"catalogTexts"`
	Theme        struct {
		Accent   string `json:"accent"`
		Typeface string `json:"typeface"`
	} `json:"theme"`
}

// approvalsOf reads one client's approvals.
func approvalsOf(t *testing.T, ts *httptest.Server, c *http.Client) []approvalView {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/approvals", "")
	if code != http.StatusOK {
		t.Fatalf("list approvals: %d (%s)", code, body)
	}
	var out []approvalView
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode approvals: %v (%s)", err, body)
	}
	return out
}

func TestAnApproverSeesTheirOwnRequestInItsOwnBrand(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "alice", "mallory")
	alice, mallory := clients[0], clients[1]

	cat, orderID := aCatalogueWithAnOrder(t, ts, admin)

	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", ownApprovalBPMN); code != http.StatusOK {
		t.Fatalf("deploy approval model: %d (%s)", code, b)
	}

	// And the approval, started by process id exactly as the fulfilment model
	// starts it — including that the model addresses the process by id and not by
	// key, which is the route that did not exist.
	start := fmt.Sprintf(`{"processId":"kunden-genehmigung","variables":{
		"orderId":%q,"itemId":"vpn","recipient":"usr_kunde","orderer":"root"}}`, orderID)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
		t.Fatalf("start approval: %d (%s)", code, b)
	}

	// What the page reads.
	got := approvalsOf(t, ts, alice)
	if len(got) != 1 {
		t.Fatalf("alice sees %d approvals, want 1", len(got))
	}
	a := got[0]
	if a.OrderID != orderID || a.ItemID != "vpn" {
		t.Errorf("approval decides %s/%s, want %s/vpn", a.OrderID, a.ItemID, orderID)
	}
	if a.Recipient != "usr_kunde" {
		t.Errorf("recipient = %q", a.Recipient)
	}
	// The product in words, not as an id: the whole reason the join happens on the
	// server, where the release can be read.
	if a.Texts["de"] != "VPN-Zugang" {
		t.Errorf("texts = %v, want the product's own name", a.Texts)
	}
	if a.CatalogID != cat || a.CatalogTexts["de"] != "Kundenkatalog" {
		t.Errorf("catalogue = %s %v", a.CatalogID, a.CatalogTexts)
	}
	// And the brand, which is the point of the page.
	if a.Theme.Accent != "#d52b1e" || a.Theme.Typeface != "serif" {
		t.Errorf("theme = %+v, want the catalogue's", a.Theme)
	}

	// The approver is not the catalogue's audience and must not become it. The
	// brand reached them through the task; the catalogue itself stays closed.
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/catalogs/"+cat, ""); code != http.StatusNotFound {
		t.Errorf("the catalogue opened to an approver: %d", code)
	}

	// And somebody who holds nothing sees nothing.
	if got := approvalsOf(t, ts, mallory); len(got) != 0 {
		t.Errorf("a stranger sees %d approvals, want none", len(got))
	}

	// Deciding is completing the task, which is what the page does.
	if code, b := cReq(t, alice, ts, "POST",
		fmt.Sprintf("/api/v1/tasks/%d/complete", a.Task.Key),
		`{"variables":{"genehmigt":false,"begruendung":"nicht nötig"}}`); code != http.StatusOK {
		t.Fatalf("alice decides: %d (%s)", code, b)
	}
	if got := approvalsOf(t, ts, alice); len(got) != 0 {
		t.Errorf("a decided approval is still listed: %d", len(got))
	}
}

// TestTheMarkTravelsUnderTheTaskAndNotTheCatalogue.
//
// An approver is not the catalogue's audience — a line manager approves a request
// for a customer group they are not in — so the catalogue's own logo route refuses
// them, and rightly: opening it would open one customer's mark to everybody who
// ever holds a task. The mark therefore travels under the gate the approver does
// pass, which is the task.
func TestTheMarkTravelsUnderTheTaskAndNotTheCatalogue(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "alice", "mallory")
	alice, mallory := clients[0], clients[1]

	cat, ord := aCatalogueWithAnOrder(t, ts, admin)
	png := "\x89PNG\r\n\x1a\n" + "mark"
	if code, b := cReqTyped(t, admin, ts, "PUT", "/api/v1/catalogs/"+cat+"/logo", "image/png", png); code != http.StatusNoContent {
		t.Fatalf("upload mark: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", ownApprovalBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	start := fmt.Sprintf(`{"processId":"kunden-genehmigung","variables":{
		"orderId":%q,"itemId":"vpn","recipient":"usr_kunde","orderer":"root"}}`, ord)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", start); code != http.StatusOK {
		t.Fatalf("start approval: %d (%s)", code, b)
	}

	got := approvalsOf(t, ts, alice)
	if len(got) != 1 {
		t.Fatalf("alice sees %d approvals, want 1", len(got))
	}
	path := fmt.Sprintf("/api/v1/approvals/%d/logo", got[0].Task.Key)

	code, body := cReq(t, alice, ts, "GET", path, "")
	if code != http.StatusOK {
		t.Fatalf("the approver cannot see the mark of the order they are deciding: %d (%s)", code, body)
	}
	if string(body) != png {
		t.Errorf("the mark came back changed: %q", body)
	}
	// The catalogue itself stays shut to them, which is the point of the separate
	// route rather than a widened one.
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/catalogs/"+cat+"/logo", ""); code != http.StatusNotFound {
		t.Errorf("the catalogue's own logo route opened to an approver: %d", code)
	}
	// And somebody holding nothing gets neither.
	if code, _ := cReq(t, mallory, ts, "GET", path, ""); code != http.StatusForbidden {
		t.Errorf("a stranger reached the mark: %d", code)
	}
}

// TestStartingByProcessIdTakesTheNewestVersion: an orchestrator that pinned the
// version it first saw would keep starting a superseded model for as long as an
// order stayed open, which is the opposite of what redeploying one means.
func TestStartingByProcessIdTakesTheNewestVersion(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	for i := 0; i < 2; i++ {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", ownApprovalBPMN); code != http.StatusOK {
			t.Fatalf("deploy %d: %d (%s)", i, code, b)
		}
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/processes", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: %d (%s)", code, body)
	}
	var procs []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		Version   int32  `json:"version"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	var newest uint64
	var best int32
	for _, p := range procs {
		if p.ProcessID == "kunden-genehmigung" && p.Version >= best {
			newest, best = p.Key, p.Version
		}
	}
	if best < 2 {
		t.Fatalf("the second deployment did not make a second version (%s)", body)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/instances", `{"processId":"kunden-genehmigung"}`)
	if code != http.StatusOK {
		t.Fatalf("start by id: %d (%s)", code, body)
	}
	var started struct {
		DefinitionKey uint64 `json:"definitionKey"`
	}
	if err := json.Unmarshal(body, &started); err != nil {
		t.Fatalf("decode start: %v (%s)", err, body)
	}
	if started.DefinitionKey != newest {
		t.Errorf("started definition %d, want the newest %d", started.DefinitionKey, newest)
	}
}

func TestStartingByProcessIdRefusesWhatIsNotThere(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"no such process", `{"processId":"nichts-dergleichen"}`, http.StatusNotFound},
		{"no process at all", `{"variables":{"a":1}}`, http.StatusBadRequest},
		{"not JSON", `nope`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", tc.body); code != tc.want {
				t.Fatalf("= %d (%s), want %d", code, b, tc.want)
			}
		})
	}
}

// aCatalogueWithAnOrder builds the design-time half these cases need: a branded
// catalogue, a product in it that needs approving, a release, and one order
// placed against that release. It returns the catalogue and the order.
func aCatalogueWithAnOrder(t *testing.T, ts *httptest.Server, admin *http.Client) (string, string) {
	t.Helper()
	// A catalogue, branded, owned by the administrator who creates it.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Kundenkatalog"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	var cat struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/catalogs/"+cat.ID+"/theme",
		`{"accent":"#d52b1e","typeface":"serif"}`); code != http.StatusOK {
		t.Fatalf("set theme: %d (%s)", code, b)
	}

	// A product that alice approves, by name.
	product := `{"id":"vpn","homeCatalog":"` + cat.ID + `","state":"active",` +
		`"texts":{"de":"VPN-Zugang"},"approval":{"kind":"kunden-genehmigung","ref":"alice"},` +
		`"provisionProcess":"prov","deprovisionProcess":"deprov"}`
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", product); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat.ID,
		`{"items":["vpn"]}`); code != http.StatusOK {
		t.Fatalf("offer product: %d (%s)", code, b)
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

	// An order against that release. The maintainer may order from their own
	// catalogue, which is what lets this run without inventing a customer group.
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel.ID+`","items":["vpn"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place order: %d (%s)", code, body)
	}
	var ord struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ord); err != nil {
		t.Fatalf("decode order: %v (%s)", err, body)
	}

	return cat.ID, ord.ID
}
