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

// TestDeployWarnsOfACallIntoSeveralTriggers: the same finding reaches the deploy
// response as a warning, and the deploy goes through — the target is resolved per
// server, and the incident at runtime is the authority.
func TestDeployWarnsOfACallIntoSeveralTriggers(t *testing.T) {
	ts := newTestServer(t)
	deployXML(t, ts, severalTriggersBPMN)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", callerOfSeveralTriggersBPMN, "application/xml")
	if code != http.StatusOK || !strings.Contains(string(body), "park on an incident") {
		t.Fatalf("deploy: %d (%s), want 200 with the warning", code, body)
	}
}

// TestCSVUploadRefusesAProcessOnlyItsTriggersCanStart: a CSV batch is a start by hand
// like any other, and is refused the same way (ADR-0426).
func TestCSVUploadRefusesAProcessOnlyItsTriggersCanStart(t *testing.T) {
	ts := newTestServer(t)
	key := deployXML(t, ts, severalTriggersBPMN)
	upload, ct := buildCSVUpload(t, "records.csv", strptr(sampleRecordsCSV), strptr(validCSVConfig))
	code, body := postMultipart(t, ts, fmt.Sprintf("/api/v1/processes/%d/instances-from-csv", key), upload, ct)
	if code != http.StatusConflict || !strings.Contains(string(body), "ADR-0426") {
		t.Fatalf("CSV start: %d (%s), want 409 citing ADR-0426", code, body)
	}
}

// TestPublicStartRefusesAProcessOnlyItsTriggersCanStart: a link minted while the
// process had a start form stops starting it once its newest version can only be
// entered through its triggers — with the answer a non-executable process gets,
// because the person filling in the form cannot act on the model's shape.
func TestPublicStartRefusesAProcessOnlyItsTriggersCanStart(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", startFormBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy v1: %d (%s)", code, body)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"onboard"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var link struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, body)
	}
	v2 := strings.Replace(severalTriggersBPMN, `id="laptop-lifecycle"`, `id="onboard"`, 1)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", v2, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy v2: %d (%s)", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", "{}", "application/json"); code != http.StatusConflict {
		t.Fatalf("public start: %d (%s), want 409", code, body)
	}
}

// TestProblemsPanelReadsATargetFromTheSameModel: a caller and its target drawn in one
// model are checked against each other, not against whatever is deployed — the
// version in the model is the one that will deploy with it.
func TestProblemsPanelReadsATargetFromTheSameModel(t *testing.T) {
	ts := newTestServer(t)
	both := strings.Replace(callerOfSeveralTriggersBPMN, "</definitions>",
		severalTriggersBPMN[strings.Index(severalTriggersBPMN, "<message"):], 1)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", both, "application/xml")
	if code != http.StatusOK || !strings.Contains(string(body), "call.untriggered-start") {
		t.Fatalf("validate: %d (%s), want the finding from the sibling process", code, body)
	}
}

// TestAReturnRefusesADeprovisioningOnlyItsTriggersCanStart: giving a line back starts
// its deprovisioning by hand, and that is refused for a process only its triggers can
// start — the return stays recorded, and the answer says why nothing runs.
func TestAReturnRefusesADeprovisioningOnlyItsTriggersCanStart(t *testing.T) {
	ts, admin, _, ord := aServerWithAnOrderFor(t, "alice")
	deprov := strings.Replace(severalTriggersBPMN, `id="laptop-lifecycle"`, `id="deprov"`, 1)
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", deprov); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/vpn", `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("report done: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/orders/"+ord+"/lines/vpn/return", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "ADR-0426") {
		t.Fatalf("return: %d (%s), want the refusal citing ADR-0426", code, body)
	}
}
