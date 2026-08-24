package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// staleGateBPMN writes a script task's result to a dotted target: accepted when it
// was deployed, refused by today's deploy gate (variable.dotted-target). It runs
// start → script → end with no wait, so one drain finishes an instance of it.
const staleGateBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="stale-gate" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="total">
      <extensionElements><zeebe:script expression="= 1" resultVariable="customers.gesamtumsatz"/></extensionElements>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="total"/>
    <sequenceFlow id="f2" sourceRef="total" targetRef="end"/>
  </process>
</definitions>`

// seedDeployment writes a deployment record straight into the store, which is how
// a definition deployed by an *older* build is simulated: it is on disk in the
// shape that build wrote, and the current build meets it for the first time on
// reload — the one path where a rule added since the deploy can be met by a model
// that never had to satisfy it.
func seedDeployment(t *testing.T, dir string, key uint64, processID, xml string, dmnXMLs ...string) {
	t.Helper()
	depDir := filepath.Join(dir, "deployments")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatalf("mkdir deployments: %v", err)
	}
	rec := map[string]any{
		"key":        key,
		"processId":  processID,
		"name":       processID,
		"version":    1,
		"deployedAt": 1,
		"xml":        xml,
	}
	if len(dmnXMLs) > 0 {
		rec["dmnXmls"] = dmnXMLs
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(depDir, fmt.Sprintf("%d.json", key)), data, 0o644); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

// TestStoredDeploymentSurvivesANewValidationRule is the property this split
// exists for: a validation rule added after a definition was deployed must not
// take the server down with it. The engine used to refuse to start at all, which
// turned an upgrade into an outage on a model nobody had touched — every
// definition and every running instance unreachable because one stored model
// would not pass a gate that did not exist when it was deployed.
func TestStoredDeploymentSurvivesANewValidationRule(t *testing.T) {
	dir := t.TempDir()
	seedDeployment(t, dir, 1, "stale-gate", staleGateBPMN)

	s := boot(t, dir) // must not fail: the server comes up with the definition
	defer s.shutdown()

	code, body := doReq(t, s.ts, "GET", "/api/v1/processes", "", "")
	if code != 200 || !strings.Contains(string(body), `"processId":"stale-gate"`) {
		t.Fatalf("processes after reload: status=%d body=%s", code, body)
	}

	// And it is a working definition, not a listing: an instance of it runs to
	// completion, writing the very variable the rule objects to. Validation is a
	// deploy-time gate; what it refuses at deploy still executes exactly as it did.
	code, body = doReq(t, s.ts, "POST", "/api/v1/processes/1/instances", "", "application/json")
	if code != 200 {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	var insts []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		State     string `json:"state"`
	}
	code, body = doReq(t, s.ts, "GET", "/api/v1/instances", "", "")
	if code != 200 {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 || insts[0].ProcessID != "stale-gate" || insts[0].State != "completed" {
		t.Fatalf("instance = %+v, want one completed stale-gate instance (%s)", insts, body)
	}

	// The script task the rule objects to is the one that ran: its step wrote the
	// dotted name, exactly as it did before the rule existed.
	code, body = doReq(t, s.ts, "GET", fmt.Sprintf("/api/v1/instances/%d/timeline", insts[0].Key), "", "")
	if code != 200 || !strings.Contains(string(body), "customers.gesamtumsatz") {
		t.Fatalf("the script task did not write its result: status=%d body=%s", code, body)
	}
}

// TestDeployStillRefusesWhatReloadTolerates is the other half: the gate is skipped
// on reload only. Deploying the same model through the API still fails, with the
// rule named — otherwise the fix would quietly delete the check instead of moving
// it to the moment it belongs to.
func TestDeployStillRefusesWhatReloadTolerates(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, "POST", "/api/v1/deployments", staleGateBPMN, "application/xml")
	if code == 200 {
		t.Fatalf("deploy of a dotted target succeeded: %s", body)
	}
	if !strings.Contains(string(body), "variable.dotted-target") {
		t.Fatalf("deploy rejection does not name the rule: status=%d body=%s", code, body)
	}
}

// mixedDmnModel bundles a decision that compiles ("Menu") with one that no longer
// does ("Broken", reading a variable nothing declares) — the DMN counterpart of a
// BPMN model a rule added since the deploy would refuse.
const mixedDmnModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="mixeddefs" name="mixed" namespace="http://atlas/dmn">
  <decision id="Menu" name="Menu"><literalExpression id="mle"><text>"Fixed"</text></literalExpression></decision>
  <decision id="Broken" name="Broken"><literalExpression id="ble"><text>Nonexistent + 1</text></literalExpression></decision>
</definitions>`

// decidingBPMN delegates to the healthy decision of the model bundled with it, and
// waits for nothing else, so one drain of an instance says whether that decision
// still answers after the model came back from disk.
const decidingBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="with-dmn" isExecutable="true">
    <startEvent id="start"/>
    <businessRuleTask id="decide">
      <extensionElements><calledDecision decisionId="Menu" resultVariable="out"/></extensionElements>
    </businessRuleTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="end"/>
  </process>
</definitions>`

// TestStoredDmnModelSurvivesANewDiagnostic is the DMN half of the same property:
// a deployment record snapshots the DMN models its business rule tasks evaluate
// against, and reloading them applied the deploy gate too — so a decision that
// stopped compiling took the server down exactly like a refused BPMN model. The
// definition now loads, the decisions that still compile still answer, and only
// the broken decision fails, as a job, if something evaluates it.
func TestStoredDmnModelSurvivesANewDiagnostic(t *testing.T) {
	dir := t.TempDir()
	seedDeployment(t, dir, 1, "with-dmn", decidingBPMN, mixedDmnModel)

	s := boot(t, dir) // must not fail: one broken decision is not a boot failure
	defer s.shutdown()

	code, body := doReq(t, s.ts, "GET", "/api/v1/processes", "", "")
	if code != 200 || !strings.Contains(string(body), `"processId":"with-dmn"`) {
		t.Fatalf("processes after reload: status=%d body=%s", code, body)
	}

	// The model is registered, not merely tolerated: an instance delegates to the
	// decision that still compiles and gets its answer back.
	if code, body := doReq(t, s.ts, "POST", "/api/v1/processes/1/instances", "", "application/json"); code != 200 {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	var insts []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	code, body = doReq(t, s.ts, "GET", "/api/v1/instances", "", "")
	if code != 200 {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 || insts[0].State != "completed" {
		t.Fatalf("instance = %+v, want one completed instance (%s)", insts, body)
	}
	code, body = doReq(t, s.ts, "GET", fmt.Sprintf("/api/v1/instances/%d/timeline", insts[0].Key), "", "")
	if code != 200 || !strings.Contains(string(body), "Fixed") {
		t.Fatalf("the healthy decision did not answer: status=%d body=%s", code, body)
	}
}

// TestNewFailsOnUnparsableStoredDmnModel draws the DMN line where the BPMN line is
// drawn: XML temis cannot parse yields no model at all, so it stays a hard startup
// error naming the record, not a silently missing decision.
func TestNewFailsOnUnparsableStoredDmnModel(t *testing.T) {
	dir := t.TempDir()
	seedDeployment(t, dir, 1, "with-dmn", decidingBPMN, "<this is not dmn")

	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err == nil {
		srv.Close()
		t.Fatal("New with an unparsable stored DMN model: want error, got nil")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "deployments", "1.json")) {
		t.Fatalf("error does not name the record to fix: %v", err)
	}
}
