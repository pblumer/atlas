package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// prefillBPMN is a start -> user task -> end process. The user task carries no I/O
// mapping, so its element-instance scope holds no local variables — the case that
// regressed form prefill (a public-registration approval showing blank fields).
const prefillBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             targetNamespace="http://atlas/test">
  <process id="prefill" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="review" name="Review"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="e"/>
  </process>
</definitions>`

// TestTaskFormPrefillInheritsProcessVariables is the regression guard for the blank
// approval form: a top-level user task with no input mappings must still prefill
// from the process variables it inherits. The Tasks app reads the task's
// element-instance scope for prefill; that scope is empty here, so the read must
// resolve up the scope chain to the process root (ADR-0068) rather than return {}.
func TestTaskFormPrefillInheritsProcessVariables(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", prefillBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}

	// Start with process variables, as a start/registration form's submission does.
	code, body = doReq(t, ts, http.MethodPost,
		"/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances",
		`{"variables":{"vorname":"Markus","email":"m@example.org"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, body)
	}

	// The token parks at the user task; find its element-instance scope.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("tasks: %d %s", code, body)
	}
	var tasks []struct {
		ElementInstanceKey uint64 `json:"elementInstanceKey"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		ProcessID          string `json:"processId"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	var eik, pik uint64
	for _, tk := range tasks {
		if tk.ProcessID == "prefill" {
			eik, pik = tk.ElementInstanceKey, tk.ProcessInstanceKey
		}
	}
	if eik == 0 {
		t.Fatal("user task not found")
	}
	if eik == pik {
		t.Fatal("expected the task to have its own element-instance scope, distinct from the process root")
	}

	// Reading the task's own (empty) element-instance scope must still surface the
	// inherited process variables — the exact fix. Before it, this returned {}.
	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/instances/"+strconv.FormatUint(eik, 10)+"/variables", "", "")
	if code != http.StatusOK {
		t.Fatalf("variables: %d %s", code, body)
	}
	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	if vars["vorname"] != "Markus" || vars["email"] != "m@example.org" {
		t.Fatalf("task-scope variables = %v; want inherited vorname=Markus email=m@example.org", vars)
	}
}
