package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The read side of the instance views — runtime overlay, replay, data objects, object
// graph, lifecycle — when a record behind them cannot be read.
//
// These views are what an operator looks at to decide what to do about an instance,
// and each of them is assembled from several reads. The rule they share is the one
// TestInstanceDataObjectsReportsScanErrors states for the Data tab: a read that fails is
// a 500, never a view with a hole in it, because a partial answer here reads as a fact
// ("this token is not there", "this datum never passed through that state"). A few of
// them deliberately degrade instead — the cross-instance index keeps its values when the
// vocabulary cannot be read, the aggregate overlay says its incident counts are not
// exact — and those are pinned too, because "degrade" must not quietly become "fail" or
// "pretend".
//
// The faults are injected into the real store through the state package's test
// affordances (InjectCorrupt*), or written as records directly where a view reads state
// no running process would produce on demand, the same way the existing Data tab tests
// seed it.

// handlersWaitBPMN parks one instance at a service task no worker serves.
const handlersWaitBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="wait" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="t"><extensionElements><zeebe:taskDefinition type="work"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`

// handlersDeployAt deploys a model through path (which may carry ?projectId=) and
// returns its definition key.
func handlersDeployAt(t *testing.T, x deployTestHarness, path, xml string) uint64 {
	t.Helper()
	code, body := x.do(http.MethodPost, path, xml)
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil || dep.Key == 0 {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return dep.Key
}

// handlersStartAt starts an instance of a definition and returns its key.
func handlersStartAt(t *testing.T, x deployTestHarness, defKey uint64) uint64 {
	t.Helper()
	code, body := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}")
	if code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, body)
	}
	var out struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.InstanceKey == 0 {
		t.Fatalf("decode start: %v (%s)", err, body)
	}
	return out.InstanceKey
}

// handlersTokenOf returns the element instance an instance's single live token sits on.
func handlersTokenOf(t *testing.T, srv *Server, instKey uint64) uint64 {
	t.Helper()
	var keys []uint64
	var err error
	srv.do(func() {
		err = srv.store.ElementInstancesOfProcess(instKey, func(k uint64) error {
			keys = append(keys, k)
			return nil
		})
	})
	if err != nil || len(keys) != 1 {
		t.Fatalf("instance %d holds tokens %v (err %v), want exactly one", instKey, keys, err)
	}
	return keys[0]
}

// handlersSeed writes state records in one transaction on the run loop.
func handlersSeed(t *testing.T, srv *Server, write func(tx *state.Tx) error) {
	t.Helper()
	var err error
	srv.do(func() {
		tx := srv.store.NewTransaction()
		defer tx.Close()
		if err = write(tx); err == nil {
			err = tx.Commit()
		}
	})
	if err != nil {
		t.Fatalf("seed state: %v", err)
	}
}

// handlersInject runs one of the store's corruption affordances on the run loop.
func handlersInject(t *testing.T, srv *Server, inject func() error) {
	t.Helper()
	var err error
	srv.do(func() { err = inject() })
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
}

// handlersExpect asserts a status and a fragment of the body.
func handlersExpect(t *testing.T, what string, code int, body []byte, wantCode int, wantText string) {
	t.Helper()
	if code != wantCode {
		t.Fatalf("%s: status=%d body=%s, want %d", what, code, body, wantCode)
	}
	if !strings.Contains(string(body), wantText) {
		t.Errorf("%s: body=%s, want it to contain %q", what, body, wantText)
	}
}

// TestHandlersRuntimeOfOneInstanceRefusesWhatItCannotRead: isolating one instance on
// the diagram walks its own tokens and looks up the incident hanging off each. Either
// read failing is a 500 — a token that silently drops off the overlay reads as an
// instance that has moved on.
func TestHandlersRuntimeOfOneInstanceRefusesWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(s *Server, token uint64) error
	}{
		{"token", func(s *Server, token uint64) error { return s.store.InjectCorruptElementInstance(token) }},
		{"incident", func(s *Server, token uint64) error { return s.store.InjectCorruptIncident(token) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			x := deployTestHarness{t, srv.Handler()}
			def := handlersDeployAt(t, x, "/api/v1/deployments", handlersWaitBPMN)
			inst := handlersStartAt(t, x, def)
			path := fmt.Sprintf("/api/v1/processes/%d/runtime?instance=%d", def, inst)
			if code, body := x.do(http.MethodGet, path, ""); code != http.StatusOK {
				t.Fatalf("runtime before the fault: status=%d body=%s", code, body)
			}
			token := handlersTokenOf(t, srv, inst)
			handlersInject(t, srv, func() error { return tc.corrupt(srv, token) })

			code, body := x.do(http.MethodGet, path, "")
			handlersExpect(t, "runtime", code, body, http.StatusInternalServerError, "read runtime")
		})
	}
}

// TestHandlersAggregateRuntimeSaysWhenItsIncidentCountsAreNotExact: the aggregate view
// reads its incidents from a whole snapshot of the incident family. When that reading
// cannot be taken, the view still answers from its counters — but says the incident
// numbers are not exact rather than reporting a zero it has not earned.
func TestHandlersAggregateRuntimeSaysWhenItsIncidentCountsAreNotExact(t *testing.T) {
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	def := handlersDeployAt(t, x, "/api/v1/deployments", handlersWaitBPMN)
	inst := handlersStartAt(t, x, def)
	token := handlersTokenOf(t, srv, inst)
	handlersInject(t, srv, func() error { return srv.store.InjectCorruptIncident(token) })

	code, body := x.do(http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime", def), "")
	if code != http.StatusOK {
		t.Fatalf("aggregate runtime: status=%d body=%s", code, body)
	}
	var rt struct {
		Tokens              int  `json:"tokens"`
		IncidentCountsExact bool `json:"incidentCountsExact"`
		IncidentTotal       int  `json:"incidentTotal"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if rt.IncidentCountsExact {
		t.Errorf("incident counts claimed exact from a reading that failed: %s", body)
	}
	if rt.Tokens != 1 {
		t.Errorf("tokens = %d, want the counters' 1: the view should still answer what it can", rt.Tokens)
	}
}

// handlersCallerBPMN calls handlersChildBPMN, which waits at a user task — so the
// caller's replay needs the reverse call-activity links.
const handlersChildBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="child" isExecutable="true">
    <startEvent id="cs"/><userTask id="ct"/><endEvent id="ce"/>
    <sequenceFlow id="cf1" sourceRef="cs" targetRef="ct"/>
    <sequenceFlow id="cf2" sourceRef="ct" targetRef="ce"/>
  </process>
</definitions>`

const handlersCallerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="caller" isExecutable="true">
    <startEvent id="s"/>
    <callActivity id="call">
      <extensionElements><zeebe:calledElement processId="child" bindingType="latest"/></extensionElements>
    </callActivity>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="call"/>
    <sequenceFlow id="f2" sourceRef="call" targetRef="e"/>
  </process>
</definitions>`

// TestHandlersReplayRefusesWhenTheInstanceFamilyCannotBeRead: a replay of a definition
// that calls another process links each call activity to the child it started, and
// there is no index from parent to child, so that link is a walk of the instance
// family. A record in it that cannot be read is a 500 — a replay that silently lost
// its drill-in links would read as a call that started nothing.
func TestHandlersReplayRefusesWhenTheInstanceFamilyCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	handlersDeployAt(t, x, "/api/v1/deployments", handlersChildBPMN)
	caller := handlersDeployAt(t, x, "/api/v1/deployments", handlersCallerBPMN)
	inst := handlersStartAt(t, x, caller)
	path := fmt.Sprintf("/api/v1/instances/%d/timeline", inst)
	if code, body := x.do(http.MethodGet, path, ""); code != http.StatusOK {
		t.Fatalf("timeline before the fault: status=%d body=%s", code, body)
	}
	// Not this instance's record: some other instance in the family the walk crosses.
	handlersInject(t, srv, func() error { return srv.store.InjectCorruptProcessInstance(inst + 1000) })

	code, body := x.do(http.MethodGet, path, "")
	handlersExpect(t, "timeline", code, body, http.StatusInternalServerError, "read instance timeline")
}

// handlersOrderModel is an information model with one class whose lifecycle the
// lifecycle view draws on.
const handlersOrderModel = `{
  "classes":[
    {"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
     "attributes":[{"name":"id","type":"string","multiplicity":"1"}],
     "lifecycle":{
       "states":[{"name":"received","initial":true,"x":0,"y":0},
                 {"name":"shipped","final":true,"x":0,"y":90}],
       "transitions":[{"id":"t1","name":"ship","from":"received","to":"shipped"}]}}
  ],
  "associations":[]}`

// handlersDataBPMN declares two Order data objects and runs straight through: the
// tests seed the instance's data rather than running it.
const handlersDataBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <itemDefinition id="ItemDefinition_Order" structureRef="Order"/>
  <process id="sales" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="ItemDefinition_Order"/>
    <dataObject id="DO_draft" name="draft" itemSubjectRef="ItemDefinition_Order"/>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// handlersSalesApplication creates an application with the Order model and deploys
// handlersDataBPMN into it, returning the definition key.
func handlersSalesApplication(t *testing.T, x deployTestHarness) uint64 {
	t.Helper()
	code, body := x.do(http.MethodPost, "/api/v1/applications", `{"name":"Sales"}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil || app.ID == "" {
		t.Fatalf("decode application: %v (%s)", err, body)
	}
	code, body = x.do(http.MethodPost, "/api/v1/infomodel/models", fmt.Sprintf(`{"applicationId":%q,"name":"Sales data"}`, app.ID))
	if code != http.StatusCreated {
		t.Fatalf("create model: status=%d body=%s", code, body)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &m); err != nil || m.ID == "" {
		t.Fatalf("decode model: %v (%s)", err, body)
	}
	if code, body := x.do(http.MethodPut, "/api/v1/infomodel/models/"+m.ID, handlersOrderModel); code != http.StatusOK {
		t.Fatalf("save model: status=%d body=%s", code, body)
	}
	return handlersDeployAt(t, x, "/api/v1/deployments?projectId="+app.ID, handlersDataBPMN)
}

// handlersSeedInstance writes an active instance of defKey under key with the given
// data objects.
func handlersSeedInstance(t *testing.T, srv *Server, key, defKey uint64, objects ...model.DataObjectValue) {
	t.Helper()
	handlersSeed(t, srv, func(tx *state.Tx) error {
		if err := tx.PutProcessInstance(key, &model.ProcessInstanceValue{ProcessDefKey: defKey, State: model.PIActive}); err != nil {
			return err
		}
		for i := range objects {
			o := objects[i]
			o.ScopeKey = key
			if err := tx.PutDataObject(&o); err != nil {
				return err
			}
		}
		return nil
	})
}

// handlersBreakInfoModels makes the information-model store unreadable: a record in
// it is cut short.
func handlersBreakInfoModels(t *testing.T, srv *Server) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(srv.dataDir, "information-models", "10.json"), []byte(`{"id":`), 0o600); err != nil {
		t.Fatalf("break information models: %v", err)
	}
}

// TestHandlersLifecycleRefusesWhatItCannotRead: the lifecycle view reads the object's
// trail and the class's machine. Either one unreadable is a 500 — drawing the machine
// without the trail would show a datum that never moved, and the trail without the
// machine has nothing to be drawn on.
func TestHandlersLifecycleRefusesWhatItCannotRead(t *testing.T) {
	const scope = uint64(7001)
	for _, tc := range []struct {
		name    string
		breakIt func(t *testing.T, srv *Server)
	}{
		{"information model", handlersBreakInfoModels},
		{"state trail", func(t *testing.T, srv *Server) {
			handlersInject(t, srv, func() error { return srv.store.InjectCorruptDataObjectSnapshot(scope, 1, 1) })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			x := deployTestHarness{t, srv.Handler()}
			def := handlersSalesApplication(t, x)
			handlersSeedInstance(t, srv, scope, def, model.DataObjectValue{Name: "order", State: "received", Kind: model.VarNull})
			path := fmt.Sprintf("/api/v1/instances/%d/lifecycle", scope)
			if code, body := x.do(http.MethodGet, path, ""); code != http.StatusOK {
				t.Fatalf("lifecycle before the fault: status=%d body=%s", code, body)
			}
			tc.breakIt(t, srv)
			code, body := x.do(http.MethodGet, path, "")
			handlersExpect(t, "lifecycle", code, body, http.StatusInternalServerError, "read lifecycle")
		})
	}
}

// TestHandlersLifecycleOfObjectsWithNoRecordedTrail: an object that only ever had its
// state seeded — no write was recorded — is still drawn standing on that state, with
// no attribution, because its current state is known even where its history is not.
// An object with no state at all is drawn standing nowhere, and an object the model
// does not declare has no class to draw and is left out rather than guessed at.
func TestHandlersLifecycleOfObjectsWithNoRecordedTrail(t *testing.T) {
	const scope = uint64(7002)
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	def := handlersSalesApplication(t, x)
	handlersSeedInstance(t, srv, scope, def,
		model.DataObjectValue{Name: "order", State: "received", Kind: model.VarNull},
		model.DataObjectValue{Name: "draft", Kind: model.VarNull},
		model.DataObjectValue{Name: "stray", State: "received", Kind: model.VarNull},
	)
	code, body := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/lifecycle", scope), "")
	if code != http.StatusOK {
		t.Fatalf("lifecycle: status=%d body=%s", code, body)
	}
	var traces []struct {
		Object  string `json:"object"`
		Class   string `json:"class"`
		Current string `json:"current"`
	}
	if err := json.Unmarshal(body, &traces); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	got := map[string]string{}
	for _, tr := range traces {
		if tr.Class != "Order" {
			t.Errorf("object %q drawn as class %q, want Order", tr.Object, tr.Class)
		}
		got[tr.Object] = tr.Current
	}
	if len(got) != 2 {
		t.Fatalf("lifecycles for %v, want exactly order and draft: %s", got, body)
	}
	if cur, ok := got["order"]; !ok || cur != "received" {
		t.Errorf("order stands on %q (listed %v), want its seeded state %q", cur, ok, "received")
	}
	if cur, ok := got["draft"]; !ok || cur != "" {
		t.Errorf("draft stands on %q (listed %v), want nowhere", cur, ok)
	}
	if _, ok := got["stray"]; ok {
		t.Error("an object the model does not declare was given a lifecycle")
	}
}

// TestHandlersObjectGraphRefusesWhatItCannotRead: the object graph reads the instance
// record (for the classes its definition declares) and the objects themselves.
func TestHandlersObjectGraphRefusesWhatItCannotRead(t *testing.T) {
	const scope = uint64(7003)
	for _, tc := range []struct {
		name   string
		inject func(srv *Server) error
	}{
		{"instance record", func(srv *Server) error { return srv.store.InjectCorruptProcessInstance(scope) }},
		{"data object", func(srv *Server) error { return srv.store.InjectCorruptDataObject(scope, "order") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			x := deployTestHarness{t, srv.Handler()}
			def := handlersDeployAt(t, x, "/api/v1/deployments", handlersDataBPMN)
			handlersSeedInstance(t, srv, scope, def, model.DataObjectValue{Name: "draft", Kind: model.VarNull})
			handlersInject(t, srv, func() error { return tc.inject(srv) })
			code, body := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/object-graph", scope), "")
			handlersExpect(t, "object graph", code, body, http.StatusInternalServerError, "read object graph")
		})
	}
}

// TestHandlersObjectGraphCarriesScalarValuesExactly: an object holding a scalar has no
// members to draw, so the value itself rides on the node — a number keeping its exact
// text, so 10.10 is not drawn as 10.1 — and an unset object is drawn as unset rather
// than as the text "null".
func TestHandlersObjectGraphCarriesScalarValuesExactly(t *testing.T) {
	const scope = uint64(7004)
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	def := handlersDeployAt(t, x, "/api/v1/deployments", handlersWaitBPMN)
	handlersSeedInstance(t, srv, scope, def,
		model.DataObjectValue{Name: "amount", Kind: model.VarNumber, Text: "10.10"},
		model.DataObjectValue{Name: "label", Kind: model.VarString, Text: "ORD-1"},
		model.DataObjectValue{Name: "approved", Kind: model.VarBool, Bool: true},
		model.DataObjectValue{Name: "missing", Kind: model.VarNull},
	)
	code, body := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/object-graph", scope), "")
	if code != http.StatusOK {
		t.Fatalf("object graph: status=%d body=%s", code, body)
	}
	var g struct {
		Nodes []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
			Unset bool   `json:"unset"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	values := map[string]string{}
	unset := map[string]bool{}
	for _, n := range g.Nodes {
		values[n.Name], unset[n.Name] = n.Value, n.Unset
	}
	for name, want := range map[string]string{"amount": "10.10", "label": "ORD-1", "approved": "true"} {
		if values[name] != want {
			t.Errorf("node %q carries %q, want %q (graph %s)", name, values[name], want, body)
		}
	}
	if values["missing"] != "" || !unset["missing"] {
		t.Errorf("an unset object is drawn with value %q, unset=%v; want no value and unset", values["missing"], unset["missing"])
	}
}

// TestHandlersDataObjectIndexRefusesWhatItCannotRead: the cross-instance index walks
// the instance family and each instance's objects. A sweep that skipped an unreadable
// one would answer "nobody is carrying this order" when somebody might be.
func TestHandlersDataObjectIndexRefusesWhatItCannotRead(t *testing.T) {
	const scope = uint64(7005)
	for _, tc := range []struct {
		name   string
		inject func(srv *Server) error
	}{
		{"instance record", func(srv *Server) error { return srv.store.InjectCorruptProcessInstance(scope + 1) }},
		{"data object", func(srv *Server) error { return srv.store.InjectCorruptDataObject(scope, "order") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			x := deployTestHarness{t, srv.Handler()}
			def := handlersDeployAt(t, x, "/api/v1/deployments", handlersDataBPMN)
			handlersSeedInstance(t, srv, scope, def, model.DataObjectValue{Name: "draft", Kind: model.VarNull})
			if code, body := x.do(http.MethodGet, "/api/v1/data-objects", ""); code != http.StatusOK {
				t.Fatalf("index before the fault: status=%d body=%s", code, body)
			}
			handlersInject(t, srv, func() error { return tc.inject(srv) })
			code, body := x.do(http.MethodGet, "/api/v1/data-objects", "")
			handlersExpect(t, "data-object index", code, body, http.StatusInternalServerError, "read data objects")
		})
	}
}

// handlersDataObjectIndex is the part of the cross-instance index these tests read.
type handlersDataObjectIndex struct {
	Objects []struct {
		InstanceKey uint64 `json:"instanceKey"`
		Name        string `json:"name"`
		ItemType    string `json:"itemType"`
		Key         string `json:"key"`
	} `json:"objects"`
	Scanned   int  `json:"scanned"`
	Truncated bool `json:"truncated"`
}

// TestHandlersDataObjectIndexStopsAtItsResultCap: the index returns at most one page of
// objects, newest instance first, and says it stopped — so the instance it never
// reached is reported as unscanned rather than as carrying nothing.
func TestHandlersDataObjectIndexStopsAtItsResultCap(t *testing.T) {
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	def := handlersDeployAt(t, x, "/api/v1/deployments", handlersWaitBPMN)
	busy := make([]model.DataObjectValue, dataObjectsAcrossInstancesLimit)
	for i := range busy {
		busy[i] = model.DataObjectValue{Name: fmt.Sprintf("o%03d", i), Kind: model.VarNull}
	}
	handlersSeedInstance(t, srv, 9000, def, busy...)                                                  // newest: fills the page
	handlersSeedInstance(t, srv, 8000, def, model.DataObjectValue{Name: "late", Kind: model.VarNull}) // never reached

	code, body := x.do(http.MethodGet, "/api/v1/data-objects", "")
	if code != http.StatusOK {
		t.Fatalf("index: status=%d body=%s", code, body)
	}
	var idx handlersDataObjectIndex
	if err := json.Unmarshal(body, &idx); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(idx.Objects) != dataObjectsAcrossInstancesLimit || idx.Scanned != 1 || !idx.Truncated {
		t.Fatalf("index returned %d objects from %d instance(s), truncated=%v; want %d from 1, truncated",
			len(idx.Objects), idx.Scanned, idx.Truncated, dataObjectsAcrossInstancesLimit)
	}
	for _, o := range idx.Objects {
		if o.InstanceKey != 9000 {
			t.Fatalf("object %q of instance %d is on the page; the sweep should stop at the newest instance's", o.Name, o.InstanceKey)
		}
	}
}

// TestHandlersDataObjectIndexKeepsItsValuesWithoutAVocabulary: the business key comes
// from the application's information model, and the index degrades rather than fails
// when that model cannot be read — the object is still listed, it simply has no key.
// The same object with the model readable carries its key, which is what shows the
// degradation is the vocabulary's absence and nothing else.
func TestHandlersDataObjectIndexKeepsItsValuesWithoutAVocabulary(t *testing.T) {
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	def := handlersSalesApplication(t, x)
	handlersSeedInstance(t, srv, 7006, def, model.DataObjectValue{Name: "order", Kind: model.VarJSON, Text: `{"id":"ORD-1"}`})

	read := func() handlersDataObjectIndex {
		t.Helper()
		code, body := x.do(http.MethodGet, "/api/v1/data-objects", "")
		if code != http.StatusOK {
			t.Fatalf("index: status=%d body=%s", code, body)
		}
		var idx handlersDataObjectIndex
		if err := json.Unmarshal(body, &idx); err != nil || len(idx.Objects) != 1 {
			t.Fatalf("decode index: %v (%s)", err, body)
		}
		return idx
	}
	if got := read().Objects[0]; got.Key != "ORD-1" || got.ItemType != "Order" {
		t.Fatalf("with the model readable the order reads %+v, want class Order and key ORD-1", got)
	}
	handlersBreakInfoModels(t, srv)
	if got := read().Objects[0]; got.Name != "order" || got.ItemType != "Order" || got.Key != "" {
		t.Errorf("without the model the order reads %+v, want it listed with its class and no key", got)
	}
}

// TestHandlersDataTabIgnoresTheTrailOfAnObjectNoLongerCarried: a state trail can hold
// entries for an object of a scope the instance no longer carries. Those are not
// attached to anything — and in particular not to the object that is listed.
func TestHandlersDataTabIgnoresTheTrailOfAnObjectNoLongerCarried(t *testing.T) {
	const scope = uint64(7007)
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	def := handlersDeployAt(t, x, "/api/v1/deployments", handlersDataBPMN)
	handlersSeedInstance(t, srv, scope, def, model.DataObjectValue{Name: "order", State: "shipped", Kind: model.VarNull})
	handlersSeed(t, srv, func(tx *state.Tx) error {
		if err := tx.RecordDataObjectSnapshot(1, 1, &model.DataObjectValue{ScopeKey: scope, Name: "order", State: "received", Kind: model.VarNull}); err != nil {
			return err
		}
		return tx.RecordDataObjectSnapshot(2, 2, &model.DataObjectValue{ScopeKey: scope, Name: "gone", State: "lost", Kind: model.VarNull})
	})
	code, body := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", scope), "")
	if code != http.StatusOK {
		t.Fatalf("data objects: status=%d body=%s", code, body)
	}
	var objects []struct {
		Name    string `json:"name"`
		History []struct {
			State string `json:"state"`
		} `json:"history"`
	}
	if err := json.Unmarshal(body, &objects); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(objects) != 1 || objects[0].Name != "order" {
		t.Fatalf("objects = %s, want only the order the instance carries", body)
	}
	if h := objects[0].History; len(h) != 1 || h[0].State != "received" {
		t.Errorf("order's trail = %+v, want only its own entry", h)
	}
}

// TestHandlersInstanceJobsAnswerUnavailableWhileTheServerCloses: the job listing reads
// off the loop, which it still needs a turn of to take its view. A server that is
// closing has no turn to give, and the answer is a 503 the caller can retry against
// the next server — not a 500, and not an empty list that reads as "nothing parked".
func TestHandlersInstanceJobsAnswerUnavailableWhileTheServerCloses(t *testing.T) {
	dir := t.TempDir()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	proc := engine.New(1, lg, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := srv.Handler()
	srv.Close()

	code, body := deployTestHarness{t, h}.do(http.MethodGet, "/api/v1/instances/1/jobs", "")
	handlersExpect(t, "instance jobs", code, body, http.StatusServiceUnavailable, "shutting down")
}

// TestHandlersInstanceReadsRefuseAnUnreadableRecord: cancelling an instance first
// confirms it is running, and reading its variables walks its scope chain. A record on
// either path that cannot be decoded is a 500 naming the read — not a 404 that tells an
// operator the instance is gone, and not an empty variable set that tells a form there
// is nothing to prefill.
func TestHandlersInstanceReadsRefuseAnUnreadableRecord(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, want string
		inject                   func(srv *Server, inst uint64) error
	}{
		{"cancel", http.MethodDelete, "/api/v1/instances/%d", "find instance",
			func(srv *Server, inst uint64) error { return srv.store.InjectCorruptProcessInstance(inst) }},
		{"variables", http.MethodGet, "/api/v1/instances/%d/variables", "read variables",
			func(srv *Server, inst uint64) error { return srv.store.InjectCorruptElementInstance(inst) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			x := deployTestHarness{t, srv.Handler()}
			inst := handlersStartAt(t, x, handlersDeployAt(t, x, "/api/v1/deployments", handlersWaitBPMN))
			handlersInject(t, srv, func() error { return tc.inject(srv, inst) })
			code, body := x.do(tc.method, fmt.Sprintf(tc.path, inst), "")
			handlersExpect(t, tc.name, code, body, http.StatusInternalServerError, tc.want)
		})
	}
}
