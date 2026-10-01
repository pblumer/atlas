package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A per-position strand with a service beside the change (ADR-0429): it waits at an
// event-based gateway for a change, a password reset or its return. A change stops
// at a person's task before the strand waits again, so a test can see a strand that
// does not take an action right now; a reset runs a script and waits again at once.
const actingStrandBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m_prov" name="tool.provision"/>
  <message id="m_deprov" name="tool.deprovision">
    <extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>
  </message>
  <message id="m_change" name="tool.change">
    <extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>
  </message>
  <message id="m_reset" name="tool.reset">
    <extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>
  </message>
  <message id="m_audit" name="tool.audit">
    <extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>
  </message>
  <process id="tool-strand" isExecutable="true">
    <startEvent id="Provision"><messageEventDefinition messageRef="m_prov"/></startEvent>
    <exclusiveGateway id="Wait"/>
    <eventBasedGateway id="Gate"/>
    <intermediateCatchEvent id="AskChange"><messageEventDefinition messageRef="m_change"/></intermediateCatchEvent>
    <userTask id="Busy"/>
    <scriptTask id="C"><extensionElements><zeebe:script expression="=size" resultVariable="changedTo"/></extensionElements></scriptTask>
    <intermediateCatchEvent id="AskReset"><messageEventDefinition messageRef="m_reset"/></intermediateCatchEvent>
    <scriptTask id="R"><extensionElements><zeebe:script expression="=reason" resultVariable="resetFor"/></extensionElements></scriptTask>
    <intermediateCatchEvent id="AskAudit"><messageEventDefinition messageRef="m_audit"/></intermediateCatchEvent>
    <scriptTask id="A"><extensionElements><zeebe:script expression="=true" resultVariable="audited"/></extensionElements></scriptTask>
    <intermediateCatchEvent id="AskReturn"><messageEventDefinition messageRef="m_deprov"/></intermediateCatchEvent>
    <startEvent id="Fallback"><messageEventDefinition messageRef="m_deprov"/></startEvent>
    <exclusiveGateway id="Return"/>
    <endEvent id="End"/>
    <sequenceFlow id="s1" sourceRef="Provision" targetRef="Wait"/>
    <sequenceFlow id="s2" sourceRef="Wait" targetRef="Gate"/>
    <sequenceFlow id="s3" sourceRef="Gate" targetRef="AskChange"/>
    <sequenceFlow id="s4" sourceRef="AskChange" targetRef="Busy"/>
    <sequenceFlow id="s4b" sourceRef="Busy" targetRef="C"/>
    <sequenceFlow id="s5" sourceRef="C" targetRef="Wait"/>
    <sequenceFlow id="r1" sourceRef="Gate" targetRef="AskReset"/>
    <sequenceFlow id="r2" sourceRef="AskReset" targetRef="R"/>
    <sequenceFlow id="r3" sourceRef="R" targetRef="Wait"/>
    <sequenceFlow id="a1" sourceRef="Gate" targetRef="AskAudit"/>
    <sequenceFlow id="a2" sourceRef="AskAudit" targetRef="A"/>
    <sequenceFlow id="a3" sourceRef="A" targetRef="Wait"/>
    <sequenceFlow id="s6" sourceRef="Gate" targetRef="AskReturn"/>
    <sequenceFlow id="s7" sourceRef="AskReturn" targetRef="Return"/>
    <sequenceFlow id="s8" sourceRef="Fallback" targetRef="Return"/>
    <sequenceFlow id="s9" sourceRef="Return" targetRef="End"/>
  </process>
</definitions>`

// toolActions are the tool's actions: the order's two, a change and a reset the
// person who holds it may ask for, and an audit only an operator runs.
func toolActions(deprovisionTriggers string) string {
	return `[{"key":"provision","message":"tool.provision","effect":"provision"},` +
		`{"key":"deprovision","message":"tool.deprovision","effect":"deprovision","triggers":` + deprovisionTriggers + `},` +
		`{"key":"change","message":"tool.change","effect":"change","triggers":["customer","operator"],"labels":{"de":"Ändern"}},` +
		`{"key":"password-reset","message":"tool.reset","effect":"service","triggers":["customer"],"labels":{"de":"Passwort zurücksetzen","en":"Reset password"},"form":"reset-form"},` +
		`{"key":"audit","message":"tool.audit","effect":"service","triggers":["operator"],"labels":{"de":"Prüfen"}}]`
}

// aToolHeldForRita publishes the tool, orders it as an admin for rita, provisions it
// and reports it done. It answers rita's client and a stranger's beside the admin's.
func aToolHeldForRita(t *testing.T, deprovisionTriggers string) (ts *httptest.Server, admin, rita, stranger *http.Client, ord string, strand uint64) {
	t.Helper()
	ts, _ = newAuthServerWith(t, "root", "rootpassword")
	admin = newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	ritaID := createUserWithRoles(t, admin, ts.URL, "rita", `["user"]`)
	createUserWithRoles(t, admin, ts.URL, "sam", `["user"]`)
	rita = signInAs(t, ts.URL, "rita", "a-password-that-is-long")
	stranger = signInAs(t, ts.URL, "sam", "a-password-that-is-long")

	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", actingStrandBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"tool","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Werkzeug"},`+
			`"approval":{"kind":"none"},"lifecycleProcess":"tool-strand","lifecycleForm":"per-position",`+
			`"actions":`+toolActions(deprovisionTriggers)+`}`); code != http.StatusOK {
		t.Fatalf("save: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["tool"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+idOf(t, body)+`","items":["tool"],"recipient":"`+ritaID+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, body)
	}
	ord = idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/start", `{"operation":"provision"}`)
	var started startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &started) != nil || started.InstanceKey == 0 {
		t.Fatalf("start: %d (%s)", code, body)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	return ts, admin, rita, stranger, ord, started.InstanceKey
}

// offeredAction is one action as the availability route answers it.
type offeredAction struct {
	Key       string            `json:"key"`
	Available bool              `json:"available"`
	Why       string            `json:"why"`
	Labels    map[string]string `json:"labels"`
	Form      string            `json:"form"`
}

// actionsOf reads which actions a caller is offered on the tool, by key.
func actionsOf(t *testing.T, c *http.Client, ts *httptest.Server, ord string) (int, map[string]offeredAction, string) {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/orders/"+ord+"/lines/tool/actions", "")
	out := map[string]offeredAction{}
	if code != http.StatusOK {
		return code, out, string(body)
	}
	var resp struct {
		Actions []offeredAction `json:"actions"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode actions: %v (%s)", err, body)
	}
	for _, a := range resp.Actions {
		out[a.Key] = a
	}
	return code, out, string(body)
}

// TestTheRecipientAsksAHeldRightForItsActions: the person a right was ordered for is
// offered its customer actions — not the operator's audit, not the provision or the
// return — with their labels and form; asking for one reaches the strand that holds
// the right with the reason, and a retry under the same command id is the first.
func TestTheRecipientAsksAHeldRightForItsActions(t *testing.T) {
	ts, admin, rita, stranger, ord, strand := aToolHeldForRita(t, `["customer","operator"]`)
	act := func(c *http.Client, key, body string) (int, []byte) {
		return cReq(t, c, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/"+key, body)
	}

	code, offered, body := actionsOf(t, rita, ts, ord)
	if code != http.StatusOK || len(offered) != 2 || !offered["change"].Available || !offered["password-reset"].Available {
		t.Fatalf("rita is offered %d (%s), want change and password-reset, both available", code, body)
	}
	if got := offered["password-reset"]; got.Labels["en"] != "Reset password" || got.Form != "reset-form" {
		t.Fatalf("password-reset = %+v, want its labels and form", got)
	}
	if _, ok := offered["audit"]; ok {
		t.Fatalf("rita is offered the operator's audit: %s", body)
	}
	if code, offered, body := actionsOf(t, admin, ts, ord); code != http.StatusOK || !offered["audit"].Available {
		t.Fatalf("an operator is offered %d (%s), want the audit beside the customer actions", code, body)
	}

	if code, b := act(rita, "password-reset", `{"reason":"locked out"}`); code != http.StatusBadRequest || !strings.Contains(string(b), "commandId is required") {
		t.Fatalf("without a command id: %d (%s), want 400", code, b)
	}
	if code, b := act(rita, "password-reset", `{"commandId":"r-1","variables":{"orderId":"someone-else"}}`); code != http.StatusBadRequest || !strings.Contains(string(b), "set by the order") {
		t.Fatalf("a variable the order sets: %d (%s), want 400", code, b)
	}
	code, b := act(rita, "password-reset", `{"commandId":"r-1","reason":"locked out"}`)
	var took struct {
		Action      string `json:"action"`
		InstanceKey uint64 `json:"instanceKey"`
		Process     string `json:"process"`
	}
	if code != http.StatusOK || json.Unmarshal(b, &took) != nil || took.InstanceKey != strand || took.Action != "password-reset" || took.Process != "tool-strand" {
		t.Fatalf("reset: %d (%s), want delivered to %d", code, b, strand)
	}
	if vars := variablesOf(t, admin, ts, strand); !strings.Contains(vars, `"resetFor"`) || !strings.Contains(vars, "locked out") {
		t.Fatalf("the strand did not run the reset: %s", vars)
	}
	if code, b := act(rita, "password-reset", `{"commandId":"r-1","reason":"locked out"}`); code != http.StatusOK {
		t.Fatalf("retried reset: %d (%s), want the first answer", code, b)
	}
	if got := instancesOf(t, ts, admin, ord); len(got["password-reset"]) != 1 || got["password-reset"][0] != strand {
		t.Fatalf("instances = %v, want the reset recorded once on the strand", got)
	}

	if code, b := act(rita, "audit", `{"commandId":"a-1"}`); code != http.StatusForbidden || !strings.Contains(string(b), "operator") {
		t.Fatalf("rita asks for the operator's audit: %d (%s), want 403", code, b)
	}
	if code, b := act(admin, "audit", `{"commandId":"a-1"}`); code != http.StatusOK {
		t.Fatalf("an operator asks for the audit: %d (%s)", code, b)
	}
	for key, want := range map[string]string{
		"provision":   "the order's own",
		"deprovision": "/return",
		"no-such":     "declares no action no-such",
	} {
		if code, b := act(rita, key, `{"commandId":"x"}`); code != http.StatusConflict || !strings.Contains(string(b), want) {
			t.Fatalf("%s: %d (%s), want 409 naming %q", key, code, b, want)
		}
	}

	// Somebody who neither placed the order nor holds what it granted is told what
	// everyone is told about an order that is not there.
	if code, _, body := actionsOf(t, stranger, ts, ord); code != http.StatusNotFound {
		t.Fatalf("a stranger reads the actions: %d (%s), want 404", code, body)
	}
	if code, b := act(stranger, "password-reset", `{"commandId":"s-1"}`); code != http.StatusNotFound {
		t.Fatalf("a stranger asks: %d (%s), want 404", code, b)
	}
}

// TestAnActionTheStrandDoesNotWaitForIsNotAvailable: while the strand works a change
// it does not wait for anything, so neither action is offered as available, and
// asking anyway is refused by the engine's own answer rather than queued.
func TestAnActionTheStrandDoesNotWaitForIsNotAvailable(t *testing.T) {
	ts, _, rita, _, ord, _ := aToolHeldForRita(t, `["customer","operator"]`)
	if code, b := cReq(t, rita, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/change",
		`{"commandId":"c-1","variables":{"size":"L"}}`); code != http.StatusOK {
		t.Fatalf("change: %d (%s)", code, b)
	}
	code, offered, body := actionsOf(t, rita, ts, ord)
	if code != http.StatusOK || offered["change"].Available || offered["password-reset"].Available ||
		!strings.Contains(offered["password-reset"].Why, "does not take this action now") {
		t.Fatalf("while the change runs: %d (%s), want nothing available", code, body)
	}
	code, b := cReq(t, rita, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/password-reset", `{"commandId":"r-1"}`)
	if code != http.StatusConflict || !strings.Contains(string(b), "does not wait for tool.reset") {
		t.Fatalf("reset while busy: %d (%s), want 409", code, b)
	}
}

// TestTheReturnFollowsItsActionsTriggers: the recipient who holds a right may give it
// back when its return names `customer` (ADR-0429 §10, decision 4) — before, the
// portal offered the button and the server answered 404 — and a product whose return
// only an operator gives keeps it from the recipient.
func TestTheReturnFollowsItsActionsTriggers(t *testing.T) {
	ts, _, rita, _, ord, strand := aToolHeldForRita(t, `["customer","operator"]`)
	code, b := cReq(t, rita, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/return", "")
	if code != http.StatusOK || !strings.Contains(string(b), `"status":"returning"`) {
		t.Fatalf("rita returns what she holds: %d (%s)", code, b)
	}
	_ = strand

	ts2, admin2, rita2, _, ord2, _ := aToolHeldForRita(t, `["operator"]`)
	if code, b := cReq(t, rita2, ts2, "POST", "/api/v1/orders/"+ord2+"/lines/tool/return", ""); code != http.StatusForbidden || !strings.Contains(string(b), "operator") {
		t.Fatalf("rita returns what only an operator returns: %d (%s), want 403", code, b)
	}
	if code, b := cReq(t, admin2, ts2, "GET", "/api/v1/orders/"+ord2, ""); code != http.StatusOK || !strings.Contains(string(b), `"status":"done"`) {
		t.Fatalf("after the refused return: %d (%s), want the line still held", code, b)
	}
	if code, b := cReq(t, admin2, ts2, "POST", "/api/v1/orders/"+ord2+"/lines/tool/return", ""); code != http.StatusOK {
		t.Fatalf("an operator returns it: %d (%s)", code, b)
	}
}

// A per-operation lifecycle with a repair beside provision and return: each action is
// a message start of its own, so asking for one starts an instance.
var repairingLaptopBPMN = strings.NewReplacer(
	`<message id="m_never"`,
	`<message id="m_repair" name="laptop.repair"/><message id="m_never"`,
	`<endEvent id="DEnd"/>`,
	`<endEvent id="DEnd"/><startEvent id="Repair"><messageEventDefinition messageRef="m_repair"/></startEvent>`+
		`<scriptTask id="F"><extensionElements><zeebe:script expression="=reason" resultVariable="repairedFor"/></extensionElements></scriptTask>`+
		`<endEvent id="FEnd"/>`,
	`<sequenceFlow id="d3"`,
	`<sequenceFlow id="f1" sourceRef="Repair" targetRef="F"/><sequenceFlow id="f2" sourceRef="F" targetRef="FEnd"/><sequenceFlow id="d3"`,
).Replace(laptopLifecycleBPMN)

// TestAPerOperationActionStartsItsProcess: on a per-operation product the action act
// enters the lifecycle process at the action's start event, records the instance on
// the line under the action's key, and refuses a line that is not held yet.
func TestAPerOperationActionStartsItsProcess(t *testing.T) {
	ts, admin, _, cat := aLifecycleOrder(t)
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", repairingLaptopBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	repair := `,{"key":"repair","message":"laptop.repair","effect":"service","triggers":["customer"],"labels":{"de":"Reparieren"}}`
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products", laptopActions(cat, repair)); code != http.StatusOK {
		t.Fatalf("save product: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+idOf(t, body)+`","items":["laptop"]}`)
	if code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, body)
	}
	ord := idOf(t, body)
	repairIt := "/api/v1/orders/" + ord + "/lines/laptop/actions/repair"
	if code, b := cReq(t, admin, ts, "POST", repairIt, `{"commandId":"f-1"}`); code != http.StatusConflict || !strings.Contains(string(b), "only of a held right") {
		t.Fatalf("repair before it is held: %d (%s), want 409", code, b)
	}

	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/start", `{"operation":"provision"}`)
	var started startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &started) != nil {
		t.Fatalf("start: %d (%s)", code, body)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord+"/lines/laptop/actions", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"key":"repair"`) || !strings.Contains(string(body), `"available":true`) {
		t.Fatalf("actions of a held per-operation line: %d (%s), want repair available", code, body)
	}
	code, body = cReq(t, admin, ts, "POST", repairIt, `{"commandId":"f-1","reason":"cracked screen"}`)
	var took struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &took) != nil || took.InstanceKey == 0 || took.InstanceKey == started.InstanceKey {
		t.Fatalf("repair: %d (%s), want a new instance", code, body)
	}
	if vars := variablesOf(t, admin, ts, took.InstanceKey); !strings.Contains(vars, `"repairedFor"`) || !strings.Contains(vars, "cracked screen") {
		t.Fatalf("the repair instance ran %s", vars)
	}
	code, body = cReq(t, admin, ts, "POST", repairIt, `{"commandId":"f-1","reason":"cracked screen"}`)
	var again struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &again) != nil || again.InstanceKey != took.InstanceKey {
		t.Fatalf("retried repair: %d (%s), want the first instance %d", code, body, took.InstanceKey)
	}
	if got := instancesOf(t, ts, admin, ord); len(got["repair"]) != 1 || got["repair"][0] != took.InstanceKey {
		t.Fatalf("instances = %v, want the repair recorded once", got)
	}
}

// TestAnActionAskedAsATriggerIsCheckedByTheServer: a caller may name which of the
// action's triggers it asks as — the MCP tool always names operator or system. The
// server refuses a trigger the action does not declare, an operator's or the system's
// trigger from somebody who is not an operator, and a trigger that does not exist, so
// a customer's action cannot be asked as the system to get around who it is for.
func TestAnActionAskedAsATriggerIsCheckedByTheServer(t *testing.T) {
	ts, admin, rita, _, ord, strand := aToolHeldForRita(t, `["customer","operator"]`)
	act := func(c *http.Client, key, body string) (int, []byte) {
		return cReq(t, c, ts, "POST", "/api/v1/orders/"+ord+"/lines/tool/actions/"+key, body)
	}

	if code, b := act(admin, "password-reset", `{"commandId":"r-1","trigger":"system"}`); code != http.StatusForbidden ||
		!strings.Contains(string(b), "not by system") {
		t.Fatalf("a customer's action asked as the system: %d (%s), want 403", code, b)
	}
	if code, b := act(rita, "audit", `{"commandId":"a-1","trigger":"operator"}`); code != http.StatusForbidden ||
		!strings.Contains(string(b), "only an operator may ask as operator") {
		t.Fatalf("rita asks as an operator: %d (%s), want 403", code, b)
	}
	if code, b := act(admin, "audit", `{"commandId":"a-1","trigger":"robot"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown trigger: %d (%s), want 400", code, b)
	}
	if vars := variablesOf(t, admin, ts, strand); strings.Contains(vars, `"audited"`) || strings.Contains(vars, `"resetFor"`) {
		t.Fatalf("a refused ask reached the strand: %s", vars)
	}

	if code, b := act(admin, "audit", `{"commandId":"a-1","trigger":"operator"}`); code != http.StatusOK {
		t.Fatalf("an operator asks as operator: %d (%s)", code, b)
	}
	if code, b := act(rita, "password-reset", `{"commandId":"r-2","trigger":"customer","reason":"locked out"}`); code != http.StatusOK {
		t.Fatalf("rita asks as the customer: %d (%s)", code, b)
	}
	if vars := variablesOf(t, admin, ts, strand); !strings.Contains(vars, `"audited"`) || !strings.Contains(vars, "locked out") {
		t.Fatalf("the strand did not run both: %s", vars)
	}
}
