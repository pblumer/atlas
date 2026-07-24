package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// startFormBPMN binds a start form to the start event; the process list should
// surface it as startFormId so the Tasks app can offer a "start via form" flow
// (ADR-0028).
const startFormBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="onboard" isExecutable="true" name="Onboarding">
    <startEvent id="s">
      <extensionElements><zeebe:formDefinition formId="onboarding-form"/></extensionElements>
    </startEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

func TestProcessListStartForm(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", startFormBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: status=%d body=%s", code, body)
	}
	var procs []struct {
		ProcessID   string `json:"processId"`
		StartFormID string `json:"startFormId"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	if len(procs) != 1 {
		t.Fatalf("processes = %d, want 1", len(procs))
	}
	if procs[0].StartFormID != "onboarding-form" {
		t.Errorf("startFormId = %q, want \"onboarding-form\"", procs[0].StartFormID)
	}
}
