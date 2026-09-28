package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Tests for the bindings ADR-draft-a-business-rule-task-chooses-its-decision-version
// introduces, beyond latest following the newest version (decisiondeployment tests).

// A task that names a version that is not deployed must not deploy — and the refusal
// names what is deployed, so the author can pick one.
func TestAVersionThatIsNotDeployedRefusesTheDeploy(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	deployOneDecision(t, x, "", eligibilityDMN("approve"))
	deployOneDecision(t, x, "", eligibilityDMN("vip"))

	code, body := x.do(http.MethodPost, "/api/v1/deployments", eligibilityProcessAt("orders", 3))
	if code == http.StatusOK {
		t.Fatalf("deploy of a task bound to v3 = %d %s, want a refusal: only v1 and v2 are deployed", code, body)
	}
	for _, want := range []string{"eligibility", "version 3", "v1, v2"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("refusal = %s, want it to name %q", body, want)
		}
	}
	if _, b := x.do(http.MethodGet, "/api/v1/processes", ""); strings.Contains(string(b), `"orders"`) {
		t.Errorf("processes = %s, want nothing deployed from a refused model", b)
	}
}

// Camunda's versionTag binding was read as latest, so a model that pinned a tag ran
// whatever was newest. It is refused now, naming the task.
func TestAVersionTagBindingIsRefusedRatherThanReadAsLatest(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	deployOneDecision(t, x, "", eligibilityDMN("approve"))

	bpmn := strings.Replace(eligibilityProcess("orders", "versionTag"), `bindingType="versionTag"`, `bindingType="versionTag" versionTag="2027"`, 1)
	code, body := x.do(http.MethodPost, "/api/v1/deployments", bpmn)
	if code == http.StatusOK {
		t.Fatalf("deploy of a versionTag binding = %d %s, want a refusal", code, body)
	}
	if !strings.Contains(string(body), "versionTag") || !strings.Contains(string(body), "decide") {
		t.Errorf("refusal = %s, want it to name versionTag and the task", body)
	}
}

// The evaluation says which deployment answered and which version that is. Without
// it an operator sees a table and cannot tell which version ran — the gap the
// 2026-09-28 report was about.
func TestAnEvaluationNamesTheVersionThatAnswered(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	deployOneDecision(t, x, "", eligibilityDMN("approve"))
	v2 := deployOneDecision(t, x, "", eligibilityDMN("vip"))
	procKey := deployProcess(t, x, eligibilityProcess("orders", "latest"))
	if got := runAndReadVerdict(t, x, procKey, "orders"); got != "vip" {
		t.Fatalf("verdict = %q, want vip", got)
	}

	inst := latestActiveInstance(t, x, "orders")
	code, b := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions", inst), "")
	if code != http.StatusOK {
		t.Fatalf("instance decisions: %d %s", code, b)
	}
	var evals []struct {
		DecisionID      string `json:"decisionId"`
		DecisionKey     uint64 `json:"decisionKey"`
		DecisionVersion int32  `json:"decisionVersion"`
	}
	if err := json.Unmarshal(b, &evals); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	if len(evals) != 1 || evals[0].DecisionKey != v2.Key || evals[0].DecisionVersion != 2 {
		t.Fatalf("evaluations = %+v, want one answered by v2 (key %d)", evals, v2.Key)
	}
}

// latestActiveInstance returns the newest active instance of a process.
func latestActiveInstance(t *testing.T, x deployTestHarness, processID string) uint64 {
	t.Helper()
	_, ib := x.do(http.MethodGet, "/api/v1/instances", "")
	var insts []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(listRows(t, ib), &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, ib)
	}
	var key uint64
	for _, in := range insts {
		if in.ProcessID == processID && in.State == "active" && in.Key > key {
			key = in.Key
		}
	}
	if key == 0 {
		t.Fatalf("no active instance of %q in %s", processID, ib)
	}
	return key
}
