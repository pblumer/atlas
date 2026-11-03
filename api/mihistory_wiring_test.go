package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

const miWiringBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="d">
  <process id="miwiring" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="calc">
      <extensionElements><zeebe:script resultVariable="r" expression="= loopCounter"/></extensionElements>
      <multiInstanceLoopCharacteristics isSequential="false"><loopCardinality>3</loopCardinality></multiInstanceLoopCharacteristics>
    </scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="calc"/>
    <sequenceFlow id="f2" sourceRef="calc" targetRef="e"/>
  </process>
</definitions>`

// The MI history routes are served by the real server: their reads go through
// the server's off-loop read view and its deployments (ADR-0517).
func TestMultiInstanceHistoryRoutesAreServed(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", miWiringBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(body, &dep)
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), `{}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, body)
	}
	var inst struct {
		Key uint64 `json:"instanceKey"`
	}
	_ = json.Unmarshal(body, &inst)
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/multi-instances", inst.Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("bodies: %d %s", code, body)
	}
	var listing struct {
		ProcessDefinitionKey uint64 `json:"processDefinitionKey"`
		InstanceState        string `json:"instanceState"`
		Bodies               []struct {
			ElementID  string `json:"elementId"`
			Mode       string `json:"mode"`
			Iterations int    `json:"iterations"`
			Completed  int    `json:"completed"`
		} `json:"bodies"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		t.Fatal(err)
	}
	if listing.ProcessDefinitionKey != dep.Key || listing.InstanceState != "completed" || len(listing.Bodies) != 1 ||
		listing.Bodies[0].ElementID != "calc" || listing.Bodies[0].Mode != "parallel" || listing.Bodies[0].Completed != 3 {
		t.Fatalf("listing: %s", body)
	}
	if code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/424242/multi-instances", "", ""); code != http.StatusNotFound {
		t.Fatalf("unknown instance: %d %s", code, body)
	}
}
