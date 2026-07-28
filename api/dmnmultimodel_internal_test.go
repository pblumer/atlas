package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// twoModelBPMN is a Start → BusinessRuleTask(Dish, from model "dish") →
// BusinessRuleTask(Menu, from model "menu") → End process: its two business rule
// tasks call decisions that live in *different* DMN models, the case a single
// bundled model cannot cover.
const twoModelBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="both" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="d1">
      <extensionElements>
        <calledDecision decisionId="Dish" resultVariable="dish"/>
        <decisionInput name="Season" value="Winter"/>
      </extensionElements>
    </businessRuleTask>
    <businessRuleTask id="d2">
      <extensionElements>
        <calledDecision decisionId="Menu" resultVariable="menu"/>
      </extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="d1"/>
    <sequenceFlow id="f2" sourceRef="d1" targetRef="d2"/>
    <sequenceFlow id="f3" sourceRef="d2" targetRef="e"/>
  </process>
</definitions>`

// TestDeploySpanningTwoModels is the end-to-end proof of multi-model bundling: a
// process whose business rule tasks reference decisions from two different DMN
// models deploys (rather than being refused for lack of one covering model), and an
// instance evaluates both decisions and runs to completion. This is the case the
// user hit — two decisions in two models — that the single-model deploy gate wrongly
// refused.
func TestDeploySpanningTwoModels(t *testing.T) {
	srv, dir := newValidateServer(t) // seeds dish.dmn (decision "Dish")
	x := deployTestHarness{t, srv.Handler()}
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "menu.dmn"), []byte(menuDMNModel), 0o644); err != nil {
		t.Fatalf("write menu model: %v", err)
	}
	// A reference for each model so the deploy bundling can resolve both.
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add dish ref: %d %s", code, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Menu","modelRef":"menu"}`); code != http.StatusOK {
		t.Fatalf("add menu ref: %d %s", code, b)
	}

	// The deploy is accepted (200), not refused (409): the two models together cover
	// the two decisions.
	code, b := x.do(http.MethodPost, "/api/v1/deployments", twoModelBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy spanning two models: status=%d body=%s, want 200 (both models bundled)", code, b)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(b, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	// An instance evaluates both decisions and completes — no token parks on either
	// business rule task.
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, b)
	}

	// Find the instance and read its decision evaluations: both decisions must have run.
	_, list := x.do(http.MethodGet, "/api/v1/instances", "")
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(list, &instances); err != nil || len(instances) == 0 {
		t.Fatalf("decode instances: %v body=%s", err, list)
	}
	_, decs := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions", instances[0].Key), "")
	var evals []struct {
		DecisionID string `json:"decisionId"`
	}
	if err := json.Unmarshal(decs, &evals); err != nil {
		t.Fatalf("decode decisions: %v body=%s", err, decs)
	}
	got := map[string]bool{}
	for _, e := range evals {
		got[e.DecisionID] = true
	}
	if !got["Dish"] || !got["Menu"] {
		t.Fatalf("evaluated decisions = %v, want both Dish and Menu (each from its own model)", got)
	}
}

// TestMultiModelDeploySurvivesRestart proves the multi-model bundle is durable: a
// process spanning two models is deployed, the server is restarted (recovery re-reads
// the persisted deployment), and both decisions still evaluate — state after replay
// equals state built live (AGENTS.md). It exercises the DMNXMLs round-trip through
// the deploy store and loadDeployments.
func TestMultiModelDeploySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	srv, closeSrv := bootAPIWithModels(t, dir, true) // seeds dish.dmn
	x := deployTestHarness{t, srv.Handler()}
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "menu.dmn"), []byte(menuDMNModel), 0o644); err != nil {
		t.Fatalf("write menu model: %v", err)
	}
	x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`)
	x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Menu","modelRef":"menu"}`)
	if code, b := x.do(http.MethodPost, "/api/v1/deployments", twoModelBPMN); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, b)
	}
	closeSrv()

	// Restart over the same dir: recovery re-registers both bundled models.
	srv2, closeSrv2 := bootAPIWithModels(t, dir, false)
	defer closeSrv2()
	x2 := deployTestHarness{t, srv2.Handler()}

	_, procs := x2.do(http.MethodGet, "/api/v1/processes", "")
	var deployed []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(procs, &deployed); err != nil || len(deployed) == 0 {
		t.Fatalf("processes after restart: %v body=%s", err, procs)
	}
	if code, b := x2.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deployed[0].Key), "{}"); code != http.StatusOK {
		t.Fatalf("create instance after restart: status=%d body=%s", code, b)
	}
	_, list := x2.do(http.MethodGet, "/api/v1/instances", "")
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(list, &instances); err != nil || len(instances) == 0 {
		t.Fatalf("instances after restart: %v body=%s", err, list)
	}
	_, decs := x2.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions", instances[0].Key), "")
	var evals []struct {
		DecisionID string `json:"decisionId"`
	}
	if err := json.Unmarshal(decs, &evals); err != nil {
		t.Fatalf("decode decisions after restart: %v body=%s", err, decs)
	}
	got := map[string]bool{}
	for _, e := range evals {
		got[e.DecisionID] = true
	}
	if !got["Dish"] || !got["Menu"] {
		t.Fatalf("after restart evaluated = %v, want both Dish and Menu (both models recovered)", got)
	}
}

// TestDeployMissingDecisionStillRefused confirms the gate still refuses a genuinely
// unprovidable decision: multi-model bundling covers decisions that span *existing*
// models, but a decision no model provides must still be a 409, not a model-less
// deploy that can never evaluate.
func TestDeployMissingDecisionStillRefused(t *testing.T) {
	srv, _ := newValidateServer(t) // seeds dish.dmn only
	x := deployTestHarness{t, srv.Handler()}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add dish ref: %d %s", code, b)
	}
	// "Menu" has no model here, so the two-decision process cannot be covered.
	code, b := x.do(http.MethodPost, "/api/v1/deployments", twoModelBPMN)
	if code != http.StatusConflict {
		t.Fatalf("deploy with an unprovided decision: status=%d body=%s, want 409", code, b)
	}
}
