package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// dataObjectBPMN parks an instance on a long timer so it stays active, and
// declares two data objects — one with an initial data state, one a nameless
// collection — so the data-objects endpoint has something to surface (ADR-0053).
const dataObjectBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="withdata" name="With Data" isExecutable="true">
    <dataObject id="DataObject_order" name="order" isCollection="false">
      <dataState name="received"/>
    </dataObject>
    <dataObject id="DataObject_items" isCollection="true"/>
    <startEvent id="s"/>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestInstanceDataObjects deploys a process with data objects, starts an
// instance, and reads its data objects back — each with its name, declared data
// state, and (unset) value — so an operator sees the data the process carries and
// what state it is in (ADR-0053).
func TestInstanceDataObjects(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", dataObjectBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, b)
	}

	// Find the running instance's key.
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
	if key == 0 {
		t.Fatal("no active instance found")
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get data objects: status=%d body=%s", code, body)
	}
	var got []struct {
		Name  string `json:"name"`
		State string `json:"state"`
		Value any    `json:"value"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode data objects: %v (%s)", err, body)
	}
	// Scanned in name order: DataObject_items (id fallback), then order.
	if len(got) != 2 {
		t.Fatalf("data objects = %d, want 2 (%s)", len(got), body)
	}
	byName := map[string]struct {
		state string
		value any
		kind  string
	}{}
	for _, d := range got {
		byName[d.Name] = struct {
			state string
			value any
			kind  string
		}{d.State, d.Value, d.Kind}
	}
	order, ok := byName["order"]
	if !ok {
		t.Fatalf("no 'order' data object (%s)", body)
	}
	if order.state != "received" {
		t.Errorf("order.state = %q, want received", order.state)
	}
	if order.kind != "null" || order.value != nil {
		t.Errorf("order value = %v kind = %q, want null/nil (unset until associations)", order.value, order.kind)
	}
	if _, ok := byName["DataObject_items"]; !ok {
		t.Errorf("nameless collection not surfaced under its id fallback (%s)", body)
	}
}

// TestInstanceDataObjectsEmpty covers an instance (or unknown key) with no data
// objects: the endpoint returns an empty array, not an error — a convenience read,
// not an existence check, mirroring the variables endpoint.
func TestInstanceDataObjectsEmpty(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/999/data-objects", "", "")
	if code != http.StatusOK {
		t.Fatalf("get data objects for unknown instance: status=%d body=%s", code, body)
	}
	if got := string(bytes.TrimSpace(body)); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

// TestInstanceDataObjectsInvalidKey rejects a non-numeric instance key.
func TestInstanceDataObjectsInvalidKey(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/not-a-number/data-objects", "", "")
	if code != http.StatusBadRequest {
		t.Errorf("non-numeric key: %d, want 400", code)
	}
}

// dataObjectLineageBPMN writes a data object from a task and then parks on a long
// timer, so the instance is still active when the endpoint is read and its "order"
// carries a value an element actually produced. The object declares an
// itemSubjectRef — the type slot BPMN leaves opaque and Atlas surfaces as the
// object's declared class.
const dataObjectLineageBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="withlineage" name="With lineage" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order">
      <dataState name="received"/>
    </dataObject>
    <dataObjectReference id="Ref_write" name="order" dataObjectRef="DO_order">
      <dataState name="approved"/>
    </dataObjectReference>
    <startEvent id="s"/>
    <scriptTask id="seed" name="Seed amount">
      <extensionElements><zeebe:script expression="= 100" resultVariable="amount"/></extensionElements>
    </scriptTask>
    <task id="record" name="Record order">
      <dataOutputAssociation id="doa"><targetRef>Ref_write</targetRef><assignment><from>= amount</from></assignment></dataOutputAssociation>
    </task>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="record"/>
    <sequenceFlow id="f3" sourceRef="record" targetRef="w"/>
    <sequenceFlow id="f4" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// dataObjectRow is the enriched shape the Data view reads: the object's own facts
// (name, state, typed value) plus what only the definition and the log together can
// say — its declared class, whether it is a collection, when it was last written and
// by which element on the diagram, and the trail of every state it passed through.
type dataObjectRow struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Value        any    `json:"value"`
	Kind         string `json:"kind"`
	ItemType     string `json:"itemType"`
	IsCollection bool   `json:"isCollection"`
	At           int64  `json:"at"`
	ProducedBy   string `json:"producedBy"`
	History      []struct {
		At         int64  `json:"at"`
		State      string `json:"state"`
		Kind       string `json:"kind"`
		Value      any    `json:"value"`
		ProducedBy string `json:"producedBy"`
	} `json:"history"`
}

// readDataObjectRows starts an instance of the given model and returns its data
// objects by name, once the instance has parked.
func readDataObjectRows(t *testing.T, ts *httptest.Server, bpmn string) map[string]dataObjectRow {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", bpmn, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
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
	if key == 0 {
		t.Fatal("no active instance found")
	}
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get data objects: status=%d body=%s", code, body)
	}
	var rows []dataObjectRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode data objects: %v (%s)", err, body)
	}
	out := map[string]dataObjectRow{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

// TestInstanceDataObjectsLineage is the read side of data-object write attribution:
// the endpoint names the element that last wrote each object and when, and carries
// the object's whole state trail — so the Data view can answer "where did this value
// come from" instead of diffing snapshots. It also surfaces what only the definition
// knows: the declared class behind itemSubjectRef, and the collection flag.
func TestInstanceDataObjectsLineage(t *testing.T) {
	ts := newTestServer(t)
	byName := readDataObjectRows(t, ts, dataObjectLineageBPMN)

	order, ok := byName["order"]
	if !ok {
		t.Fatalf("no 'order' data object, got %v", byName)
	}
	if order.ItemType != "Order" {
		t.Errorf("itemType = %q, want Order (the declared class behind itemSubjectRef)", order.ItemType)
	}
	if order.IsCollection {
		t.Error("isCollection = true, want false")
	}
	if order.State != "approved" {
		t.Errorf("state = %q, want approved", order.State)
	}
	if order.ProducedBy != "record" {
		t.Errorf("producedBy = %q, want record — the task whose association wrote it", order.ProducedBy)
	}
	if order.At == 0 {
		t.Error("at = 0, want the timestamp of the write")
	}
	// The trail is the whole point of an event-sourced data object: seeded [received]
	// by nobody, then advanced to [approved] by the task that wrote it.
	if len(order.History) != 2 {
		t.Fatalf("history has %d entries, want 2 (seed then write): %+v", len(order.History), order.History)
	}
	if order.History[0].State != "received" || order.History[0].ProducedBy != "" {
		t.Errorf("history[0] = %+v, want state received produced by nobody", order.History[0])
	}
	if order.History[1].State != "approved" || order.History[1].ProducedBy != "record" {
		t.Errorf("history[1] = %+v, want state approved produced by 'record'", order.History[1])
	}
	if order.History[1].Kind != "number" {
		t.Errorf("history[1].kind = %q, want number (= amount wrote 100)", order.History[1].Kind)
	}
}

// TestInstanceDataObjectsUntyped covers an object the model declares no class for
// and a collection: the endpoint reports the collection flag from the definition and
// leaves itemType empty rather than inventing one, and an object nothing has written
// yet has an empty producedBy — "nobody wrote it", which is a different fact from
// "we did not record who did".
func TestInstanceDataObjectsUntyped(t *testing.T) {
	ts := newTestServer(t)
	byName := readDataObjectRows(t, ts, dataObjectBPMN)

	items, ok := byName["DataObject_items"]
	if !ok {
		t.Fatalf("no collection data object, got %v", byName)
	}
	if !items.IsCollection {
		t.Error("isCollection = false, want true")
	}
	if items.ItemType != "" {
		t.Errorf("itemType = %q, want empty (the model declares none)", items.ItemType)
	}
	if items.ProducedBy != "" {
		t.Errorf("producedBy = %q, want empty — the seed is nobody's write", items.ProducedBy)
	}
	if len(items.History) != 1 {
		t.Errorf("history has %d entries, want 1 (the seed): %+v", len(items.History), items.History)
	}
}

// dataObjectSelfCompletingBPMN runs to completion with no worker: a script task
// seeds a variable and a pass-through task's output association writes the data
// object. The instance finishes, which is what lets its deployment be deleted.
const dataObjectSelfCompletingBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="gonedef" name="Gone definition" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order">
      <dataState name="received"/>
    </dataObject>
    <dataObjectReference id="Ref_write" name="order" dataObjectRef="DO_order">
      <dataState name="approved"/>
    </dataObjectReference>
    <startEvent id="s"/>
    <scriptTask id="seed">
      <extensionElements><zeebe:script expression="= 100" resultVariable="amount"/></extensionElements>
    </scriptTask>
    <task id="record">
      <dataOutputAssociation id="doa"><targetRef>Ref_write</targetRef><assignment><from>= amount</from></assignment></dataOutputAssociation>
    </task>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="record"/>
    <sequenceFlow id="f3" sourceRef="record" targetRef="e"/>
  </process>
</definitions>`

// TestInstanceDataObjectsDefinitionDeleted covers a finished instance whose
// definition has since been deleted. The values and the state trail are facts on the
// log and must still read; the *declared* half — the class and the collection flag —
// and the element names behind the writes come from the definition, so they answer
// empty rather than guessing. That is the same posture the replay takes on an element
// index it can no longer map: a wrong label is worse than a missing one.
func TestInstanceDataObjectsDefinitionDeleted(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", dataObjectSelfCompletingBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, b)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances?state=completed", "", "")
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(instances) == 0 {
		t.Fatalf("no completed instance (%s)", body)
	}
	key := instances[0].Key

	// While the definition is still deployed, the declared half is there.
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get data objects: status=%d body=%s", code, body)
	}
	var before []dataObjectRow
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(before) != 1 || before[0].ItemType != "Order" || before[0].ProducedBy != "record" {
		t.Fatalf("before delete = %+v, want one 'order' of class Order written by 'record'", before)
	}

	if code, b := doReq(t, ts, http.MethodDelete, fmt.Sprintf("/api/v1/processes/%d", deploy.Key), "", ""); code != http.StatusNoContent {
		t.Fatalf("delete deployment: status=%d body=%s", code, b)
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get data objects after delete: status=%d body=%s", code, body)
	}
	var after []dataObjectRow
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(after) != 1 {
		t.Fatalf("after delete = %d objects, want 1 (%s)", len(after), body)
	}
	got := after[0]
	if got.State != "approved" || got.Kind != "number" {
		t.Errorf("value/state = %+v, want the recorded number in state approved", got)
	}
	if len(got.History) != 2 {
		t.Errorf("history = %d entries, want 2 — the trail is on the log, not in the definition", len(got.History))
	}
	if got.ItemType != "" {
		t.Errorf("itemType = %q, want empty — the definition that declared it is gone", got.ItemType)
	}
	if got.ProducedBy != "" {
		t.Errorf("producedBy = %q, want empty — the element index can no longer be mapped", got.ProducedBy)
	}
}

// dataObjectShapesBPMN accrues a structured object field by field (ADR-0060) and
// writes a string and a boolean object beside it, then parks — so the endpoint has
// one of every value shape a data object can carry.
const dataObjectShapesBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="shapes" name="Shapes" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order"/>
    <dataObject id="DO_ref" name="ref"/>
    <dataObject id="DO_ok" name="ok"/>
    <dataObjectReference id="Ref_order" name="order" dataObjectRef="DO_order">
      <dataState name="drafted"/>
    </dataObjectReference>
    <dataObjectReference id="Ref_ref" name="ref" dataObjectRef="DO_ref"/>
    <dataObjectReference id="Ref_ok" name="ok" dataObjectRef="DO_ok"/>
    <startEvent id="s"/>
    <scriptTask id="seed">
      <extensionElements><zeebe:script expression="= 100" resultVariable="amount"/></extensionElements>
    </scriptTask>
    <task id="record">
      <dataOutputAssociation id="d1"><targetRef>Ref_order</targetRef><assignment><from>= amount</from><to>total</to></assignment></dataOutputAssociation>
      <dataOutputAssociation id="d2"><targetRef>Ref_ref</targetRef><assignment><from>= "ORD-1"</from></assignment></dataOutputAssociation>
      <dataOutputAssociation id="d3"><targetRef>Ref_ok</targetRef><assignment><from>= true</from></assignment></dataOutputAssociation>
    </task>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="record"/>
    <sequenceFlow id="f3" sourceRef="record" targetRef="w"/>
    <sequenceFlow id="f4" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestInstanceDataObjectsValueShapes reads back one of every shape a data object can
// hold — a structured object accrued through a member target (ADR-0060), a string, and
// a boolean — each typed the way the Data view renders it. A structured object is the
// case the whole design is for: `order` is one record that grows across steps, not
// three loose variables.
func TestInstanceDataObjectsValueShapes(t *testing.T) {
	ts := newTestServer(t)
	byName := readDataObjectRows(t, ts, dataObjectShapesBPMN)

	order, ok := byName["order"]
	if !ok {
		t.Fatalf("no 'order' data object, got %v", byName)
	}
	if order.Kind != "json" {
		t.Errorf("order.kind = %q, want json — a member target stores the object", order.Kind)
	}
	obj, ok := order.Value.(map[string]any)
	if !ok || fmt.Sprint(obj["total"]) != "100" {
		t.Errorf("order.value = %#v, want an object with total 100", order.Value)
	}
	if order.State != "drafted" {
		t.Errorf("order.state = %q, want drafted", order.State)
	}
	if ref := byName["ref"]; ref.Kind != "string" || ref.Value != "ORD-1" {
		t.Errorf("ref = kind %q value %v, want string ORD-1", ref.Kind, ref.Value)
	}
	if okObj := byName["ok"]; okObj.Kind != "boolean" || okObj.Value != true {
		t.Errorf("ok = kind %q value %v, want boolean true", okObj.Kind, okObj.Value)
	}
}

// dataObjectAccrualBPMN builds one order across three tasks, each writing a different
// member and advancing the data state — the way a record actually grows as a token
// moves (ADR-0060) — then parks.
const dataObjectAccrualBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="accrual" name="Accrual" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order">
      <dataState name="empty"/>
    </dataObject>
    <dataObjectReference id="Ref_id" name="order" dataObjectRef="DO_order"><dataState name="identified"/></dataObjectReference>
    <dataObjectReference id="Ref_total" name="order" dataObjectRef="DO_order"><dataState name="priced"/></dataObjectReference>
    <dataObjectReference id="Ref_ok" name="order" dataObjectRef="DO_order"><dataState name="approved"/></dataObjectReference>
    <startEvent id="s"/>
    <task id="identify">
      <dataOutputAssociation id="a1"><targetRef>Ref_id</targetRef><assignment><from>= "ORD-1"</from><to>id</to></assignment></dataOutputAssociation>
    </task>
    <task id="price">
      <dataOutputAssociation id="a2"><targetRef>Ref_total</targetRef><assignment><from>= 100</from><to>total</to></assignment></dataOutputAssociation>
    </task>
    <task id="approve">
      <dataOutputAssociation id="a3"><targetRef>Ref_ok</targetRef><assignment><from>= true</from><to>approved</to></assignment></dataOutputAssociation>
    </task>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="identify"/>
    <sequenceFlow id="f2" sourceRef="identify" targetRef="price"/>
    <sequenceFlow id="f3" sourceRef="price" targetRef="approve"/>
    <sequenceFlow id="f4" sourceRef="approve" targetRef="w"/>
    <sequenceFlow id="f5" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestInstanceDataObjectsAccrualTrail follows one record accruing across three tasks:
// every entry in the trail must name the task that made *that* write, not just the
// last one, and the row's own summary must name the most recent. This is the case the
// whole design is for — `order` is one thing that grows, and the trail is the sentence
// "identify gave it an id, price gave it a total, approve approved it".
func TestInstanceDataObjectsAccrualTrail(t *testing.T) {
	ts := newTestServer(t)
	byName := readDataObjectRows(t, ts, dataObjectAccrualBPMN)

	order, ok := byName["order"]
	if !ok {
		t.Fatalf("no 'order' data object, got %v", byName)
	}
	if len(order.History) != 4 {
		t.Fatalf("history = %d entries, want 4 (seed + three writes): %+v", len(order.History), order.History)
	}
	want := []struct{ state, by string }{
		{"empty", ""},
		{"identified", "identify"},
		{"priced", "price"},
		{"approved", "approve"},
	}
	for i, w := range want {
		if order.History[i].State != w.state || order.History[i].ProducedBy != w.by {
			t.Errorf("history[%d] = state %q by %q, want state %q by %q",
				i, order.History[i].State, order.History[i].ProducedBy, w.state, w.by)
		}
	}
	if order.ProducedBy != "approve" || order.State != "approved" {
		t.Errorf("row summary = state %q by %q, want the most recent write", order.State, order.ProducedBy)
	}
	// The members accrued rather than replaced each other — the point of a field write.
	obj, ok := order.Value.(map[string]any)
	if !ok || obj["id"] != "ORD-1" || fmt.Sprint(obj["total"]) != "100" || obj["approved"] != true {
		t.Errorf("order.value = %#v, want all three members present", order.Value)
	}
}
