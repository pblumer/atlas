package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// documentedBPMN is a model whose process and every flow node carries a
// <bpmn:documentation> child — the prose the Modeler's properties panel authors
// (ADR-0025). It deliberately ships no diagram interchange, so the server has to
// generate a layout for it on the way out: the interesting case, because that is the
// one path where the server rewrites the model it stored.
const documentedBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="dokumentiert" isExecutable="true">
    <bpmn:documentation>Bearbeitet Anträge von der Prüfung bis zur Auszahlung.</bpmn:documentation>
    <bpmn:startEvent id="start"><bpmn:documentation>Startet, sobald der Antrag eingeht.</bpmn:documentation></bpmn:startEvent>
    <bpmn:serviceTask id="pay">
      <bpmn:documentation>Zahlt über den Zahlungsdienst aus.</bpmn:documentation>
      <bpmn:extensionElements><zeebe:taskDefinition type="payment"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"><bpmn:documentation>Der Antrag ist abgeschlossen.</bpmn:documentation></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="pay"><bpmn:documentation>Immer.</bpmn:documentation></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f2" sourceRef="pay" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

// documentationTexts is what must survive every hop.
var documentationTexts = []string{
	"Bearbeitet Anträge von der Prüfung bis zur Auszahlung.",
	"Startet, sobald der Antrag eingeht.",
	"Zahlt über den Zahlungsdienst aus.",
	"Der Antrag ist abgeschlossen.",
	"Immer.",
}

// A documented model deploys like any other, and the XML the Modeler reads back carries
// every word of it. Documentation is passthrough (ADR-0025): the engine never interprets
// it, so the model is the only place it lives — losing it on the way through the server
// would silently discard the author's documentation.
func TestDocumentedModelDeploysAndRoundTrips(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", documentedBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status = %d, body = %s", code, body)
	}
	var dep struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if dep.ProcessID != "dokumentiert" {
		t.Fatalf("processId = %q, want %q", dep.ProcessID, "dokumentiert")
	}

	code, xml := doReq(t, ts, http.MethodGet, "/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/xml", "", "")
	if code != http.StatusOK {
		t.Fatalf("process xml status = %d, body = %s", code, xml)
	}
	out := string(xml)
	// The model carried no layout, so the server generated one — the rewrite that
	// must not cost the documentation.
	if !strings.Contains(out, "<bpmndi:BPMNDiagram") {
		t.Fatalf("expected a generated diagram in the served XML:\n%s", out)
	}
	for _, want := range documentationTexts {
		if !strings.Contains(out, want) {
			t.Errorf("served XML lost documentation %q\n---\n%s", want, out)
		}
	}
}

// Auto-layout re-flows a diagram's shapes; it must not touch the semantics, and
// documentation is part of the semantics.
func TestAutoLayoutKeepsDocumentation(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/layout", documentedBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("layout status = %d, body = %s", code, body)
	}
	out := string(body)
	for _, want := range documentationTexts {
		if !strings.Contains(out, want) {
			t.Errorf("re-laid-out XML lost documentation %q\n---\n%s", want, out)
		}
	}
}

// A documented model passes the dry-run compile the Problems panel calls, with no
// problems invented for the documentation.
func TestValidateAcceptsDocumentation(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", documentedBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("validate status = %d, body = %s", code, body)
	}
	var res struct {
		Problems []struct {
			Element  string `json:"element"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode validate: %v (%s)", err, body)
	}
	for _, p := range res.Problems {
		if p.Severity == "error" {
			t.Errorf("documented model reported an error problem: %+v", p)
		}
	}
}

// documentedUserTaskBPMN parks on a documented user task, so the Tasks app has prose to
// show the person doing the work. "sign" carries none: an undocumented task must report
// an empty field, not the previous task's text.
const documentedUserTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="freigabe" isExecutable="true">
    <documentation>Freigabe eines Antrags durch zwei Personen.</documentation>
    <startEvent id="start"/>
    <userTask id="review" name="Antrag prüfen">
      <documentation>Vergleiche die Angaben mit den Anlagen. Bei fehlenden Unterlagen ablehnen und den Antragsteller informieren.</documentation>
    </userTask>
    <userTask id="sign" name="Unterschreiben"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="sign"/>
    <sequenceFlow id="f3" sourceRef="sign" targetRef="end"/>
  </process>
</definitions>`

// A user task's documentation is the work instruction for whoever picks it up, so the
// Tasks app gets it on the task itself (ADR-0025 amended) — in the list and on the
// single-task fetch a deep link uses, which must not drift apart.
func TestUserTaskCarriesItsDocumentation(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", documentedUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	type taskRow struct {
		Key           uint64 `json:"key"`
		ElementID     string `json:"elementId"`
		Name          string `json:"name"`
		Documentation string `json:"documentation"`
	}
	const wantDoc = "Vergleiche die Angaben mit den Anlagen. Bei fehlenden Unterlagen ablehnen und den Antragsteller informieren."

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []taskRow
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %v (%s)", err, body)
	}
	if tasks[0].Documentation != wantDoc {
		t.Errorf("list task documentation = %q, want %q", tasks[0].Documentation, wantDoc)
	}

	// The deep-link fetch is enriched by the same code path, so it must agree.
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", tasks[0].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get task: status=%d body=%s", code, body)
	}
	var one taskRow
	if err := json.Unmarshal(body, &one); err != nil {
		t.Fatalf("decode task: %v (%s)", err, body)
	}
	if one.Documentation != wantDoc {
		t.Errorf("get task documentation = %q, want %q", one.Documentation, wantDoc)
	}

	// Complete it: the next task carries none, and the field is simply absent rather
	// than inheriting the previous task's prose.
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", tasks[0].Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	tasks = nil
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %v (%s)", err, body)
	}
	if tasks[0].ElementID != "sign" {
		t.Fatalf("expected to be parked on \"sign\", got %q", tasks[0].ElementID)
	}
	if tasks[0].Documentation != "" {
		t.Errorf("undocumented task reports documentation %q", tasks[0].Documentation)
	}
}
