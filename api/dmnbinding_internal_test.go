package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// bindingBPMN is a business rule task (static Season=Winter input, result written to
// "dish") followed by a user task so the instance parks after the decision and its
// result variable can be read. The binding is templated.
func bindingBPMN(processID, binding string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="` + processID + `" isExecutable="true">
	    <startEvent id="s"/>
	    <businessRuleTask id="decide">
	      <extensionElements>
	        <calledDecision decisionId="Dish" resultVariable="dish" bindingType="` + binding + `"/>
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

// TestDecisionBindingLatestVsDeployment is the end-to-end proof of decision binding
// (ADR-0063): deploy a process pinned to its deployment, then change the decision
// and deploy a latest-bound process. A deployment-bound instance keeps evaluating
// the version it was deployed with; a latest-bound instance sees the newest.
func TestDecisionBindingLatestVsDeployment(t *testing.T) {
	srv, _ := newValidateServer(t) // seeds dmn-models/dish.dmn (v1: Winter → Roastbeef)
	x := deployTestHarness{t, srv.Handler()}

	// A reference makes the seeded model resolvable to the single deploy.
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}

	// Deploy the deployment-bound process while the model is v1 (Winter → Roastbeef).
	pinnedKey := deployProc(t, x, bindingBPMN("pinned", "deployment"))

	// Change the decision to v2 (Winter → Steak) and deploy the latest-bound process.
	v2 := strings.Replace(validDMNModel, "Roastbeef", "Steak", 1)
	if code, b := x.do(http.MethodPost, "/api/v1/dmn-models?handle=dish", v2); code != http.StatusOK {
		t.Fatalf("overwrite model: %d %s", code, b)
	}
	trackingKey := deployProc(t, x, bindingBPMN("tracking", "latest"))

	// The deployment-bound instance still evaluates v1; the latest-bound instance v2.
	if got := runAndReadDish(t, x, pinnedKey, "pinned"); got != "Roastbeef" {
		t.Errorf("deployment-bound result = %q, want Roastbeef (pinned to deploy-time v1)", got)
	}
	if got := runAndReadDish(t, x, trackingKey, "tracking"); got != "Steak" {
		t.Errorf("latest-bound result = %q, want Steak (newest deployed v2)", got)
	}
}

func deployProc(t *testing.T, x deployTestHarness, bpmn string) uint64 {
	t.Helper()
	code, b := x.do(http.MethodPost, "/api/v1/deployments", bpmn)
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(b, &dep)
	return dep.Key
}

// runAndReadDish creates an instance of the process, which parks at the user task
// after the decision, then reads the "dish" result variable off that instance.
func runAndReadDish(t *testing.T, x deployTestHarness, key uint64, processID string) string {
	t.Helper()
	if code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, b)
	}
	_, ib := x.do(http.MethodGet, "/api/v1/instances", "")
	var insts []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		State     string `json:"state"`
	}
	_ = json.Unmarshal(ib, &insts)
	var instKey uint64
	for _, in := range insts {
		if in.ProcessID == processID && in.State == "active" {
			instKey = in.Key
		}
	}
	if instKey == 0 {
		t.Fatalf("no active instance for %q in %s", processID, ib)
	}
	_, vb := x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", instKey), "")
	var vars map[string]any
	if err := json.Unmarshal(vb, &vars); err != nil {
		t.Fatalf("decode variables %s: %v", vb, err)
	}
	dish, ok := vars["dish"]
	if !ok {
		t.Fatalf("no \"dish\" variable on instance %d: %s", instKey, vb)
	}
	return fmt.Sprint(dish)
}
