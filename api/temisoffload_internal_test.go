package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// The end-to-end proof of ADR-0233's last slice: a central decision leased by a
// worker, evaluated out of process, and reported back — leaving the same durable
// evaluation record an in-engine decision leaves (ADR-0066).
//
// This is the test the slice exists for. Everything else about temis was a copy of
// the five kinds before it; the completion carrying an evaluation is new, and a
// decision that ran on a worker but left no record would be a hole in the audit
// trail that nothing else would notice.

// centralDecisionOffloadBPMN parks at a central business rule task: the decision is
// evaluated by a named temis service, so with the kind offloaded the job waits for a
// worker to lease it.
const centralDecisionOffloadBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-zins">
  <bpmn:process id="zins" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:businessRuleTask id="decide">
      <bpmn:extensionElements>
        <zeebe:calledDecision decisionId="Hypothekarzins" resultVariable="zins"/>
        <atlas:temisConnector connector="rules"/>
      </bpmn:extensionElements>
    </bpmn:businessRuleTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <bpmn:sequenceFlow id="f2" sourceRef="decide" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// A worker leases the decision, is handed the input context the engine built, and
// completes with both halves: the outputs as the task's result variable, and the
// evaluation as the record. Both must land.
func TestACentralDecisionEvaluatedOnAWorkerIsStillRetained(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"temis"}))

	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
		centralDecisionOffloadBPMN, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"laufzeit":10}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}

	// Lease it as a worker would. The payload is what the slice put on the wire: the
	// decision id and the inputs the engine resolved, so the worker asks the service
	// the same question the engine would have.
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"io.atlas.temis.decision","worker":"w1","leaseMs":60000}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease: status=%d body=%s", code, raw)
	}
	var leased struct {
		Jobs []struct {
			Key        uint64 `json:"jobKey"`
			LeaseToken uint64 `json:"leaseToken"`
			Connector  *struct {
				Kind   string         `json:"kind"`
				Fields map[string]any `json:"fields"`
			} `json:"connector"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(leased.Jobs) != 1 {
		t.Fatalf("leased %d jobs, want 1; body=%s", len(leased.Jobs), raw)
	}
	j := leased.Jobs[0]
	if j.Connector == nil || j.Connector.Kind != "temis" {
		t.Fatalf("payload = %#v, want a temis payload", j.Connector)
	}
	if got := j.Connector.Fields["decisionId"]; got != "Hypothekarzins" {
		t.Errorf("decisionId = %#v, want the authored decision", got)
	}
	if got := j.Connector.Fields["connector"]; got != "rules" {
		t.Errorf("connector = %#v, want the named decision service", got)
	}

	// Complete it the way the widened contract says: variables plus the evaluation.
	complete := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
		"variables":{"zins":1.13},
		"decision":{"decisionId":"Hypothekarzins","inputs":{"laufzeit":10},
		            "outputs":{"zins":1.13},"trace":"{\"rules\":[3]}"}}`, j.LeaseToken)
	code, raw = serveInternal(t, srv,
		http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", j.Key), complete, "application/json")
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete: status=%d body=%s", code, raw)
	}

	// The record: what an operator opens to see how the decision was made.
	var found *model.DecisionEvaluationValue
	srv.do(func() {
		_ = srv.store.EachDecisionEvaluation(func(_ uint64, _ int64, v *model.DecisionEvaluationValue) error {
			found = v
			return nil
		})
	})
	if found == nil {
		t.Fatal("a decision evaluated on a worker left no evaluation record; the audit trail has a hole exactly where the work moved")
	}
	if found.DecisionId != "Hypothekarzins" {
		t.Errorf("decisionId = %q, want the evaluated decision", found.DecisionId)
	}
	if found.TraceJSON != `{"rules":[3]}` {
		t.Errorf("trace = %q, want the service's own account, kept verbatim", found.TraceJSON)
	}
	// The identity fields are the engine's, not the report's: they come from the job
	// the worker held a lease on, which is what stops a report attaching itself to a
	// task it did not run.
	if found.ElementInstanceKey == 0 || found.ProcessInstanceKey == 0 {
		t.Errorf("record = %+v, want the element and instance stamped from the leased job", found)
	}
}

// An operator completing a job by hand may not write a decision record, and this is
// the assertion the code's comment makes but nothing proved. Completing by hand is an
// intervention: it is recorded with who did it and why (ADR-0159). A decision
// evaluation is the opposite — an account of work a decision service actually did. If
// the manual path could write one, the audit trail would contain a decision nobody
// made, attributed to no one, indistinguishable from a real evaluation.
func TestAManualCompletionCannotWriteADecisionRecord(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"temis"}))

	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
		centralDecisionOffloadBPMN, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"laufzeit":10}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}

	var jobKey uint64
	srv.do(func() {
		_ = srv.store.ActivatableJobs(compiler.TemisDecisionJobTypeIndex, func(key uint64) error {
			jobKey = key
			return nil
		})
	})
	if jobKey == 0 {
		t.Fatal("the instance did not park on a job")
	}

	// No worker, no lease token: an operator forcing the step, and sending a decision
	// along with it.
	body := `{"reason":"the service was down and the answer is obvious",
		"variables":{"zins":1.13},
		"decision":{"decisionId":"Hypothekarzins","outputs":{"zins":1.13},"trace":"{\"rules\":[9]}"}}`
	code, raw = serveInternal(t, srv,
		http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey), body, "application/json")
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("manual complete: status=%d body=%s", code, raw)
	}

	var records int
	srv.do(func() {
		_ = srv.store.EachDecisionEvaluation(func(uint64, int64, *model.DecisionEvaluationValue) error {
			records++
			return nil
		})
	})
	if records != 0 {
		t.Errorf("decision evaluations = %d, want none: an intervention is not an evaluation, and the audit trail must not carry a decision nobody made", records)
	}
}
