package api_test

import (
	"encoding/json"
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
