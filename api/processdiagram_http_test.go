package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Saving a layout onto a deployed definition (ADR-draft-adjust-a-deployed-diagram).
//
// The endpoint's whole claim is that nothing but the picture moves, so these tests
// check the two halves separately: what arrives (the new coordinates, on every pool
// of a collaboration, surviving a restart) and what must not (the version, the
// running instances, and any edit that is not layout).

// diagramBPMN is a deployable model that already carries a diagram, so a test can
// move a shape and compare. One service task keeps an instance alive: a definition
// with a running instance is precisely the one a redeploy could not fix.
const diagramBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Definitions_d" targetNamespace="http://atlas/bpmn">
  <process id="drawn" isExecutable="true">
    <startEvent id="start" name="Los"/>
    <serviceTask id="task" name="Zahlung">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end" name="Fertig"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="drawn">
    <bpmndi:BPMNShape id="start_di" bpmnElement="start"><dc:Bounds x="150" y="100" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="task_di" bpmnElement="task"><dc:Bounds x="240" y="78" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="end_di" bpmnElement="end"><dc:Bounds x="400" y="100" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f1_di" bpmnElement="f1"><di:waypoint x="186" y="118"/><di:waypoint x="240" y="118"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f2_di" bpmnElement="f2"><di:waypoint x="340" y="118"/><di:waypoint x="400" y="118"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</definitions>`

// moved is the same model with one shape somewhere else — the smallest thing an
// operator does when a badge covers a label.
func moved(model string) string {
	return strings.Replace(model, `x="240" y="78"`, `x="240" y="420"`, 1)
}

// processXML fetches what the UI would render for a definition.
func processXML(t *testing.T, ts *httptest.Server, key string) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/"+key+"/xml", "", "")
	if code != http.StatusOK {
		t.Fatalf("get xml status=%d body=%s", code, body)
	}
	return string(body)
}

// listedVersion returns the version the process listing reports for key, and the
// layout-adjustment stamp beside it.
func listedVersion(t *testing.T, ts *httptest.Server, key uint64) (int32, int64) {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes status=%d body=%s", code, body)
	}
	var procs []struct {
		Key              uint64 `json:"key"`
		Version          int32  `json:"version"`
		DiagramUpdatedAt int64  `json:"diagramUpdatedAt"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	for _, p := range procs {
		if p.Key == key {
			return p.Version, p.DiagramUpdatedAt
		}
	}
	t.Fatalf("no process with key %d in listing %s", key, body)
	return 0, 0
}

// TestUpdateProcessDiagramKeepsTheDeployment is the point of the endpoint: the new
// coordinates are what the views draw, and the definition is otherwise exactly the
// one that was deployed — same key, same version, same running instance. A
// redeploy can do the first of those and none of the rest.
func TestUpdateProcessDiagramKeepsTheDeployment(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", `{"variables":{}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start instance status=%d body=%s", code, body)
	}
	before := countInstances(t, ts)

	code, body := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/diagram", moved(diagramBPMN), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save diagram status=%d body=%s", code, body)
	}
	var saved struct {
		Key              uint64   `json:"key"`
		Updated          []uint64 `json:"updated"`
		DiagramUpdatedAt int64    `json:"diagramUpdatedAt"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	if len(saved.Updated) != 1 || saved.Updated[0] != 1 {
		t.Errorf("updated=%v, want exactly the one definition [1]", saved.Updated)
	}

	got := processXML(t, ts, "1")
	if !strings.Contains(got, `x="240" y="420"`) {
		t.Errorf("the moved shape is not what the view renders:\n%s", got)
	}
	if strings.Contains(got, `x="240" y="78"`) {
		t.Errorf("the old position survived:\n%s", got)
	}
	// No new version, and no version at all beyond the deployed one.
	if version, stamp := listedVersion(t, ts, 1); version != 1 || stamp == 0 {
		t.Errorf("after the layout save: version=%d stamp=%d, want version 1 with a stamp", version, stamp)
	}
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/2/xml", "", ""); code != http.StatusNotFound {
		t.Errorf("a second definition exists (status=%d body=%s) — saving a layout must not deploy", code, body)
	}
	// And the instance that was running is still the same running instance.
	if after := countInstances(t, ts); after != before {
		t.Errorf("instances went from %d to %d — a layout save must not touch them", before, after)
	}
}

// TestUpdateProcessDiagramSurvivesRestart: the adjusted picture is the deployment's
// picture from now on, which means it is on disk and not merely in the registry.
func TestUpdateProcessDiagramSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first := boot(t, dir)
	if code, body := doReq(t, first.ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := doReq(t, first.ts, http.MethodPut, "/api/v1/processes/1/diagram", moved(diagramBPMN), "application/xml"); code != http.StatusOK {
		t.Fatalf("save diagram status=%d body=%s", code, body)
	}
	first.shutdown()

	second := boot(t, dir)
	defer second.shutdown()
	if got := processXML(t, second.ts, "1"); !strings.Contains(got, `x="240" y="420"`) {
		t.Errorf("after restart the old diagram came back:\n%s", got)
	}
	if version, stamp := listedVersion(t, second.ts, 1); version != 1 || stamp == 0 {
		t.Errorf("after restart: version=%d stamp=%d, want version 1 with its stamp restored", version, stamp)
	}
	// And it is still a definition, not just a picture: recovery recompiles the stored
	// document (ADR-0019), so an adjusted record that had lost or reshaped its semantic
	// half would come back as a definition that no longer runs — or not come back at all.
	if code, body := doReq(t, second.ts, http.MethodPost, "/api/v1/processes/1/instances", `{"variables":{}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("after restart, starting an instance status=%d body=%s", code, body)
	}
}

// TestUpdateProcessDiagramRefusesAModelEdit: the endpoint takes a layout, and the
// only way to be sure it took nothing else is to refuse a document that carries
// something else. A caller who renamed a task gets told to deploy, not a silently
// discarded rename.
func TestUpdateProcessDiagramRefusesAModelEdit(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	edited := strings.Replace(moved(diagramBPMN), `name="Zahlung"`, `name="Zahlung neu"`, 1)
	code, body := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/diagram", edited, "application/xml")
	if code != http.StatusConflict {
		t.Fatalf("edited model status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "deploy") {
		t.Errorf("the refusal does not say what to do instead: %s", body)
	}
	// Nothing landed — not even the layout half of the submitted document.
	if got := processXML(t, ts, "1"); strings.Contains(got, `y="420"`) || strings.Contains(got, "Zahlung neu") {
		t.Errorf("a refused save left something behind:\n%s", got)
	}
}

// TestUpdateProcessDiagramCollaborationMovesEveryPool: one drawing deploys as one
// definition per pool (ADR-0023), and each holds its own copy of it. Adjusting one
// has to move all of them, or the pools of a single picture disagree about where
// they are.
func TestUpdateProcessDiagramCollaborationMovesEveryPool(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", collabBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deployed collabDeployResp
	if err := json.Unmarshal(body, &deployed); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if len(deployed.Deployments) != 2 {
		t.Fatalf("expected two pools, got %d", len(deployed.Deployments))
	}
	// The model carries no diagram, so the view renders a generated one; what comes
	// back from an editor is that layout, adjusted. Take it from the server and move
	// a shape in it, exactly as the Modeler would.
	drawn := processXML(t, ts, "1")
	if !strings.Contains(drawn, "BPMNShape") {
		t.Fatalf("expected a generated layout to adjust:\n%s", drawn)
	}
	adjusted := bumpFirstY(t, drawn)

	code, body = doReq(t, ts, http.MethodPut, "/api/v1/processes/1/diagram", adjusted, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save diagram status=%d body=%s", code, body)
	}
	var saved struct {
		Updated []uint64 `json:"updated"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	if len(saved.Updated) != 2 {
		t.Errorf("updated=%v, want both pools of the collaboration", saved.Updated)
	}
	for _, key := range []string{"1", "2"} {
		if got := processXML(t, ts, key); !strings.Contains(got, `y="9999"`) {
			t.Errorf("pool %s still draws the old layout:\n%s", key, got)
		}
	}
}

// bumpFirstY moves the first shape in a diagram to an unmistakable y, so a test can
// tell the adjusted layout from the generated one it started as.
func bumpFirstY(t *testing.T, xml string) string {
	t.Helper()
	i := strings.Index(xml, ` y="`)
	if i < 0 {
		t.Fatalf("no shape bounds to move in:\n%s", xml)
	}
	j := strings.Index(xml[i+4:], `"`)
	return xml[:i] + ` y="9999"` + xml[i+4+j+1:]
}

// TestUpdateProcessDiagramErrors covers the exits that are not about the model: a
// key that is not a number, a definition that does not exist, an empty body, and a
// document with no diagram in it to take.
func TestUpdateProcessDiagramErrors(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/processes/abc/diagram", diagramBPMN, "application/xml"); code != http.StatusBadRequest {
		t.Errorf("bad key status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/processes/999/diagram", diagramBPMN, "application/xml"); code != http.StatusNotFound {
		t.Errorf("missing definition status=%d, want 404", code)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/diagram", "", "application/xml"); code != http.StatusBadRequest {
		t.Errorf("empty body status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/diagram", "<definitions", "application/xml"); code != http.StatusBadRequest {
		t.Errorf("malformed xml status=%d, want 400", code)
	}
	// A well-formed model of the right process, with the diagram cut out: there is
	// nothing to save, and saving it would blank the picture.
	i := strings.Index(diagramBPMN, "<bpmndi:BPMNDiagram")
	j := strings.Index(diagramBPMN, "</bpmndi:BPMNDiagram>") + len("</bpmndi:BPMNDiagram>")
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/processes/1/diagram",
		diagramBPMN[:i]+diagramBPMN[j:], "application/xml"); code != http.StatusBadRequest {
		t.Errorf("model with no diagram status=%d body=%s, want 400", code, body)
	}
}

// TestUpdateProcessDiagramSidecarCorrupt covers the persist path's read failure: the
// record the adjustment has to rewrite can no longer be decoded, so the save is
// refused with a 500 rather than applied to the registry alone. Durable before
// visible is the whole reason the write is ordered that way (I2).
func TestUpdateProcessDiagramSidecarCorrupt(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if err := os.WriteFile(filepath.Join(dir, "deployments", "1.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt record: %v", err)
	}
	if code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/diagram", moved(diagramBPMN), "application/xml"); code != http.StatusInternalServerError {
		t.Fatalf("corrupt-record save status=%d body=%s, want 500", code, body)
	}
	// And the registry still draws the deployed picture: nothing was applied.
	if got := processXML(t, s.ts, "1"); !strings.Contains(got, `x="240" y="78"`) {
		t.Errorf("a refused save changed the rendered diagram:\n%s", got)
	}
}

// TestUpdateProcessDiagramSidecarMissing covers the divergence path: the definition
// is in the registry but its record is gone, so there is nothing to save onto and
// the answer is the one a missing key gets rather than a 200 that persisted nothing.
func TestUpdateProcessDiagramSidecarMissing(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if err := os.Remove(filepath.Join(dir, "deployments", "1.json")); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	if code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/diagram", moved(diagramBPMN), "application/xml"); code != http.StatusNotFound {
		t.Fatalf("missing-record save status=%d body=%s, want 404", code, body)
	}
}
