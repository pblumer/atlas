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

// A process that declares start variables beside its start form lists both, so a start
// surface that shows the form can also ask for what the form does not. A process that
// declares none leaves the field out rather than listing an empty array.
func TestProcessListStartVariables(t *testing.T) {
	const declared = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="ucc" isExecutable="true" name="Sammelanschluss">
    <extensionElements>
      <atlas:startForm>
        <atlas:startVariable name="TenantId" type="string" required="true"/>
        <atlas:startVariable name="Retries" type="number" default="3"/>
      </atlas:startForm>
    </extensionElements>
    <startEvent id="s">
      <extensionElements><zeebe:formDefinition formId="collective-line"/></extensionElements>
    </startEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
	ts := newTestServer(t)
	for _, model := range []string{declared, startFormBPMN} {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", model, "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy: status=%d body=%s", code, body)
		}
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: status=%d body=%s", code, body)
	}
	var procs []map[string]json.RawMessage
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, p := range procs {
		var id string
		_ = json.Unmarshal(p["processId"], &id)
		byID[id] = p
	}
	const want = `[{"name":"TenantId","type":"string","required":true},{"name":"Retries","type":"number","default":"3"}]`
	if got := string(byID["ucc"]["startVariables"]); got != want {
		t.Errorf("ucc startVariables = %s, want %s", got, want)
	}
	if got := string(byID["ucc"]["startFormId"]); got != `"collective-line"` {
		t.Errorf("ucc startFormId = %s, want \"collective-line\"", got)
	}
	if raw, ok := byID["onboard"]["startVariables"]; ok {
		t.Errorf("onboard declares no start variables, but lists startVariables = %s", raw)
	}
}
