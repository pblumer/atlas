package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rowsCheckBPMN is a business rule task over a file (ADR-0548): RowValid, the
// example decision, evaluated once per record of the document in `antrag`.
const rowsCheckBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="rowscheck" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:businessRuleTask id="check">
      <bpmn:extensionElements>
        <zeebe:calledDecision decisionId="RowValid" resultVariable="pruefung"/>
        <atlas:rowsFrom source="antrag" columns="email,group,license"/>
      </bpmn:extensionElements>
    </bpmn:businessRuleTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="check"/>
    <bpmn:sequenceFlow id="f2" sourceRef="check" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// A file uploaded and named in a start is checked record by record by one job of the
// in-process DMN worker: the instance completes with a summary, the result document
// is in the store and bound to the instance, and the decision log holds one record
// for the run (ADR-0548).
func TestABusinessRuleTaskChecksAFile(t *testing.T) {
	srv, dir := newValidateServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := docHTTP(t, ts)

	model, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.dmn"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "rowvalid.dmn"), model, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := call("POST", "/api/v1/applications", "application/json", `{"name":"Datenprüfung"}`)
	var app struct{ ID string }
	if json.Unmarshal(out, &app); app.ID == "" {
		t.Fatalf("application: %d %s", code, out)
	}
	if code, out := call("POST", "/api/v1/dmnrefs", "application/json", `{"name":"RowValid","modelRef":"rowvalid","projectId":"`+app.ID+`"}`); code != http.StatusOK {
		t.Fatalf("dmnref: %d %s", code, out)
	}
	code, out = call("POST", "/api/v1/deployments?projectId="+app.ID, "application/xml", rowsCheckBPMN)
	var dep struct{ Key uint64 }
	if json.Unmarshal(out, &dep); code != http.StatusOK || dep.Key == 0 {
		t.Fatalf("deploy: %d %s", code, out)
	}

	csvText := "email,group,license,note\nada@x.io,users,PRO,a\nbob,users,PRO,b\ncy@x.io,ops,NONE,c\n"
	code, ref := call("POST", "/api/v1/documents?name=antrag.csv", "text/csv", csvText)
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %s", code, ref)
	}
	code, out = call("POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "application/json", `{"variables":{"antrag":`+string(ref)+`}}`)
	var started struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if json.Unmarshal(out, &started); code != http.StatusOK || started.InstanceKey == 0 {
		t.Fatalf("start: %d %s", code, out)
	}

	// The summary, at the instance.
	vars := instanceVars(t, deployTestHarness{t, srv.Handler()}, started.InstanceKey)
	raw, _ := json.Marshal(vars["pruefung"])
	var sum struct {
		Total, Valid, Invalid, Errors int
		Result                        map[string]any
		Failures                      []map[string]any
	}
	if err := json.Unmarshal(raw, &sum); err != nil || sum.Total != 3 || sum.Valid != 1 || sum.Invalid != 2 || sum.Errors != 0 {
		t.Fatalf("summary = %s (%v)", raw, err)
	}
	if len(sum.Failures) != 2 || sum.Failures[0]["_row"] != 3.0 {
		t.Fatalf("failures = %v", sum.Failures)
	}

	// The result document: every record, the file's own columns kept, its verdict after.
	resultID, _ := sum.Result["$document"].(string)
	code, body := call("GET", "/api/v1/documents/"+resultID, "", "")
	if code != http.StatusOK {
		t.Fatalf("download result: %d %s", code, body)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) != 4 || strings.Join(rows[0][:6], ",") != "email,group,license,note,_row,_valid" || rows[3][5] != "false" {
		t.Fatalf("result document = %q (%v)", body, err)
	}
	// It entered the instance with the completion, so it outlives the upload TTL and
	// whoever may read the instance may read it.
	code, meta := call("GET", "/api/v1/documents/"+resultID+"/meta", "", "")
	if code != http.StatusOK || !strings.Contains(string(meta), `"state":"bound"`) {
		t.Fatalf("result meta = %d %s", code, meta)
	}

	// One decision evaluation recorded for the run, not one per record.
	code, out = call("GET", fmt.Sprintf("/api/v1/instances/%d/decisions", started.InstanceKey), "", "")
	if code != http.StatusOK || strings.Count(string(out), `"decisionId":"RowValid"`) != 1 {
		t.Fatalf("decisions = %d %s", code, out)
	}
}
