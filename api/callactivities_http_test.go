package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A caller process with two call activities: one whose target ("child") is
// deployed and one whose target ("missing") is not. The per-server view must
// list both and mark only the first resolved.
const callerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="caller" name="Caller" isExecutable="true">
    <startEvent id="s"/>
    <callActivity id="callChild">
      <extensionElements><zeebe:calledElement processId="child" bindingType="latest"/></extensionElements>
    </callActivity>
    <callActivity id="callMissing">
      <extensionElements><zeebe:calledElement processId="missing" bindingType="deployment" propagateAllChildVariables="false"/></extensionElements>
    </callActivity>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="callChild"/>
    <sequenceFlow id="f2" sourceRef="callChild" targetRef="callMissing"/>
    <sequenceFlow id="f3" sourceRef="callMissing" targetRef="e"/>
  </process>
</definitions>`

const childBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="child" name="Child" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

type callActivityRow struct {
	CallerKey          uint64 `json:"callerKey"`
	CallerProcessID    string `json:"callerProcessId"`
	CallerName         string `json:"callerName"`
	CallerVersion      int32  `json:"callerVersion"`
	ElementID          string `json:"elementId"`
	CalledProcessID    string `json:"calledProcessId"`
	Binding            string `json:"binding"`
	PropagateAllParent bool   `json:"propagateAllParent"`
	PropagateAllChild  bool   `json:"propagateAllChild"`
	MultiInstance      bool   `json:"multiInstance"`
	Override           *struct {
		Action          string `json:"action"`
		TargetProcessID string `json:"targetProcessId"`
		TargetVersion   int32  `json:"targetVersion"`
	} `json:"override"`
	Resolved      bool   `json:"resolved"`
	TargetKey     uint64 `json:"targetKey"`
	TargetName    string `json:"targetName"`
	TargetVersion int32  `json:"targetVersion"`
}

// TestCallActivitiesEndpoint deploys a caller with a resolvable and an
// unresolvable call activity and asserts the per-server management view lists
// both, carries their static config, and reports resolution against the server's
// current deployments.
func TestCallActivitiesEndpoint(t *testing.T) {
	ts := newTestServer(t)

	// Empty server: no call activities anywhere.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/call-activities", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET call-activities (empty) status = %d, body = %s", code, body)
	}
	var empty []callActivityRow
	if err := json.Unmarshal(body, &empty); err != nil {
		t.Fatalf("decode empty: %v (%s)", err, body)
	}
	if len(empty) != 0 {
		t.Fatalf("empty server: got %d rows, want 0", len(empty))
	}

	// Deploy the caller before its callee: "child" resolves only once deployed.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", callerBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy caller status = %d, body = %s", code, body)
	}
	var caller struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(body, &caller)

	rows := getCallActivities(t, ts)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	byElem := map[string]callActivityRow{}
	for _, r := range rows {
		byElem[r.ElementID] = r
	}

	child := byElem["callChild"]
	if child.CallerProcessID != "caller" || child.CallerName != "Caller" || child.CallerKey != caller.Key {
		t.Errorf("callChild caller = {%q %q %d}, want {caller Caller %d}", child.CallerProcessID, child.CallerName, child.CallerKey, caller.Key)
	}
	if child.CalledProcessID != "child" || child.Binding != "latest" {
		t.Errorf("callChild target = {%q %q}, want {child latest}", child.CalledProcessID, child.Binding)
	}
	if !child.PropagateAllParent || !child.PropagateAllChild {
		t.Errorf("callChild propagation = {%v %v}, want {true true}", child.PropagateAllParent, child.PropagateAllChild)
	}
	if child.Resolved {
		t.Errorf("callChild resolved before child deployed, want unresolved")
	}

	missing := byElem["callMissing"]
	if missing.CalledProcessID != "missing" || missing.Binding != "deployment" {
		t.Errorf("callMissing target = {%q %q}, want {missing deployment}", missing.CalledProcessID, missing.Binding)
	}
	if missing.PropagateAllChild {
		t.Errorf("callMissing propagateAllChild = true, want false (isolated out)")
	}
	if missing.Resolved {
		t.Errorf("callMissing resolved, want unresolved")
	}

	// Deploy the child: its caller's call activity now resolves.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", childBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy child status = %d, body = %s", code, body)
	}
	var childDep struct {
		Key     uint64 `json:"key"`
		Version int32  `json:"version"`
	}
	_ = json.Unmarshal(body, &childDep)

	rows = getCallActivities(t, ts)
	for _, r := range rows {
		if r.ElementID != "callChild" {
			continue
		}
		if !r.Resolved || r.TargetKey != childDep.Key || r.TargetName != "Child" || r.TargetVersion != childDep.Version {
			t.Errorf("after child deploy, callChild = %+v, want resolved to child key %d v%d", r, childDep.Key, childDep.Version)
		}
	}
	// The unresolvable one stays unresolved.
	for _, r := range rows {
		if r.ElementID == "callMissing" && r.Resolved {
			t.Errorf("callMissing became resolved, want still unresolved")
		}
	}
}

func getCallActivities(t *testing.T, ts *httptest.Server) []callActivityRow {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/call-activities", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET call-activities status = %d, body = %s", code, body)
	}
	var rows []callActivityRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode rows: %v (%s)", err, body)
	}
	return rows
}
