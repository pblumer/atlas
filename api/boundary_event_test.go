package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// boundaryTimerBPMN is a boundary-timer model in the namespaced shape the bpmn-js
// editor emits (prefixed elements, a tFormalExpression timeDuration): a service
// task with an interrupting 30-minute timer boundary that escalates. It guards the
// editor → compiler → engine contract end to end.
const boundaryTimerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="publish" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="tweet">
      <bpmn:extensionElements><zeebe:taskDefinition type="post"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:boundaryEvent id="timeout" attachedToRef="tweet">
      <bpmn:timerEventDefinition>
        <bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT30M</bpmn:timeDuration>
      </bpmn:timerEventDefinition>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="done"/>
    <bpmn:endEvent id="escalated"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="tweet"/>
    <bpmn:sequenceFlow id="f2" sourceRef="tweet" targetRef="done"/>
    <bpmn:sequenceFlow id="f3" sourceRef="timeout" targetRef="escalated"/>
  </bpmn:process>
</bpmn:definitions>`

// TestDeployBoundaryTimerArms deploys the editor-shaped boundary-timer model,
// starts an instance, and confirms both the host task and its armed boundary
// event carry a live token — i.e. the boundary parsed, deployed, and armed.
func TestDeployBoundaryTimerArms(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", boundaryTimerBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}

	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime", dep.Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", code, body)
	}
	var rt struct {
		Instances int `json:"instances"`
		Tokens    int `json:"tokens"`
		Elements  []struct {
			ElementID string `json:"elementId"`
			Type      string `json:"type"`
			Tokens    int    `json:"tokens"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	byID := map[string]struct {
		typ    string
		tokens int
	}{}
	for _, e := range rt.Elements {
		byID[e.ElementID] = struct {
			typ    string
			tokens int
		}{e.Type, e.Tokens}
	}
	// One instance, two live tokens: the host task and its armed boundary event.
	if rt.Instances != 1 || rt.Tokens != 2 {
		t.Fatalf("runtime = %d instance(s), %d token(s); want 1 and 2", rt.Instances, rt.Tokens)
	}
	if host := byID["tweet"]; host.typ != "ServiceTask" || host.tokens != 1 {
		t.Errorf("host tweet = %+v, want ServiceTask with 1 token", host)
	}
	if b := byID["timeout"]; b.typ != "BoundaryEvent" || b.tokens != 1 {
		t.Errorf("boundary timeout = %+v, want BoundaryEvent with 1 token (armed)", b)
	}
}
