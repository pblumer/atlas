package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// objectGraphBPMN builds one Order carrying its customer's key and its lines
// inside its own value, plus a separate Customer object — everything an object
// diagram has to resolve: containment, a key reference, and a data state.
const objectGraphBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <itemDefinition id="ItemDefinition_Order" structureRef="Order"/>
  <itemDefinition id="ItemDefinition_Customer" structureRef="Customer"/>
  <process id="sales" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="ItemDefinition_Order"><dataState name="received"/></dataObject>
    <dataObject id="DO_buyer" name="buyer" itemSubjectRef="ItemDefinition_Customer"/>
    <dataObjectReference id="Ref_order" name="order" dataObjectRef="DO_order"><dataState name="approved"/></dataObjectReference>
    <dataObjectReference id="Ref_buyer" name="buyer" dataObjectRef="DO_buyer"/>
    <startEvent id="s"/>
    <scriptTask id="seed">
      <extensionElements><zeebe:script expression="= {id: &quot;ORD-1&quot;, customer: &quot;C-7&quot;, lines: [{quantity: 2}]}" resultVariable="orderValue"/></extensionElements>
    </scriptTask>
    <scriptTask id="seedBuyer">
      <extensionElements><zeebe:script expression="= {number: &quot;C-7&quot;, name: &quot;Acme&quot;}" resultVariable="buyerValue"/></extensionElements>
    </scriptTask>
    <task id="record">
      <dataOutputAssociation id="d1"><targetRef>Ref_order</targetRef><assignment><from>= orderValue</from></assignment></dataOutputAssociation>
      <dataOutputAssociation id="d2"><targetRef>Ref_buyer</targetRef><assignment><from>= buyerValue</from></assignment></dataOutputAssociation>
    </task>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="seedBuyer"/>
    <sequenceFlow id="f3" sourceRef="seedBuyer" targetRef="record"/>
    <sequenceFlow id="f4" sourceRef="record" targetRef="w"/>
    <sequenceFlow id="f5" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

type objectGraphResp struct {
	Nodes []struct {
		ID         string `json:"id"`
		Label      string `json:"label"`
		Class      string `json:"class"`
		State      string `json:"state"`
		Key        string `json:"key"`
		Nested     bool   `json:"nested"`
		Unset      bool   `json:"unset"`
		Attributes []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Key    bool   `json:"key"`
			Absent bool   `json:"absent"`
		} `json:"attributes"`
	} `json:"nodes"`
	Links []struct {
		From, To, Kind, Label, Via string
	} `json:"links"`
	Unresolved []struct {
		From, Role, Class, Value string
	} `json:"unresolved"`
	Degraded  bool `json:"degraded"`
	Truncated bool `json:"truncated"`
}

// salesApplicationWithModel creates an application whose information model has an
// Order that owns OrderLines and refers to a Customer by its number.
func salesApplicationWithModel(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Sales"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil || app.ID == "" {
		t.Fatalf("decode application: %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/infomodel/models",
		fmt.Sprintf(`{"applicationId":%q,"name":"Sales data"}`, app.ID), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create model: status=%d body=%s", code, body)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode model: %v", err)
	}
	const content = `{
	  "classes":[
	    {"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
	     "attributes":[{"name":"id","type":"string","multiplicity":"1"},
	                   {"name":"customer","type":"string","multiplicity":"0..1"},
	                   {"name":"total","type":"number","multiplicity":"0..1"}]},
	    {"id":"c2","name":"OrderLine","stereotype":"businessObject",
	     "attributes":[{"name":"quantity","type":"number","multiplicity":"1"}]},
	    {"id":"c3","name":"Customer","stereotype":"businessObject","identity":["number"],
	     "attributes":[{"name":"number","type":"string","multiplicity":"1"},
	                   {"name":"name","type":"string","multiplicity":"1"}]}
	  ],
	  "associations":[
	    {"id":"a1","kind":"composition","from":{"classId":"c1","multiplicity":"1"},
	     "to":{"classId":"c2","role":"lines","multiplicity":"0..*"}},
	    {"id":"a2","kind":"association","from":{"classId":"c3","role":"customer","multiplicity":"1"},
	     "to":{"classId":"c1","role":"orders","multiplicity":"0..*"}}
	  ]}`
	if code, b := doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+m.ID, content, "application/json"); code != http.StatusOK {
		t.Fatalf("save model: status=%d body=%s", code, b)
	}
	return app.ID
}

// startObjectGraphInstance deploys the fixture under an application and returns the
// parked instance's key.
func startObjectGraphInstance(t *testing.T, ts *httptest.Server, appID string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+appID, objectGraphBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, b)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	for _, in := range instances {
		if in.State == "active" {
			return in.Key
		}
	}
	t.Fatal("no active instance")
	return 0
}

func readObjectGraph(t *testing.T, ts *httptest.Server, key uint64) objectGraphResp {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/object-graph", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("object graph: status=%d body=%s", code, body)
	}
	var g objectGraphResp
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return g
}

// TestInstanceObjectGraph is slice 4 end to end: an instance's data drawn as
// objects, with the two kinds of line an object diagram can carry — a part inside
// its whole, and a reference matched on a business key.
func TestInstanceObjectGraph(t *testing.T) {
	ts := newTestServer(t)
	appID := salesApplicationWithModel(t, ts)
	key := startObjectGraphInstance(t, ts, appID)

	g := readObjectGraph(t, ts, key)
	if g.Degraded {
		t.Error("the application has a model; the graph must not report itself degraded")
	}
	byID := map[string]int{}
	for i, n := range g.Nodes {
		byID[n.ID] = i
	}

	order, ok := byID["order"]
	if !ok {
		t.Fatalf("no order node: %+v", g.Nodes)
	}
	if g.Nodes[order].Label != "order : Order" {
		t.Errorf("label = %q, want the UML object reading", g.Nodes[order].Label)
	}
	// The business key is what makes this object *this* order, and what the
	// reference from another object has to match.
	if g.Nodes[order].Key != "ORD-1" {
		t.Errorf("key = %q, want ORD-1", g.Nodes[order].Key)
	}
	if g.Nodes[order].State != "approved" {
		t.Errorf("state = %q, want the data state the write advanced it to", g.Nodes[order].State)
	}
	// An attribute the class declares and the value does not carry is absent, not
	// blank — "not set" and "set to empty" are different facts.
	for _, a := range g.Nodes[order].Attributes {
		if a.Name == "total" && !a.Absent {
			t.Errorf("total = %+v, want it marked absent", a)
		}
		if a.Name == "id" && !a.Key {
			t.Error("the key attribute is not marked")
		}
	}

	// Containment: the line lives inside the order's own value, so it is an object
	// of this instance.
	line, ok := byID["order.lines[0]"]
	if !ok {
		t.Fatalf("no nested line node: %+v", g.Nodes)
	}
	if g.Nodes[line].Class != "OrderLine" || !g.Nodes[line].Nested {
		t.Errorf("line node = %+v", g.Nodes[line])
	}

	var containment, reference bool
	for _, l := range g.Links {
		if l.From == "order" && l.To == "order.lines[0]" && l.Kind == "composition" && l.Via == "containment" {
			containment = true
		}
		if l.From == "order" && l.To == "buyer" && l.Via == "key" && l.Label == "customer" {
			reference = true
		}
	}
	if !containment {
		t.Errorf("no containment link: %+v", g.Links)
	}
	if !reference {
		t.Errorf("no key-resolved link from the order to its customer: %+v", g.Links)
	}
	if len(g.Unresolved) != 0 {
		t.Errorf("unresolved = %+v, want none — both objects are here", g.Unresolved)
	}
}

// TestInstanceObjectGraphWithoutAModelDegrades covers the honest fallback: an
// application that models nothing still gets its objects drawn, without the
// structure a model would give them, and the graph says so.
func TestInstanceObjectGraphWithoutAModelDegrades(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", objectGraphBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, b)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	var key uint64
	for _, in := range instances {
		if in.State == "active" {
			key = in.Key
		}
	}

	g := readObjectGraph(t, ts, key)
	if !g.Degraded {
		t.Error("an unmodelled application must say the picture is showing less than it could")
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want the two objects the instance carries", g.Nodes)
	}
	for _, n := range g.Nodes {
		if n.Class != "" {
			t.Errorf("a class was invented without a model: %+v", n)
		}
	}
	if len(g.Links) != 0 {
		t.Errorf("links without a model: %+v", g.Links)
	}
}

// TestInstanceObjectGraphUnknownInstance covers the convenience-read posture the
// other instance endpoints take: an empty picture, not a 404.
func TestInstanceObjectGraphUnknownInstance(t *testing.T) {
	ts := newTestServer(t)
	g := readObjectGraph(t, ts, 999)
	if len(g.Nodes) != 0 || len(g.Links) != 0 {
		t.Errorf("graph = %+v, want empty", g)
	}
}

// TestInstanceObjectGraphInvalidKey rejects a non-numeric instance key.
func TestInstanceObjectGraphInvalidKey(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/not-a-number/object-graph", "", ""); code != http.StatusBadRequest {
		t.Errorf("non-numeric key: %d, want 400", code)
	}
}
