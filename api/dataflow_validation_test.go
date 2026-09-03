package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// dataFlowBPMN is one model carrying every finding the information model can
// raise: a type nothing models, a member the class has no attribute for, and a
// read on a parallel branch the writer cannot precede.
const dataFlowBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="sales" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order"/>
    <dataObjectReference id="Ref_w" name="order" dataObjectRef="DO_order"/>
    <dataObjectReference id="Ref_r" name="order" dataObjectRef="DO_order"/>
    <startEvent id="Start_1"/>
    <parallelGateway id="Fork_1"/>
    <task id="Write_1" name="Record order">
      <dataOutputAssociation id="doa"><targetRef>Ref_w</targetRef>
        <assignment><from>= 1</from><to>amount</to></assignment></dataOutputAssociation>
    </task>
    <task id="Read_1" name="Approve">
      <dataInputAssociation id="dia"><sourceRef>Ref_r</sourceRef>
        <assignment><to>orderCopy</to></assignment></dataInputAssociation>
    </task>
    <parallelGateway id="Join_1"/>
    <endEvent id="End_1"/>
    <sequenceFlow id="f1" sourceRef="Start_1" targetRef="Fork_1"/>
    <sequenceFlow id="f2" sourceRef="Fork_1" targetRef="Write_1"/>
    <sequenceFlow id="f3" sourceRef="Fork_1" targetRef="Read_1"/>
    <sequenceFlow id="f4" sourceRef="Write_1" targetRef="Join_1"/>
    <sequenceFlow id="f5" sourceRef="Read_1" targetRef="Join_1"/>
    <sequenceFlow id="f6" sourceRef="Join_1" targetRef="End_1"/>
  </process>
</definitions>`

// TestValidateWithoutAnApplicationIsUnchanged pins the boundary: the Problems panel
// on a draft filed nowhere gets exactly the compiler's findings, as before. The
// information model resolves against one application, so with none named there is
// nothing it could say.
func TestValidateWithoutAnApplicationIsUnchanged(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", dataFlowBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("validate: status=%d body=%s", code, body)
	}
	var resp validateResult
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	for _, p := range resp.Problems {
		if strings.HasPrefix(p.Rule, "data.") {
			t.Errorf("a data-flow finding without an application: %+v", p)
		}
	}
}

// TestValidateResolvesAgainstTheApplicationModel is slice 3 end to end: the panel
// asks about a draft in an application, and the answer now includes what the
// information model knows — a member the class does not have, and a read no writer
// precedes.
func TestValidateResolvesAgainstTheApplicationModel(t *testing.T) {
	ts := newTestServer(t)
	appID := newApplicationWithOrderClass(t, ts)

	code, body := doReq(t, ts, http.MethodPost,
		"/api/v1/validate?applicationId="+appID, dataFlowBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("validate: status=%d body=%s", code, body)
	}
	var resp validateResult
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	byRule := map[string]validateProblem{}
	for _, p := range resp.Problems {
		byRule[p.Rule] = p
	}

	// The write targets `amount`, and the modeled Order has id and placedOn.
	member, ok := byRule["data.unknown-member"]
	if !ok {
		t.Fatalf("no unknown-member finding: %+v", resp.Problems)
	}
	if member.Element != "Write_1" || member.Severity != "error" {
		t.Errorf("unknown-member = %+v, want it anchored to Write_1 as an error", member)
	}
	if !strings.Contains(member.Message, "amount") {
		t.Errorf("message %q does not name the member", member.Message)
	}

	// The read is on the branch beside the write, so no writer precedes it. This is
	// the finding a snapshot of the diagram cannot show.
	read, ok := byRule["data.read-before-write"]
	if !ok {
		t.Fatalf("no read-before-write finding: %+v", resp.Problems)
	}
	if read.Element != "Read_1" || read.Severity != "warning" {
		t.Errorf("read-before-write = %+v, want it anchored to Read_1 as a warning", read)
	}

	// The compiler's own findings are still there — this appends, it does not replace.
	for _, p := range resp.Problems {
		if !strings.HasPrefix(p.Rule, "data.") {
			return
		}
	}
}

// TestValidateReportsAnUnresolvedType covers the headline resolution: a data object
// naming a type the application does not model.
func TestValidateReportsAnUnresolvedType(t *testing.T) {
	ts := newTestServer(t)
	appID := newApplicationWithOrderClass(t, ts)
	const unknownType = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <dataObject id="DO_x" name="claim" itemSubjectRef="Claim"/>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate?applicationId="+appID, unknownType, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("validate: status=%d body=%s", code, body)
	}
	var resp validateResult
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, p := range resp.Problems {
		if p.Rule == "data.unresolved-type" {
			if p.Severity != "warning" {
				t.Errorf("severity = %q, want warning — a type modeled later must not read as broken", p.Severity)
			}
			if !strings.Contains(p.Message, "Claim") {
				t.Errorf("message %q does not name the type", p.Message)
			}
			return
		}
	}
	t.Fatalf("no unresolved-type finding: %+v", resp.Problems)
}

// TestDeployWarnsAboutDataFlow pins that the same findings reach a deploy — and
// that they are warnings: a model is routinely deployed before the vocabulary it
// names exists, exactly as it is deployed before its workers do.
func TestDeployWarnsAboutDataFlow(t *testing.T) {
	ts := newTestServer(t)
	appID := newApplicationWithOrderClass(t, ts)

	code, body := doReq(t, ts, http.MethodPost,
		"/api/v1/deployments?projectId="+appID, dataFlowBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var resp struct {
		Key      uint64   `json:"key"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.Key == 0 {
		t.Fatal("the deploy was refused; these findings must never block one")
	}
	joined := strings.Join(resp.Warnings, "\n")
	if !strings.Contains(joined, "Write_1") || !strings.Contains(joined, "amount") {
		t.Errorf("warnings do not carry the member finding: %v", resp.Warnings)
	}
	if !strings.Contains(joined, "Read_1") {
		t.Errorf("warnings do not carry the read-order finding: %v", resp.Warnings)
	}
}

// TestDeployWithoutAModelIsSilent covers the other half of the same rule: an
// application that has not started modeling gets no data-flow warnings at all, so
// the feature does not shout at every process in an instance that does not use it.
func TestDeployWithoutAModelIsSilent(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", dataFlowBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var resp struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "declares no type") || strings.Contains(w, "no class of that name") {
			t.Errorf("an unmodeled application was warned about types: %q", w)
		}
	}
	// The flow finding is a property of the process alone, so it still stands.
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), "Read_1") {
		t.Errorf("the read-order finding needs no model: %v", resp.Warnings)
	}
}

// newApplicationWithOrderClass creates an application and an information model with
// an Order business object in it, and returns the application id.
func newApplicationWithOrderClass(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Sales"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil || app.ID == "" {
		t.Fatalf("decode application: %v (%s)", err, body)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/infomodel/models",
		fmt.Sprintf(`{"applicationId":%q,"name":"Sales data"}`, app.ID), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create information model: status=%d body=%s", code, body)
	}
	var model struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}
	const classes = `{"classes":[{"id":"c1","name":"Order","stereotype":"businessObject",
	  "identity":["id"],
	  "attributes":[{"name":"id","type":"string","multiplicity":"1"},
	                {"name":"placedOn","type":"date","multiplicity":"0..1"}]}]}`
	if code, b := doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+model.ID, classes, "application/json"); code != http.StatusOK {
		t.Fatalf("save classes: status=%d body=%s", code, b)
	}
	return app.ID
}
