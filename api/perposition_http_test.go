package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A per-position lifecycle: provisioning starts the strand, which waits for its
// return under the position's key; a return with no strand to reach starts at the
// deprovision start event and joins the same return path
// (ADR-0428).
const hullStrandBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m_prov" name="hull.provision"/>
  <message id="m_deprov" name="hull.deprovision">
    <extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>
  </message>
  <process id="hull-strand" isExecutable="true">
    <startEvent id="Provision"><messageEventDefinition messageRef="m_prov"/></startEvent>
    <scriptTask id="P"><extensionElements><zeebe:script expression="=true" resultVariable="ranProvision"/></extensionElements></scriptTask>
    <intermediateCatchEvent id="Held"><messageEventDefinition messageRef="m_deprov"/></intermediateCatchEvent>
    <startEvent id="Fallback"><messageEventDefinition messageRef="m_deprov"/></startEvent>
    <exclusiveGateway id="Return"/>
    <scriptTask id="D"><extensionElements><zeebe:script expression="=true" resultVariable="ranDeprovision"/></extensionElements></scriptTask>
    <endEvent id="End"/>
    <sequenceFlow id="p1" sourceRef="Provision" targetRef="P"/>
    <sequenceFlow id="p2" sourceRef="P" targetRef="Held"/>
    <sequenceFlow id="p3" sourceRef="Held" targetRef="Return"/>
    <sequenceFlow id="f1" sourceRef="Fallback" targetRef="Return"/>
    <sequenceFlow id="d1" sourceRef="Return" targetRef="D"/>
    <sequenceFlow id="d2" sourceRef="D" targetRef="End"/>
  </process>
</definitions>`

// aPerPositionCatalogue deploys the strand, binds a per-position product to it and
// publishes, returning the release to order from.
func aPerPositionCatalogue(t *testing.T) (ts *httptest.Server, admin *http.Client, release string) {
	t.Helper()
	ts, _ = newAuthServerWith(t, "root", "rootpassword")
	admin = newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", hullStrandBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"hull","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Hülle"},`+
			`"approval":{"kind":"none"},"lifecycleProcess":"hull-strand","lifecycleForm":"per-position",`+
			`"operations":{"provision":"hull.provision","deprovision":"hull.deprovision"}}`); code != http.StatusOK {
		t.Fatalf("save: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["hull"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	return ts, admin, idOf(t, body)
}

// aHeldHull orders the hull, provisions it through the start act and reports it
// done, returning the order and the strand instance.
func aHeldHull(t *testing.T, ts *httptest.Server, admin *http.Client, release string) (string, uint64) {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+release+`","items":["hull"]}`)
	if code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, body)
	}
	ord := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/hull/start", `{"operation":"provision"}`)
	var started startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &started) != nil || started.InstanceKey == 0 {
		t.Fatalf("start: %d (%s)", code, body)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/hull", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	return ord, started.InstanceKey
}

// instancesOf reads the instances an order recorded on its one line, by operation.
func instancesOf(t *testing.T, ts *httptest.Server, admin *http.Client, ord string) map[string][]uint64 {
	t.Helper()
	code, body := cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord, "")
	if code != http.StatusOK {
		t.Fatalf("order: %d (%s)", code, body)
	}
	var o struct {
		Lines []struct {
			Instances []struct {
				Key       uint64 `json:"key"`
				Operation string `json:"operation"`
			} `json:"instances"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &o); err != nil || len(o.Lines) != 1 {
		t.Fatalf("decode order: %v (%s)", err, body)
	}
	out := map[string][]uint64{}
	for _, in := range o.Lines[0].Instances {
		out[in.Operation] = append(out[in.Operation], in.Key)
	}
	return out
}

func instanceState(t *testing.T, ts *httptest.Server, admin *http.Client, key uint64) string {
	t.Helper()
	code, body := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/timeline", key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline of %d: %d (%s)", key, code, body)
	}
	var tl struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}
	return tl.State
}

// TestAPerPositionReturnReachesTheStrand: the return of a held per-position line is
// delivered to the instance its provisioning started — no second instance — which
// takes the return path and ends; and the product's return message cannot be
// published by name.
func TestAPerPositionReturnReachesTheStrand(t *testing.T) {
	ts, admin, rel := aPerPositionCatalogue(t)
	ord, strand := aHeldHull(t, ts, admin, rel)

	if vars := variablesOf(t, admin, ts, strand); !strings.Contains(vars, "ranProvision") || strings.Contains(vars, "ranDeprovision") {
		t.Fatalf("after provisioning the strand holds %s, want provision only", vars)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/messages",
		`{"name":"hull.deprovision","correlationKey":"`+ord+`/hull"}`)
	if code != http.StatusConflict || !strings.Contains(string(body), "product hull") {
		t.Fatalf("publish by name: %d (%s), want 409 naming the product", code, body)
	}

	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/hull/return", ``); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	got := instancesOf(t, ts, admin, ord)
	if len(got["deprovision"]) != 1 || got["deprovision"][0] != strand {
		t.Fatalf("instances = %v, want the return recorded on the strand %d", got, strand)
	}
	if vars := variablesOf(t, admin, ts, strand); !strings.Contains(vars, "ranDeprovision") {
		t.Fatalf("the strand did not take the return: %s", vars)
	}
	if st := instanceState(t, ts, admin, strand); st != "completed" {
		t.Fatalf("strand state = %q, want completed", st)
	}
}

// TestAPerPositionReturnWithoutAStrandStartsTheFallback: when the instance that
// carried the position is gone — here an operator cancelled it — the return starts
// the process at its deprovision start event, so the right is still returned.
func TestAPerPositionReturnWithoutAStrandStartsTheFallback(t *testing.T) {
	ts, admin, rel := aPerPositionCatalogue(t)
	ord, strand := aHeldHull(t, ts, admin, rel)
	if code, b := cReq(t, admin, ts, "DELETE", fmt.Sprintf("/api/v1/instances/%d", strand), ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("cancel the strand: %d (%s)", code, b)
	}

	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/hull/return", ``); code != http.StatusOK {
		t.Fatalf("return: %d (%s)", code, b)
	}
	got := instancesOf(t, ts, admin, ord)
	if len(got["deprovision"]) != 1 || got["deprovision"][0] == strand {
		t.Fatalf("instances = %v, want a new instance for the return", got)
	}
	if vars := variablesOf(t, admin, ts, got["deprovision"][0]); !strings.Contains(vars, "ranDeprovision") || strings.Contains(vars, "ranProvision") {
		t.Fatalf("the fallback ran %s, want the return path only", vars)
	}
}

// TestPublishingChecksWhatAPerPositionStrandOwes: a per-position product bound to a
// process that never waits for its return, or waits for it without a key, or
// circles without waiting, is refused with each reason; an unknown form and a form
// without a lifecycle process are refused too.
func TestPublishingChecksWhatAPerPositionStrandOwes(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	uncorrelated := strings.Replace(hullStrandBPMN,
		`<extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>`, ``, 1)
	uncorrelated = strings.ReplaceAll(uncorrelated, `hull-strand`, `hull-loose`)
	circling := strings.NewReplacer(`hull-strand`, `hull-circle`,
		`<sequenceFlow id="d2" sourceRef="D" targetRef="End"/>`,
		`<sequenceFlow id="d2" sourceRef="D" targetRef="End"><conditionExpression>=false</conditionExpression></sequenceFlow>`+
			`<sequenceFlow id="d3" sourceRef="D" targetRef="Return"><conditionExpression>=true</conditionExpression></sequenceFlow>`).Replace(hullStrandBPMN)
	for _, xml := range []string{laptopLifecycleBPMN, uncorrelated, circling} {
		if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", xml); code != http.StatusOK {
			t.Fatalf("deploy: %d (%s)", code, b)
		}
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	save := func(id, binding string) {
		t.Helper()
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
			`{"id":"`+id+`","homeCatalog":"`+cat+`","state":"active","texts":{"de":"X"},"approval":{"kind":"none"},`+
				binding+`}`); code != http.StatusOK {
			t.Fatalf("save %s: %d (%s)", id, code, b)
		}
	}
	perPosition := func(process, prov, deprov string) string {
		return `"lifecycleProcess":"` + process + `","lifecycleForm":"per-position",` +
			`"operations":{"provision":"` + prov + `","deprovision":"` + deprov + `"}`
	}
	save("never-caught", perPosition("laptop-lifecycle", "laptop.provision", "laptop.deprovision"))
	save("loose", perPosition("hull-loose", "hull.provision", "hull.deprovision"))
	save("circle", perPosition("hull-circle", "hull.provision", "hull.deprovision"))
	save("odd-form", `"lifecycleProcess":"hull-circle","lifecycleForm":"sometimes",`+
		`"operations":{"provision":"hull.provision","deprovision":"hull.deprovision"}`)
	save("form-only", `"provisionProcess":"p","deprovisionProcess":"d","lifecycleForm":"per-position"`)
	// The catalogue's own rules are checked before anything is asked of the engine,
	// so the two kinds are published in two rounds.
	publish := func(items string, want ...string) {
		t.Helper()
		if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":[`+items+`]}`); code != http.StatusOK {
			t.Fatalf("offer: %d (%s)", code, b)
		}
		code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("publish %s: %d (%s), want 422", items, code, body)
		}
		for _, w := range want {
			if !strings.Contains(string(body), w) {
				t.Errorf("publish answer lacks %q: %s", w, body)
			}
		}
	}
	publish(`"odd-form","form-only"`,
		"lifecycle form sometimes is not one",
		"names a lifecycle form but binds no lifecycle process")
	publish(`"never-caught","loose","circle"`,
		"laptop-lifecycle never waits for",
		"Held waits for hull.deprovision without a correlation key",
		"hull-circle circles through")
}

// TestAPerPositionReturnTheStrandCannotTakeIsRefused: a strand still busy in a step
// that does not listen for the return answers 409 and starts nothing beside it.
func TestAPerPositionReturnTheStrandCannotTakeIsRefused(t *testing.T) {
	busy := strings.NewReplacer(
		`<sequenceFlow id="p2" sourceRef="P" targetRef="Held"/>`,
		`<sequenceFlow id="p2" sourceRef="P" targetRef="Busy"/><sequenceFlow id="p2b" sourceRef="Busy" targetRef="Held"/>`,
		`<intermediateCatchEvent id="Held">`,
		`<userTask id="Busy"/><intermediateCatchEvent id="Held">`,
	).Replace(hullStrandBPMN)
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", busy); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"hull","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Hülle"},`+
			`"approval":{"kind":"none"},"lifecycleProcess":"hull-strand","lifecycleForm":"per-position",`+
			`"operations":{"provision":"hull.provision","deprovision":"hull.deprovision"}}`); code != http.StatusOK {
		t.Fatalf("save: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["hull"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	ord, strand := aHeldHull(t, ts, admin, idOf(t, body))

	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/hull/return", ``)
	if code != http.StatusConflict || !strings.Contains(string(body), "does not wait for hull.deprovision") {
		t.Fatalf("return while busy: %d (%s), want 409", code, body)
	}
	if got := instancesOf(t, ts, admin, ord); len(got["deprovision"]) != 0 || len(got["provision"]) != 1 || got["provision"][0] != strand {
		t.Fatalf("instances = %v, want the strand alone and no return recorded", got)
	}
}

// A strand that waits at an event-based gateway for a change or its return; a change
// runs its step and goes back to waiting.
const changingStrandBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m_prov" name="kit.provision"/>
  <message id="m_deprov" name="kit.deprovision">
    <extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>
  </message>
  <message id="m_change" name="kit.change">
    <extensionElements><zeebe:subscription correlationKey="=orderId + &#34;/&#34; + positionId"/></extensionElements>
  </message>
  <process id="kit-strand" isExecutable="true">
    <startEvent id="Provision" name="Ordered"><messageEventDefinition messageRef="m_prov"/></startEvent>
    <exclusiveGateway id="Wait"/>
    <eventBasedGateway id="Gate" name="Held"/>
    <intermediateCatchEvent id="AskChange" name="Change asked"><messageEventDefinition messageRef="m_change"/></intermediateCatchEvent>
    <scriptTask id="C"><extensionElements><zeebe:script expression="=size" resultVariable="changedTo"/></extensionElements></scriptTask>
    <intermediateCatchEvent id="AskReturn" name="Return asked"><messageEventDefinition messageRef="m_deprov"/></intermediateCatchEvent>
    <startEvent id="Fallback"><messageEventDefinition messageRef="m_deprov"/></startEvent>
    <exclusiveGateway id="Return"/>
    <endEvent id="End"/>
    <sequenceFlow id="s1" sourceRef="Provision" targetRef="Wait"/>
    <sequenceFlow id="s2" sourceRef="Wait" targetRef="Gate"/>
    <sequenceFlow id="s3" sourceRef="Gate" targetRef="AskChange"/>
    <sequenceFlow id="s4" sourceRef="AskChange" targetRef="C"/>
    <sequenceFlow id="s5" sourceRef="C" targetRef="Wait"/>
    <sequenceFlow id="s6" sourceRef="Gate" targetRef="AskReturn"/>
    <sequenceFlow id="s7" sourceRef="AskReturn" targetRef="Return"/>
    <sequenceFlow id="s8" sourceRef="Fallback" targetRef="Return"/>
    <sequenceFlow id="s9" sourceRef="Return" targetRef="End"/>
  </process>
</definitions>`

// aHeldKit publishes a per-position product with a change operation, orders it,
// provisions it and reports it done.
func aHeldKit(t *testing.T) (ts *httptest.Server, admin *http.Client, ord string, strand uint64) {
	t.Helper()
	ts, _ = newAuthServerWith(t, "root", "rootpassword")
	admin = newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", changingStrandBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"A"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"kit","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Kit"},`+
			`"approval":{"kind":"none"},"lifecycleProcess":"kit-strand","lifecycleForm":"per-position",`+
			`"operations":{"provision":"kit.provision","deprovision":"kit.deprovision","change":"kit.change"}}`); code != http.StatusOK {
		t.Fatalf("save: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["kit"]}`); code != http.StatusOK {
		t.Fatalf("offer: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders", `{"releaseId":"`+idOf(t, body)+`","items":["kit"]}`)
	if code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, body)
	}
	ord = idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/kit/start", `{"operation":"provision"}`)
	var started startAnswer
	if code != http.StatusOK || json.Unmarshal(body, &started) != nil || started.InstanceKey == 0 {
		t.Fatalf("start: %d (%s)", code, body)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/kit", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	return ts, admin, ord, started.InstanceKey
}

// TestAHeldPositionIsChangedInItsStrand: a change reaches the instance that carries
// the right, runs its step and returns the strand to waiting; a retry with the same
// changeId answers with the first delivery; the position reads as held, not as work
// under way; and a change without an id, or to a strand that is gone, is refused.
func TestAHeldPositionIsChangedInItsStrand(t *testing.T) {
	ts, admin, ord, strand := aHeldKit(t)
	change := "/api/v1/orders/" + ord + "/lines/kit/change"

	code, got, body := progressOf(t, admin, ts, ord, "kit")
	if code != http.StatusOK || got.State != "held" {
		t.Fatalf("progress of a waiting strand: %d (%s), want held", code, body)
	}

	if code, b := cReq(t, admin, ts, "POST", change, `{"reason":"bigger"}`); code != http.StatusBadRequest {
		t.Fatalf("change without an id: %d (%s), want 400", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", change, `{"changeId":"c-1","reason":"bigger","variables":{"size":"L"}}`)
	var took struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &took) != nil || took.InstanceKey != strand {
		t.Fatalf("change: %d (%s), want delivered to %d", code, body, strand)
	}
	if vars := variablesOf(t, admin, ts, strand); !strings.Contains(vars, `"changedTo"`) || !strings.Contains(vars, `"L"`) {
		t.Fatalf("the strand did not run the change: %s", vars)
	}
	if code, b := cReq(t, admin, ts, "POST", change, `{"changeId":"c-1","variables":{"size":"XL"}}`); code != http.StatusOK {
		t.Fatalf("retried change: %d (%s), want the first answer", code, b)
	}
	if vars := variablesOf(t, admin, ts, strand); strings.Contains(vars, `"XL"`) {
		t.Fatalf("a retried change was delivered twice: %s", vars)
	}
	if got := instancesOf(t, ts, admin, ord); len(got["change"]) != 1 || got["change"][0] != strand {
		t.Fatalf("instances = %v, want the change recorded on the strand", got)
	}

	if code, b := cReq(t, admin, ts, "DELETE", fmt.Sprintf("/api/v1/instances/%d", strand), ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("cancel the strand: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", change, `{"changeId":"c-2"}`)
	if code != http.StatusConflict || !strings.Contains(string(body), "no longer running") {
		t.Fatalf("change after the strand is gone: %d (%s), want 409", code, body)
	}
}

// TestAChangeIsOnlyForAHeldPerPositionRight: a line of the per-operation form, and
// a per-position line not yet held, are refused; so is an unknown order.
func TestAChangeIsOnlyForAHeldPerPositionRight(t *testing.T) {
	ts, admin, ord, _ := aLifecycleOrder(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/change", `{"changeId":"c"}`); code != http.StatusConflict {
		t.Fatalf("per-operation line: %d (%s), want 409", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/nope/lines/laptop/change", `{"changeId":"c"}`); code != http.StatusNotFound {
		t.Fatalf("unknown order: %d (%s), want 404", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/laptop/change", `{`); code != http.StatusBadRequest {
		t.Fatalf("malformed body: %d (%s), want 400", code, b)
	}

	ts2, admin2, rel := aPerPositionCatalogue(t)
	code, body := cReq(t, admin2, ts2, "POST", "/api/v1/orders", `{"releaseId":"`+rel+`","items":["hull"]}`)
	if code != http.StatusCreated {
		t.Fatalf("order: %d (%s)", code, body)
	}
	pending := idOf(t, body)
	if code, b := cReq(t, admin2, ts2, "POST", "/api/v1/orders/"+pending+"/lines/hull/change", `{"changeId":"c"}`); code != http.StatusConflict {
		t.Fatalf("per-position product without a change operation: %d (%s), want 409", code, b)
	}
}

// TestRedeployingAPerPositionProcessSaysWhatStaysOnTheOldVersion: a new version of a
// process a per-position product binds is deployed with a warning that counts the
// rights still held on the older one.
func TestRedeployingAPerPositionProcessSaysWhatStaysOnTheOldVersion(t *testing.T) {
	ts, admin, _, _ := aHeldKit(t)
	code, body := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", changingStrandBPMN)
	if code != http.StatusOK || !strings.Contains(string(body), "1 instance(s) of kit-strand still run on older versions") {
		t.Fatalf("redeploy: %d (%s), want the held right counted", code, body)
	}
}
