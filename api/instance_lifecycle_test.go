package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ADR-0259 §4: the declared state machine with the life one instance actually had
// drawn on it. Everything it needs was already on disk — the class's lifecycle in
// the information model, the object's state trail in the log, with the element that
// made each write — so this endpoint adds no fact, it reads two that existed against
// each other. These tests are end to end because that is the only place the claim
// can be checked: the trail has to come from a process that really ran.

// lifecycleBPMN moves one order twice — received → approved → shipped — by writing it
// through two references that carry different data states, which is how BPMN advances
// a data state at all. The second object is deliberately of a class with no lifecycle,
// so the endpoint has something it must stay silent about.
const lifecycleBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <itemDefinition id="ItemDefinition_Order" structureRef="Order"/>
  <itemDefinition id="ItemDefinition_Customer" structureRef="Customer"/>
  <process id="sales" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="ItemDefinition_Order"><dataState name="received"/></dataObject>
    <dataObject id="DO_buyer" name="buyer" itemSubjectRef="ItemDefinition_Customer"/>
    <dataObjectReference id="Ref_approved" name="order" dataObjectRef="DO_order"><dataState name="approved"/></dataObjectReference>
    <dataObjectReference id="Ref_shipped" name="order" dataObjectRef="DO_order"><dataState name="shipped"/></dataObjectReference>
    <dataObjectReference id="Ref_buyer" name="buyer" dataObjectRef="DO_buyer"/>
    <startEvent id="s"/>
    <scriptTask id="seed">
      <extensionElements><zeebe:script expression="= {id: &quot;ORD-1&quot;, customer: &quot;C-7&quot;}" resultVariable="orderValue"/></extensionElements>
    </scriptTask>
    <task id="approve">
      <dataOutputAssociation id="d1"><targetRef>Ref_approved</targetRef><assignment><from>= orderValue</from></assignment></dataOutputAssociation>
      <dataOutputAssociation id="d2"><targetRef>Ref_buyer</targetRef><assignment><from>= {number: "C-7", name: "Acme"}</from></assignment></dataOutputAssociation>
    </task>
    <task id="ship">
      <dataOutputAssociation id="d3"><targetRef>Ref_shipped</targetRef><assignment><from>= orderValue</from></assignment></dataOutputAssociation>
    </task>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="approve"/>
    <sequenceFlow id="f3" sourceRef="approve" targetRef="ship"/>
    <sequenceFlow id="f4" sourceRef="ship" targetRef="w"/>
    <sequenceFlow id="f5" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// orderLifecycleModel is the Sales model with a lifecycle on Order and none on
// Customer — the normal mix, and the one that proves silence is per class.
const orderLifecycleModel = `{
  "classes":[
    {"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
     "attributes":[{"name":"id","type":"string","multiplicity":"1"},
                   {"name":"customer","type":"string","multiplicity":"0..1"}],
     "lifecycle":{
       "states":[{"name":"received","initial":true,"x":0,"y":0},
                 {"name":"approved","x":0,"y":90},
                 {"name":"shipped","final":true,"x":0,"y":180},
                 {"name":"cancelled","final":true,"x":220,"y":90}],
       "transitions":[{"id":"t1","name":"approve","from":"received","to":"approved"},
                      {"id":"t2","name":"ship","from":"approved","to":"shipped"},
                      {"id":"t3","name":"cancel","from":"received","to":"cancelled"}]}},
    {"id":"c3","name":"Customer","stereotype":"businessObject","identity":["number"],
     "attributes":[{"name":"number","type":"string","multiplicity":"1"},
                   {"name":"name","type":"string","multiplicity":"1"}]}
  ],
  "associations":[]}`

type lifecycleTraceResp struct {
	Object  string `json:"object"`
	Class   string `json:"class"`
	Current string `json:"current"`
	States  []struct {
		Name    string `json:"name"`
		Initial bool   `json:"initial"`
		Final   bool   `json:"final"`
		Visited bool   `json:"visited"`
		Current bool   `json:"current"`
		Entered int    `json:"entered"`
	} `json:"states"`
	Transitions []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		From   string `json:"from"`
		To     string `json:"to"`
		Taken  int    `json:"taken"`
		LastBy string `json:"lastBy"`
	} `json:"transitions"`
	Undeclared []struct {
		From string `json:"from"`
		To   string `json:"to"`
		By   string `json:"by"`
	} `json:"undeclared"`
	Unknown []string `json:"unknown"`
}

// lifecycleApplication creates the Sales application with the model above.
func lifecycleApplication(t *testing.T, ts *httptest.Server, content string) string {
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
	if code, b := doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+m.ID, content, "application/json"); code != http.StatusOK {
		t.Fatalf("save model: status=%d body=%s", code, b)
	}
	return app.ID
}

func startLifecycleInstance(t *testing.T, ts *httptest.Server, appID string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+appID, lifecycleBPMN, "application/xml")
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

func readLifecycle(t *testing.T, ts *httptest.Server, key uint64) []lifecycleTraceResp {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/lifecycle", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("lifecycle: status=%d body=%s", code, body)
	}
	var out []lifecycleTraceResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

func TestInstanceLifecycleDrawsTheDeclaredMachineWithThisInstanceOnIt(t *testing.T) {
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, orderLifecycleModel)
	key := startLifecycleInstance(t, ts, appID)

	traces := readLifecycle(t, ts, key)
	// Only the order: Customer declares no lifecycle, and a class without one is
	// silent everywhere — including here.
	if len(traces) != 1 {
		t.Fatalf("traces = %d, want 1 (only the object whose class declares a lifecycle): %+v", len(traces), traces)
	}
	tr := traces[0]
	if tr.Object != "order" || tr.Class != "Order" {
		t.Errorf("object/class = %q/%q, want order/Order", tr.Object, tr.Class)
	}
	// The whole machine, not just what was used: four states and three transitions,
	// including the way out this instance never took.
	if len(tr.States) != 4 || len(tr.Transitions) != 3 {
		t.Fatalf("states/transitions = %d/%d, want 4/3", len(tr.States), len(tr.Transitions))
	}
	if tr.Current != "shipped" {
		t.Errorf("current = %q, want shipped", tr.Current)
	}
	visited := map[string]bool{}
	for _, s := range tr.States {
		visited[s.Name] = s.Visited
	}
	for _, want := range []string{"received", "approved", "shipped"} {
		if !visited[want] {
			t.Errorf("%s: visited = false, want true", want)
		}
	}
	if visited["cancelled"] {
		t.Error("cancelled: visited = true, want false — this order was never cancelled")
	}
	if len(tr.Unknown) != 0 || len(tr.Undeclared) != 0 {
		t.Errorf("unknown/undeclared = %v/%v, want none — the process moved it the way the model says", tr.Unknown, tr.Undeclared)
	}
}

func TestInstanceLifecycleNamesTheElementThatMadeEachMove(t *testing.T) {
	// The whole reason to draw the overlay rather than read the trail as a list: an
	// edge that says which element in the process moved the datum along it.
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, orderLifecycleModel)
	key := startLifecycleInstance(t, ts, appID)

	traces := readLifecycle(t, ts, key)
	if len(traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(traces))
	}
	by := map[string]string{}
	taken := map[string]int{}
	for _, tt := range traces[0].Transitions {
		by[tt.ID] = tt.LastBy
		taken[tt.ID] = tt.Taken
	}
	if taken["t1"] != 1 || by["t1"] != "approve" {
		t.Errorf("t1 (received→approved): taken/by = %d/%q, want 1/approve", taken["t1"], by["t1"])
	}
	if taken["t2"] != 1 || by["t2"] != "ship" {
		t.Errorf("t2 (approved→shipped): taken/by = %d/%q, want 1/ship", taken["t2"], by["t2"])
	}
	if taken["t3"] != 0 {
		t.Errorf("t3 (received→cancelled): taken = %d, want 0", taken["t3"])
	}
}

func TestInstanceLifecycleIsSilentWhereNothingIsDeclared(t *testing.T) {
	// The same process against a model with no lifecycle on anything: nothing to draw,
	// and an empty list rather than an error or a machine with no states.
	const noLifecycle = `{
	  "classes":[
	    {"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
	     "attributes":[{"name":"id","type":"string","multiplicity":"1"}]},
	    {"id":"c3","name":"Customer","stereotype":"businessObject","identity":["number"],
	     "attributes":[{"name":"number","type":"string","multiplicity":"1"}]}
	  ],"associations":[]}`
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, noLifecycle)
	key := startLifecycleInstance(t, ts, appID)

	if traces := readLifecycle(t, ts, key); len(traces) != 0 {
		t.Errorf("traces = %+v, want none", traces)
	}
}

func TestInstanceLifecycleReportsAMoveTheModelDoesNotAllow(t *testing.T) {
	// The run-time twin of the `data.illegal-transition` deploy check. Here the model
	// declares no way from received to shipped, and the process makes that move
	// anyway — which is exactly what happens when a lifecycle is drawn after the
	// processes that write it already exist.
	const gapModel = `{
	  "classes":[
	    {"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
	     "attributes":[{"name":"id","type":"string","multiplicity":"1"}],
	     "lifecycle":{
	       "states":[{"name":"received","initial":true,"x":0,"y":0},
	                 {"name":"shipped","final":true,"x":0,"y":90}],
	       "transitions":[]}},
	    {"id":"c3","name":"Customer","stereotype":"businessObject","identity":["number"],
	     "attributes":[{"name":"number","type":"string","multiplicity":"1"}]}
	  ],"associations":[]}`
	ts := newTestServer(t)
	appID := lifecycleApplication(t, ts, gapModel)
	key := startLifecycleInstance(t, ts, appID)

	traces := readLifecycle(t, ts, key)
	if len(traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(traces))
	}
	tr := traces[0]
	// `approved` is not a state this model declares, so it is named rather than
	// swallowed — and the two moves through it are moves the machine does not join.
	if len(tr.Unknown) != 1 || tr.Unknown[0] != "approved" {
		t.Errorf("unknown = %v, want [approved]", tr.Unknown)
	}
	if len(tr.Undeclared) != 2 {
		t.Fatalf("undeclared = %+v, want 2 (received→approved and approved→shipped)", tr.Undeclared)
	}
	if tr.Undeclared[0].From != "received" || tr.Undeclared[0].To != "approved" || tr.Undeclared[0].By != "approve" {
		t.Errorf("undeclared[0] = %+v, want received→approved by approve", tr.Undeclared[0])
	}
	if tr.Undeclared[1].By != "ship" {
		t.Errorf("undeclared[1].by = %q, want ship", tr.Undeclared[1].By)
	}
}

func TestInstanceLifecycleRefusesAKeyThatIsNotOne(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/not-a-number/lifecycle", "", ""); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	// An instance that does not exist carries no data objects, so it has no lifecycle
	// to draw — an empty list, the same answer the data-objects endpoint gives, rather
	// than a 404 that would make an ordinary tab read as an error.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/999/lifecycle", "", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", code, body)
	}
	if string(body) != "[]\n" && string(body) != "[]" {
		t.Errorf("body = %s, want an empty array", body)
	}
}
