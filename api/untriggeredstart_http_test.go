package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A lifecycle process: one message start per operation, no none start (ADR-0425).
// Only its triggers can start it.
const severalTriggersBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="m_prov" name="laptop.provision"/>
  <message id="m_deprov" name="laptop.deprovision"/>
  <process id="laptop-lifecycle" isExecutable="true">
    <startEvent id="Provision"><messageEventDefinition messageRef="m_prov"/></startEvent>
    <startEvent id="Deprovision"><messageEventDefinition messageRef="m_deprov"/></startEvent>
    <endEvent id="ProvEnd"/>
    <endEvent id="DeprovEnd"/>
    <sequenceFlow id="f1" sourceRef="Provision" targetRef="ProvEnd"/>
    <sequenceFlow id="f2" sourceRef="Deprovision" targetRef="DeprovEnd"/>
  </process>
</definitions>`

// The same shape with one message start: ADR-0035's permissiveness keeps it
// startable by hand, and ADR-0426 leaves it so.
const singleTriggerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="m_only" name="only.trigger"/>
  <process id="single-trigger" isExecutable="true">
    <startEvent id="Only"><messageEventDefinition messageRef="m_only"/></startEvent>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Only" targetRef="End"/>
  </process>
</definitions>`

// TestStartByHandRefusesAProcessOnlyItsTriggersCanStart: both start routes answer
// 409 for a process with several start events and no none start, naming its start
// events — instead of creating an instance that runs every branch at once.
func TestStartByHandRefusesAProcessOnlyItsTriggersCanStart(t *testing.T) {
	ts := newTestServer(t)
	key := deployXML(t, ts, severalTriggersBPMN)

	for _, tc := range []struct{ name, path, body string }{
		{"by key", fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}"},
		{"by process id", "/api/v1/instances", `{"processId":"laptop-lifecycle"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, tc.path, tc.body, "application/json")
			if code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body=%s", code, body)
			}
			for _, want := range []string{"Provision", "Deprovision", "ADR-0426"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("refusal %s does not name %q", body, want)
				}
			}
		})
	}
	if n := len(instanceKeysOf(t, ts, key)); n != 0 {
		t.Fatalf("instances of the lifecycle process = %d, want 0 — a refusal creates nothing", n)
	}
}

// TestStartByHandStillStartsASingleTriggerProcess: the one-trigger case is not
// touched.
func TestStartByHandStillStartsASingleTriggerProcess(t *testing.T) {
	ts := newTestServer(t)
	key := deployXML(t, ts, singleTriggerBPMN)
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", code, body)
	}
}

// A caller whose call activity targets the several-triggers process above.
const callerOfSeveralTriggersBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="caller" isExecutable="true">
    <startEvent id="Start"/>
    <callActivity id="CallLifecycle">
      <extensionElements><zeebe:calledElement processId="laptop-lifecycle"/></extensionElements>
    </callActivity>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="CallLifecycle"/>
    <sequenceFlow id="f2" sourceRef="CallLifecycle" targetRef="End"/>
  </process>
</definitions>`

// TestProblemsPanelWarnsOfACallIntoSeveralTriggers: the Problems panel says at
// design time what the engine will do at runtime — the call activity parks on an
// incident — as a warning on the call activity, because the target is resolved per
// server and the incident stays the authority (ADR-0426).
func TestProblemsPanelWarnsOfACallIntoSeveralTriggers(t *testing.T) {
	ts := newTestServer(t)
	deployXML(t, ts, severalTriggersBPMN)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", callerOfSeveralTriggersBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("validate: status=%d body=%s", code, body)
	}
	var resp struct {
		Problems []struct {
			Element, Severity, Rule, Message string
		} `json:"problems"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, p := range resp.Problems {
		if p.Rule == "call.untriggered-start" {
			if p.Element != "CallLifecycle" || p.Severity != "warning" {
				t.Errorf("finding = %+v, want a warning on CallLifecycle", p)
			}
			return
		}
	}
	t.Fatalf("no call.untriggered-start finding in %s", body)
}
