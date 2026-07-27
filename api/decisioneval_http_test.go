package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestInstanceDecisionsEndpoint deploys the dinner process (Start → business rule
// task "Dish" → End), runs an instance to completion, then reads the new
// /instances/{key}/decisions surface (ADR-0066). It asserts the durable evaluation
// is served back with everything an operator needs to see how the decision was
// made after the fact: the diagram element, the inputs it saw, the outputs it
// produced, and the temis trace.
func TestInstanceDecisionsEndpoint(t *testing.T) {
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

	// Find the (now completed) instance's key from the instances list.
	_, list := x.do(http.MethodGet, "/api/v1/instances", "")
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(list, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(instances) == 0 {
		t.Fatalf("no instances listed: %s", list)
	}
	instanceKey := instances[0].Key

	code, b = x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/decisions", instanceKey), "")
	if code != http.StatusOK {
		t.Fatalf("decisions status=%d body=%s", code, b)
	}
	var decisions []struct {
		At         int64           `json:"at"`
		ElementID  string          `json:"elementId"`
		DecisionID string          `json:"decisionId"`
		Inputs     json.RawMessage `json:"inputs"`
		Outputs    json.RawMessage `json:"outputs"`
		Trace      json.RawMessage `json:"trace"`
	}
	if err := json.Unmarshal(b, &decisions); err != nil {
		t.Fatalf("decode decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1; body=%s", len(decisions), b)
	}
	d := decisions[0]
	if d.DecisionID != "Dish" {
		t.Errorf("decisionId = %q, want Dish", d.DecisionID)
	}
	if d.ElementID != "decide" {
		t.Errorf("elementId = %q, want decide (the business rule task's diagram id)", d.ElementID)
	}
	if string(d.Inputs) != `{"Season":"Winter"}` {
		t.Errorf("inputs = %s, want {\"Season\":\"Winter\"}", d.Inputs)
	}
	if string(d.Outputs) != `{"Dish":"Roastbeef"}` {
		t.Errorf("outputs = %s, want {\"Dish\":\"Roastbeef\"}", d.Outputs)
	}
	if len(d.Trace) == 0 {
		t.Errorf("trace is empty, want the temis explanation; body=%s", b)
	}
}

// TestInstanceDecisionsEmptyForUnknownInstance covers the convenience-read contract:
// an instance key that made no decisions (here, one that never existed) yields an
// empty array and 200, not a 404 — the same shape as the variables endpoint.
func TestInstanceDecisionsEmptyForUnknownInstance(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	code, b := x.do(http.MethodGet, "/api/v1/instances/123456/decisions", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", code, b)
	}
	if string(b) != "[]\n" && string(b) != "[]" {
		t.Fatalf("body=%q, want empty array", b)
	}
}

// TestInstanceDecisionsRejectsBadKey covers the key-parse guard.
func TestInstanceDecisionsRejectsBadKey(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	if code, _ := x.do(http.MethodGet, "/api/v1/instances/not-a-number/decisions", ""); code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}
