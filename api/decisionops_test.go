package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// menuDMNModel is a second, input-free literal-expression decision ("Menu"), so a
// test can deploy two distinct decisions and exercise the overview's cross-decision
// ordering.
const menuDMNModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="menudefs" name="menu" namespace="http://atlas/dmn">
  <decision id="Menu" name="Menu"><literalExpression id="mle"><text>"Fixed"</text></literalExpression></decision>
</definitions>`

// brtBPMN is a Start → BusinessRuleTask(decisionID) → userTask → End process, so an
// instance parks after the decision. The decision id and process id are templated.
func brtBPMN(processID, decisionID string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="` + processID + `" isExecutable="true">
	    <startEvent id="s"/>
	    <businessRuleTask id="decide">
	      <extensionElements>
	        <calledDecision decisionId="` + decisionID + `" resultVariable="out"/>
	        <decisionInput name="Season" value="Winter"/>
	      </extensionElements>
	    </businessRuleTask>
	    <userTask id="wait"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
	    <sequenceFlow id="f2" sourceRef="decide" targetRef="wait"/>
	    <sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>
	  </process>
	</definitions>`
}

// deployedDecision mirrors the handleDeployedDecisions response row, for decoding
// in these tests.
type deployedDecision struct {
	DecisionID  string `json:"decisionId"`
	Local       bool   `json:"local"`
	Evaluations int    `json:"evaluations"`
	Processes   []struct {
		ProcessID string `json:"processId"`
		Name      string `json:"name"`
		Key       uint64 `json:"key"`
		Version   int32  `json:"version"`
	} `json:"processes"`
	LastEvaluatedAt int64 `json:"lastEvaluatedAt"`
}

// TestDeployedDecisionsEmpty covers the no-decisions state: with nothing deployed,
// the Operations decisions overview is an empty array and a 200 — a convenience
// read, not an existence check.
func TestDeployedDecisionsEmpty(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	code, b := x.do(http.MethodGet, "/api/v1/decisions/deployed", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", code, b)
	}
	if string(b) != "[]\n" && string(b) != "[]" {
		t.Fatalf("body=%q, want empty array", b)
	}
}

// TestDeployedDecisionsListsDeployedAndUsed deploys the dinner process (which calls
// the local DMN decision "Dish"), lists the overview before running anything — the
// decision must already appear, tied to its process, with zero evaluations, exactly
// like a deployed process with no instances — then runs one instance and confirms
// the evaluation count and last-evaluated time advance.
func TestDeployedDecisionsListsDeployedAndUsed(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Dinner")
	x.saveDraft(pid, dinnerBPMN)
	x.addRef(pid, "Dish decision", "dish")

	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	key := rep.Definitions[0].Key

	// Before any run: the deployed decision shows with its process and no usage.
	dec := getDeployed(t, x, "Dish")
	if !dec.Local {
		t.Errorf("Local = false, want true (a locally evaluated DMN decision)")
	}
	if dec.Evaluations != 0 {
		t.Errorf("Evaluations = %d, want 0 before any run", dec.Evaluations)
	}
	if dec.LastEvaluatedAt != 0 {
		t.Errorf("LastEvaluatedAt = %d, want 0 before any run", dec.LastEvaluatedAt)
	}
	if len(dec.Processes) != 1 || dec.Processes[0].Key != key {
		t.Fatalf("processes = %+v, want the one deployed process key %d", dec.Processes, key)
	}

	// Run an instance: the decision evaluates once, so usage advances.
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, b)
	}
	dec = getDeployed(t, x, "Dish")
	if dec.Evaluations != 1 {
		t.Errorf("Evaluations = %d, want 1 after one run", dec.Evaluations)
	}
	if dec.LastEvaluatedAt == 0 {
		t.Errorf("LastEvaluatedAt = 0, want a set timestamp after one run")
	}
}

// TestDeployedDecisionsRollup exercises the overview's rollup across processes and
// versions and its ordering. Two processes reference decision "Dish" and one of them
// is deployed twice, so "Dish" must roll up to one row listing both processes with
// the redeployed one pinned at its latest version — the decision analogue of the
// instances view's one-row-per-process collapse. A second, unrelated decision "Menu"
// gives a second row: before any run both decisions tie at zero evaluations and sort
// by id ("Dish" before "Menu"); after a "Dish" instance runs, usage breaks the tie
// and "Dish" leads by evaluation count.
func TestDeployedDecisionsRollup(t *testing.T) {
	srv, dir := newValidateServer(t) // seeds dish.dmn (decision "Dish")
	x := deployTestHarness{t, srv.Handler()}
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "menu.dmn"), []byte(menuDMNModel), 0o644); err != nil {
		t.Fatalf("write menu model: %v", err)
	}
	// References make both seeded models resolvable to a direct deploy's DMN bundling.
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add dish ref: %d %s", code, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Menu","modelRef":"menu"}`); code != http.StatusOK {
		t.Fatalf("add menu ref: %d %s", code, b)
	}

	// Two processes call "Dish"; "beta" is deployed twice (v1, then v2).
	alphaKey := deployProc(t, x, brtBPMN("alpha", "Dish"))
	deployProc(t, x, brtBPMN("beta", "Dish"))
	betaV2Key := deployProc(t, x, brtBPMN("beta", "Dish"))
	// A third process calls "Menu".
	deployProc(t, x, brtBPMN("gamma", "Menu"))

	// Before any run: "Dish" rolls up both processes (beta at its latest version) and
	// the two decisions tie at zero, so they order by id — "Dish" first.
	rows := listDeployed(t, x)
	if len(rows) != 2 || rows[0].DecisionID != "Dish" || rows[1].DecisionID != "Menu" {
		t.Fatalf("rows = %+v, want [Dish, Menu] (tie broken by id)", rows)
	}
	dish := rows[0]
	if len(dish.Processes) != 2 {
		t.Fatalf("Dish processes = %+v, want 2 (alpha, beta)", dish.Processes)
	}
	byPID := map[string]uint64{}
	verByPID := map[string]int32{}
	for _, p := range dish.Processes {
		byPID[p.ProcessID] = p.Key
		verByPID[p.ProcessID] = p.Version
	}
	if byPID["alpha"] != alphaKey {
		t.Errorf("alpha key = %d, want %d", byPID["alpha"], alphaKey)
	}
	if verByPID["beta"] != 2 || byPID["beta"] != betaV2Key {
		t.Errorf("beta ref = key %d v%d, want key %d v2 (latest of two deploys)", byPID["beta"], verByPID["beta"], betaV2Key)
	}

	// Run a "Dish" instance; usage now breaks the tie and "Dish" leads by evaluations.
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", alphaKey), "{}"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, b)
	}
	rows = listDeployed(t, x)
	if rows[0].DecisionID != "Dish" || rows[0].Evaluations != 1 {
		t.Fatalf("rows[0] = %+v, want Dish with 1 evaluation leading", rows[0])
	}
	if rows[1].DecisionID != "Menu" || rows[1].Evaluations != 0 {
		t.Fatalf("rows[1] = %+v, want Menu with 0 evaluations", rows[1])
	}
}

// listDeployed fetches and decodes the whole decisions overview.
func listDeployed(t *testing.T, x deployTestHarness) []deployedDecision {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/decisions/deployed", "")
	if code != http.StatusOK {
		t.Fatalf("deployed decisions status=%d body=%s", code, b)
	}
	var rows []deployedDecision
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode deployed decisions: %v", err)
	}
	return rows
}

// TestDecisionEvaluationsDrilldown deploys the dinner process (decision "Dish"),
// runs an instance, then reads the per-decision drill-down. The evaluation must come
// back tied to the instance it ran in, its diagram element, and the exact
// inputs/outputs/trace an operator needs to debug what the decision saw. A different
// decision id yields an empty list — the scan filters by id.
func TestDecisionEvaluationsDrilldown(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	pid := x.mkProject("Dinner")
	x.saveDraft(pid, dinnerBPMN)
	x.addRef(pid, "Dish decision", "dish")
	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	key := rep.Definitions[0].Key
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, b)
	}

	code, b = x.do(http.MethodGet, "/api/v1/decisions/Dish/evaluations", "")
	if code != http.StatusOK {
		t.Fatalf("evaluations status=%d body=%s", code, b)
	}
	var rows []struct {
		At          int64           `json:"at"`
		InstanceKey uint64          `json:"instanceKey"`
		ProcessID   string          `json:"processId"`
		ElementID   string          `json:"elementId"`
		Inputs      json.RawMessage `json:"inputs"`
		Outputs     json.RawMessage `json:"outputs"`
		Trace       json.RawMessage `json:"trace"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1; body=%s", len(rows), b)
	}
	r := rows[0]
	if r.InstanceKey == 0 {
		t.Errorf("instanceKey = 0, want the running instance")
	}
	if r.ProcessID != "dinner" {
		t.Errorf("processId = %q, want dinner", r.ProcessID)
	}
	if r.ElementID != "decide" {
		t.Errorf("elementId = %q, want decide (the business rule task's diagram id)", r.ElementID)
	}
	if string(r.Inputs) != `{"Season":"Winter"}` {
		t.Errorf("inputs = %s, want {\"Season\":\"Winter\"}", r.Inputs)
	}
	if string(r.Outputs) != `{"Dish":"Roastbeef"}` {
		t.Errorf("outputs = %s, want {\"Dish\":\"Roastbeef\"}", r.Outputs)
	}
	if len(r.Trace) == 0 {
		t.Errorf("trace is empty, want the temis explanation; body=%s", b)
	}

	// A different decision id filters to nothing — a 200 empty array, not a 404.
	code, b = x.do(http.MethodGet, "/api/v1/decisions/Nope/evaluations", "")
	if code != http.StatusOK || (string(b) != "[]\n" && string(b) != "[]") {
		t.Fatalf("unknown decision: status=%d body=%q, want 200 []", code, b)
	}
}

// TestListDecisionsIncludesDeployed proves the picker can offer a decision that is
// deployed even when no DMN reference exists for it. It deploys the dinner process
// (which bundles the "dish" model into the registry), deletes the reference, then
// reads the unscoped catalog: the decision must still be listed, sourced from the
// deployed model, with its inputs so the picker can auto-fill them. A project-scoped
// listing, by contrast, stays limited to the project's own references and omits it.
func TestListDecisionsIncludesDeployed(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	pid := x.mkProject("Dinner")
	x.saveDraft(pid, dinnerBPMN)
	code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish decision","modelRef":"dish","projectId":"`+pid+`"}`)
	if code != http.StatusOK {
		t.Fatalf("add ref status=%d body=%s", code, b)
	}
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &ref); err != nil || ref.ID == "" {
		t.Fatalf("decode ref id: %v body=%s", err, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	countDish := func(body []byte) (decisionCatalogItem, int) {
		var items []decisionCatalogItem
		if err := json.Unmarshal(body, &items); err != nil {
			t.Fatalf("decode catalog: %v", err)
		}
		var hit decisionCatalogItem
		n := 0
		for _, it := range items {
			if it.ID == "Dish" {
				hit = it
				n++
			}
		}
		return hit, n
	}
	find := func(body []byte) (decisionCatalogItem, bool) {
		it, n := countDish(body)
		return it, n > 0
	}

	// While the reference still exists, the decision is offered exactly once: the
	// deployed-model source is deduped against the reference that already lists it.
	if _, refBody := x.do(http.MethodGet, "/api/v1/decisions", ""); true {
		if refItem, n := countDish(refBody); n != 1 {
			t.Fatalf("Dish listed %d times with a reference present, want exactly 1; body=%s", n, refBody)
		} else if refItem.ModelRef == "" {
			t.Errorf("with a reference present, Dish ModelRef = empty, want the editable handle")
		}
	}

	// Remove the reference: the decision is now only known through the deployed model.
	if code, b := x.do(http.MethodDelete, "/api/v1/dmnrefs/"+ref.ID, ""); code != http.StatusNoContent {
		t.Fatalf("delete ref status=%d body=%s", code, b)
	}

	// Unscoped: the deployed decision is offered, with its input for auto-fill.
	_, cat := x.do(http.MethodGet, "/api/v1/decisions", "")
	dish, ok := find(cat)
	if !ok {
		t.Fatalf("unscoped catalog omits the deployed decision Dish; body=%s", cat)
	}
	if dish.ModelRef != "" {
		t.Errorf("deployed decision ModelRef = %q, want empty (no editable handle)", dish.ModelRef)
	}
	if len(dish.Inputs) != 1 || dish.Inputs[0].Name != "Season" {
		t.Errorf("inputs = %+v, want one input named Season", dish.Inputs)
	}

	// Project-scoped: deployed decisions are engine-wide, not the project's, so the
	// scoped listing (only project references) no longer lists Dish once the ref is gone.
	_, bs := x.do(http.MethodGet, "/api/v1/decisions?projectId="+pid, "")
	if _, ok := find(bs); ok {
		t.Errorf("project-scoped catalog should omit the deployed-only decision; body=%s", bs)
	}
}

// getDeployed fetches the decisions overview and returns the row for decisionID,
// failing the test if the endpoint errors or the decision is absent.
func getDeployed(t *testing.T, x deployTestHarness, decisionID string) deployedDecision {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/decisions/deployed", "")
	if code != http.StatusOK {
		t.Fatalf("deployed decisions status=%d body=%s", code, b)
	}
	var rows []deployedDecision
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode deployed decisions: %v", err)
	}
	for _, r := range rows {
		if r.DecisionID == decisionID {
			return r
		}
	}
	t.Fatalf("decision %q not listed; body=%s", decisionID, b)
	return deployedDecision{}
}
