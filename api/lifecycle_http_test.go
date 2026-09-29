package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A product's lifecycle process (ADR-0425): one message start per operation, each
// branch writing that it ran and then parking, so a test can read which ran.
const laptopLifecycleBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m_prov" name="laptop.provision"/>
  <message id="m_deprov" name="laptop.deprovision"/>
  <message id="m_never" name="never"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <process id="laptop-lifecycle" isExecutable="true">
    <startEvent id="Provision"><messageEventDefinition messageRef="m_prov"/></startEvent>
    <scriptTask id="P"><extensionElements><zeebe:script expression="=true" resultVariable="ranProvision"/></extensionElements></scriptTask>
    <intermediateCatchEvent id="PWait"><messageEventDefinition messageRef="m_never"/></intermediateCatchEvent>
    <endEvent id="PEnd"/>
    <startEvent id="Deprovision"><messageEventDefinition messageRef="m_deprov"/></startEvent>
    <scriptTask id="D"><extensionElements><zeebe:script expression="=true" resultVariable="ranDeprovision"/></extensionElements></scriptTask>
    <intermediateCatchEvent id="DWait"><messageEventDefinition messageRef="m_never"/></intermediateCatchEvent>
    <endEvent id="DEnd"/>
    <sequenceFlow id="p1" sourceRef="Provision" targetRef="P"/>
    <sequenceFlow id="p2" sourceRef="P" targetRef="PWait"/>
    <sequenceFlow id="p3" sourceRef="PWait" targetRef="PEnd"/>
    <sequenceFlow id="d1" sourceRef="Deprovision" targetRef="D"/>
    <sequenceFlow id="d2" sourceRef="D" targetRef="DWait"/>
    <sequenceFlow id="d3" sourceRef="DWait" targetRef="DEnd"/>
  </process>
</definitions>`

// aLifecycleOrder deploys the lifecycle process, binds a product to it, publishes
// and places an order for it. No system processes run, so nothing starts the
// position but the test.
func aLifecycleOrder(t *testing.T) (ts *httptest.Server, admin *http.Client, orderID, catalogID string) {
	t.Helper()
	ts, _ = newAuthServerWith(t, "root", "rootpassword")
	admin = newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", laptopLifecycleBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	catalogID = idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"laptop","homeCatalog":"`+catalogID+`","state":"active","texts":{"de":"Laptop"},`+
			`"approval":{"kind":"none"},"lifecycleProcess":"laptop-lifecycle",`+
			`"operations":{"provision":"laptop.provision","deprovision":"laptop.deprovision"}}`,
	); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+catalogID, `{"items":["laptop"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+catalogID+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+rel+`","items":["laptop"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	return ts, admin, idOf(t, body), catalogID
}

func variablesOf(t *testing.T, c *http.Client, ts *httptest.Server, key uint64) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", key), "")
	if code != http.StatusOK {
		t.Fatalf("variables of %d: %d (%s)", key, code, body)
	}
	return string(body)
}

type startAnswer struct {
	InstanceKey uint64 `json:"instanceKey"`
	Started     string `json:"started"`
	Process     string `json:"process"`
	Already     bool   `json:"already"`
}

// TestTheOrderStartsALifecyclePositionAtItsProvisionStart: the start act enters the
// product's lifecycle process at its provision start and nowhere else, marks the
// line running, and a second call answers with the same instance.
func TestTheOrderStartsALifecyclePositionAtItsProvisionStart(t *testing.T) {
	ts, admin, ord, _ := aLifecycleOrder(t)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/start", `{"operation":"provision"}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, body)
	}
	var first startAnswer
	if err := json.Unmarshal(body, &first); err != nil || first.InstanceKey == 0 || first.Started != "provision" {
		t.Fatalf("answer = %s (%v)", body, err)
	}
	vars := variablesOf(t, admin, ts, first.InstanceKey)
	if !strings.Contains(vars, "ranProvision") || strings.Contains(vars, "ranDeprovision") {
		t.Fatalf("the instance ran %s, want the provision branch only", vars)
	}

	code, body = cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord, "")
	if code != http.StatusOK || !strings.Contains(string(body), `"status":"running"`) {
		t.Fatalf("order after the start: %d (%s), want the line running", code, body)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/start", `{"operation":"provision"}`)
	var again startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &again) != nil || !again.Already || again.InstanceKey != first.InstanceKey {
		t.Fatalf("second start: %d (%s), want the first instance again", code, body)
	}
}

// TestTheDirectedTriggerRoute: an uncatalogued start event is triggered, a repeated
// trigger answers with the first instance, and a catalogue product's operation is
// refused from outside.
func TestTheDirectedTriggerRoute(t *testing.T) {
	ts, admin, _, _ := aLifecycleOrder(t)

	// The catalogue binds both start events of laptop-lifecycle, so a second
	// process with an uncatalogued one is deployed for the positive case.
	other := strings.NewReplacer(`id="laptop-lifecycle"`, `id="hr-events"`,
		`name="laptop.provision"`, `name="hr.joiner"`, `name="laptop.deprovision"`, `name="hr.leaver"`).Replace(laptopLifecycleBPMN)
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", other); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}

	code, body := cReq(t, admin, ts, "POST", "/api/v1/processes/hr-events/triggers/hr.leaver",
		`{"triggerId":"leaver-1","variables":{"orderId":"x"}}`)
	if code != http.StatusCreated {
		t.Fatalf("trigger: %d (%s)", code, body)
	}
	var first struct {
		InstanceKey uint64 `json:"instanceKey"`
		Replayed    bool   `json:"replayed"`
	}
	if err := json.Unmarshal(body, &first); err != nil || first.InstanceKey == 0 {
		t.Fatalf("answer %s (%v)", body, err)
	}
	if vars := variablesOf(t, admin, ts, first.InstanceKey); !strings.Contains(vars, "ranDeprovision") || strings.Contains(vars, "ranProvision") {
		t.Fatalf("the leaver trigger ran %s, want its own branch only", vars)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/processes/hr-events/triggers/hr.leaver",
		`{"triggerId":"leaver-1","variables":{"orderId":"x"}}`)
	var again struct {
		InstanceKey uint64 `json:"instanceKey"`
		Replayed    bool   `json:"replayed"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &again) != nil || !again.Replayed || again.InstanceKey != first.InstanceKey {
		t.Fatalf("repeat: %d (%s), want a replay of %d", code, body, first.InstanceKey)
	}

	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/v1/processes/hr-events/triggers/hr.leaver", `{}`, http.StatusBadRequest},
		{"/api/v1/processes/hr-events/triggers/hr.nobody", `{"triggerId":"a"}`, http.StatusNotFound},
		{"/api/v1/processes/nothing/triggers/hr.leaver", `{"triggerId":"a"}`, http.StatusNotFound},
		{"/api/v1/processes/laptop-lifecycle/triggers/laptop.deprovision", `{"triggerId":"a"}`, http.StatusConflict},
	} {
		if code, b := cReq(t, admin, ts, "POST", tc.path, tc.body); code != tc.want {
			t.Errorf("%s %s: %d (%s), want %d", tc.path, tc.body, code, b, tc.want)
		}
	}
}

// TestPublishingRefusesALifecycleBindingTheProcessDoesNotHave: the catalogue asks the
// engine at publish whether the named start events exist.
func TestPublishingRefusesALifecycleBindingTheProcessDoesNotHave(t *testing.T) {
	ts, admin, _, cat := aLifecycleOrder(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"laptop","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Laptop"},`+
			`"approval":{"kind":"none"},"lifecycleProcess":"laptop-lifecycle",`+
			`"operations":{"provision":"laptop.provision","deprovision":"laptop.gone"}}`,
	); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), "laptop.gone") {
		t.Fatalf("publish: %d (%s), want 422 naming laptop.gone", code, body)
	}
}

// TestAReturnEntersTheLifecycleProcessAtItsDeprovisionStart: giving a held position
// back starts the same process at its deprovision start, and the order records the
// instance as the deprovisioning.
func TestAReturnEntersTheLifecycleProcessAtItsDeprovisionStart(t *testing.T) {
	ts, admin, ord, _ := aLifecycleOrder(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/start", `{}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/return", ``); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord, "")
	if code != http.StatusOK {
		t.Fatalf("order: %d (%s)", code, body)
	}
	var o struct {
		Lines []struct {
			Status    string `json:"status"`
			Instances []struct {
				Key       uint64 `json:"key"`
				Operation string `json:"operation"`
			} `json:"instances"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &o); err != nil || len(o.Lines) != 1 {
		t.Fatalf("decode order: %v (%s)", err, body)
	}
	var deprov uint64
	for _, in := range o.Lines[0].Instances {
		if in.Operation == "deprovision" {
			deprov = in.Key
		}
	}
	if o.Lines[0].Status != "returning" || deprov == 0 {
		t.Fatalf("line = %+v, want returning with a deprovision instance", o.Lines[0])
	}
	if vars := variablesOf(t, admin, ts, deprov); !strings.Contains(vars, "ranDeprovision") || strings.Contains(vars, "ranProvision") {
		t.Fatalf("the return ran %s, want the deprovision branch only", vars)
	}
}

// TestTheStartActRefusesWhatIsNotItsToStart: each way the start act declines, and
// the approval a gated position owes before its provisioning.
func TestTheStartActRefusesWhatIsNotItsToStart(t *testing.T) {
	ts, admin, ord, cat := aLifecycleOrder(t)
	start := func(line, body string) (int, []byte) {
		return cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/"+line+"/start", body)
	}
	for _, c := range []struct {
		line, body string
		want       int
	}{
		{"laptop", `{`, http.StatusBadRequest},
		{"laptop", `{"operation":"deprovision"}`, http.StatusBadRequest},
		{"nothing", `{}`, http.StatusNotFound},
	} {
		if code, b := start(c.line, c.body); code != c.want {
			t.Errorf("%s %s: %d (%s), want %d", c.line, c.body, code, b, c.want)
		}
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/nope/lines/laptop/start", `{}`); code != http.StatusNotFound {
		t.Errorf("unknown order: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop", `{"status":"skipped"}`); code != http.StatusOK {
		t.Fatalf("settle the line: %d (%s)", code, b)
	}
	if code, b := start("laptop", `{}`); code != http.StatusConflict {
		t.Errorf("a settled line: %d (%s), want 409", code, b)
	}

	// A gated product: the act owes the approval first. The approval process is not
	// deployed on this server, so the act says it cannot start it; with approvedBy it
	// records the approval and provisions.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"laptop","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Laptop"},`+
			`"approval":{"kind":"fixed","ref":"root"},"lifecycleProcess":"laptop-lifecycle",`+
			`"operations":{"provision":"laptop.provision","deprovision":"laptop.deprovision"}}`,
	); code != http.StatusOK {
		t.Fatalf("gate the product: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+rel+`","items":["laptop"]}`)
	if code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, body)
	}
	gated := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+gated+"/lines/laptop/start", `{}`); code != http.StatusConflict ||
		!strings.Contains(string(b), "atlas-genehmigung-fix") {
		t.Errorf("gated start without the approval process: %d (%s), want 409 naming it", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+gated+"/lines/laptop/start", `{"approvedBy":"root"}`)
	var got startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &got) != nil || got.Started != "provision" {
		t.Fatalf("approved start: %d (%s), want the provisioning", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/orders/"+gated, "")
	if code != http.StatusOK || !strings.Contains(string(body), `"approvedBy":"root"`) {
		t.Fatalf("order after the approved start: %d (%s), want the approver recorded", code, body)
	}
}

// TestTheTriggerRouteRefusesAnUnreadableBody: malformed JSON and variables that are
// not an object are the caller's to fix.
func TestTheTriggerRouteRefusesAnUnreadableBody(t *testing.T) {
	ts, admin, _, _ := aLifecycleOrder(t)
	for _, body := range []string{`{`, `{"triggerId":"a","variables":"x"}`} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/processes/laptop-lifecycle/triggers/x", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d (%s), want 400", body, code, b)
		}
	}
}
