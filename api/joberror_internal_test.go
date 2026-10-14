package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/model"
)

// errorBoundaryBPMN is jobTypeBPMN with an error boundary on the task catching code and
// leading to its own end, so a thrown error has somewhere to go (ADR-0089).
func errorBoundaryBPMN(procID, jobType, code string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:error id="err" errorCode="%s"/>
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><zeebe:taskDefinition type="%s" retries="3"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:boundaryEvent id="b" attachedToRef="t"><bpmn:errorEventDefinition errorRef="err"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:endEvent id="handled"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="handled"/>
  </bpmn:process>
</bpmn:definitions>`, code, procID, jobType)
}

// throwError posts a body to the job's error route.
func throwError(t *testing.T, srv *Server, jobKey uint64, body string) (int, string) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/error", jobKey), body, "application/json")
	return code, string(raw)
}

// openIncidents reads the incidents straight from state.
func openIncidents(t *testing.T, srv *Server) []model.IncidentValue {
	t.Helper()
	var out []model.IncidentValue
	srv.do(func() {
		_ = srv.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
			out = append(out, *v)
			return nil
		})
	})
	return out
}

// A worker holding a job throws the BPMN error its work ended in, and the boundary on
// the task catches it: the instance takes the error path, the worker's row says what it
// sent, and nothing is filed as an operator intervention — it is the protocol.
func TestAWorkerThrowsAnErrorTheTasksBoundaryCatches(t *testing.T) {
	srv, _ := jobPullSrvClockedWith(t, errorBoundaryBPMN("orders", "charge-card", "CARD_DECLINED"), `{}`)
	_, got := pull(t, srv, `{"type":"charge-card","worker":"w1"}`)
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	job := got.Jobs[0]
	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"errorCode":"CARD_DECLINED","errorMessage":"issuer said no"}`, job.LeaseToken)
	if code, b := throwError(t, srv, job.JobKey, body); code != http.StatusOK {
		t.Fatalf("throw: status=%d body=%s", code, b)
	}

	var active, actions int
	var outcome, message string
	srv.do(func() {
		_ = srv.store.ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error { active++; return nil })
		_ = srv.store.OperatorActionHistory(job.ProcessInstanceKey, func(int64, uint64, *model.OperatorActionValue) error {
			actions++
			return nil
		})
		for _, r := range srv.workers.jobsOf("w1") {
			if r.JobKey == job.JobKey {
				outcome, message = r.Outcome, r.Error
			}
		}
	})
	if active != 0 {
		t.Errorf("active instances = %d, want 0 (the boundary caught the error and the instance ended)", active)
	}
	if n := len(openIncidents(t, srv)); n != 0 {
		t.Errorf("incidents = %d, want 0", n)
	}
	if actions != 0 {
		t.Errorf("%d operator action(s) for a worker's throw, want 0", actions)
	}
	if outcome != jobRunErrorThrown || message != "CARD_DECLINED: issuer said no" {
		t.Errorf("worker row = %q %q, want %q with the code and message", outcome, message, jobRunErrorThrown)
	}
}

// With nothing to catch it the job stays, under a job incident that names the code and
// the worker's message, and resolving it gives the job back to the workers.
func TestAnUncaughtWorkerErrorIsAJobIncidentResolvingRetries(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, got := pull(t, srv, `{"type":"send-email","worker":"w1"}`)
	job := got.Jobs[0]
	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"errorCode":"NO_SUCH_RECIPIENT","errorMessage":"mailbox unknown"}`, job.LeaseToken)
	if code, b := throwError(t, srv, job.JobKey, body); code != http.StatusOK {
		t.Fatalf("throw: status=%d body=%s", code, b)
	}
	incs := openIncidents(t, srv)
	if len(incs) != 1 {
		t.Fatalf("incidents = %d, want 1", len(incs))
	}
	inc := incs[0]
	if inc.JobKey != job.JobKey || inc.Message != "unhandled error 'NO_SUCH_RECIPIENT': mailbox unknown" {
		t.Errorf("incident = %+v, want a job incident naming the code and message", inc)
	}
	if typ := incidentType(&inc); typ != "job" {
		t.Errorf("incident type = %q, want job", typ)
	}
	if code, b := serveInternal(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/incidents/%d/resolve", inc.ElementInstanceKey), `{"retries":2}`, "application/json"); code != http.StatusOK {
		t.Fatalf("resolve: status=%d body=%s", code, b)
	}
	if _, again := pull(t, srv, `{"type":"send-email","worker":"w2"}`); len(again.Jobs) != 1 || again.Jobs[0].JobKey != job.JobKey {
		t.Errorf("after resolving, pulled %d jobs, want the same job back", len(again.Jobs))
	}
}

// Throwing by hand is an intervention, so it must say why, and it is recorded with who
// threw which code (ADR-0159). Without a reason it is refused before anything happens.
func TestAnOperatorThrowsByHandOnlyWithAReason(t *testing.T) {
	srv, _ := jobPullSrvClockedWith(t, errorBoundaryBPMN("orders", "charge-card", "WITHDRAWN"), `{}`)
	jobKey, piKey := parkedServiceJob(t, srv, 1)
	if code, _ := throwError(t, srv, jobKey, `{"errorCode":"WITHDRAWN"}`); code != http.StatusBadRequest {
		t.Fatalf("throw by hand without a reason: status=%d, want 400", code)
	}
	if n := len(openIncidents(t, srv)); n != 0 {
		t.Fatalf("a refused throw left %d incidents", n)
	}
	if code, b := throwError(t, srv, jobKey, `{"errorCode":"WITHDRAWN","reason":"the customer withdrew by phone"}`); code != http.StatusOK {
		t.Fatalf("throw by hand: status=%d body=%s", code, b)
	}
	var got []model.OperatorActionValue
	srv.do(func() {
		_ = srv.store.OperatorActionHistory(piKey, func(_ int64, _ uint64, v *model.OperatorActionValue) error {
			got = append(got, *v)
			return nil
		})
	})
	if len(got) != 1 || got[0].Kind != model.OperatorActionThrowJobError ||
		got[0].ErrorCode != "WITHDRAWN" || got[0].Reason != "the customer withdrew by phone" {
		t.Errorf("operator actions = %+v, want the throw of WITHDRAWN with its reason", got)
	}
}

// The refusals: a throw names a code, a worker presents its lease, a stale worker cannot
// throw on work another worker now holds, and an unknown job is not found.
func TestAThrowIsRefusedWhenItCannotBeTrusted(t *testing.T) {
	srv, clk := jobPullSrvClocked(t, "send-email", `{}`)
	_, first := pull(t, srv, `{"type":"send-email","worker":"mailer","leaseMs":60000}`)
	stale := first.Jobs[0]

	if code, _ := throwError(t, srv, stale.JobKey, fmt.Sprintf(`{"worker":"mailer","leaseToken":%d}`, stale.LeaseToken)); code != http.StatusBadRequest {
		t.Errorf("throw without a code: status=%d, want 400", code)
	}
	if code, _ := throwError(t, srv, stale.JobKey, `{"worker":"mailer","errorCode":"X"}`); code != http.StatusBadRequest {
		t.Errorf("throw as a worker without a lease token: status=%d, want 400", code)
	}
	if code, _ := throwError(t, srv, 999999, `{"errorCode":"X","reason":"r"}`); code != http.StatusNotFound {
		t.Errorf("throw on an unknown job: status=%d, want 404", code)
	}
	if code, _ := throwError(t, srv, stale.JobKey, `{"errorCode":`); code != http.StatusBadRequest {
		t.Errorf("throw with a truncated body: status=%d, want 400", code)
	}

	expireLease(t, srv, clk)
	if _, second := pull(t, srv, `{"type":"send-email","worker":"mailer","leaseMs":60000}`); len(second.Jobs) != 1 {
		t.Fatal("job not re-offered")
	}
	body := fmt.Sprintf(`{"worker":"mailer","leaseToken":%d,"errorCode":"X"}`, stale.LeaseToken)
	if code, _ := throwError(t, srv, stale.JobKey, body); code != http.StatusConflict {
		t.Errorf("stale throw: status=%d, want 409", code)
	}
	if n := len(openIncidents(t, srv)); n != 0 {
		t.Errorf("a refused throw left %d incidents", n)
	}
}

// The instance timeline marks the step an operator threw from as forced by hand, with
// the code, so the console can say which intervention it was rather than calling it a
// completion.
func TestAThrowByHandIsMarkedOnItsStep(t *testing.T) {
	srv, _ := jobPullSrvClockedWith(t, errorBoundaryBPMN("orders", "charge-card", "WITHDRAWN"), `{}`)
	jobKey, piKey := parkedServiceJob(t, srv, 1)
	if code, b := throwError(t, srv, jobKey, `{"errorCode":"WITHDRAWN","reason":"the customer withdrew by phone"}`); code != http.StatusOK {
		t.Fatalf("throw by hand: status=%d body=%s", code, b)
	}
	code, raw := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", piKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline: status=%d body=%s", code, raw)
	}
	var tl struct {
		Steps []struct {
			ElementID string `json:"elementId"`
			Manual    *struct {
				Action    string `json:"action"`
				Reason    string `json:"reason"`
				ErrorCode string `json:"errorCode"`
			} `json:"manual"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	marked := 0
	for _, s := range tl.Steps {
		if s.Manual == nil {
			continue
		}
		marked++
		if s.ElementID != "t" || s.Manual.Action != "throwJobError" || s.Manual.ErrorCode != "WITHDRAWN" ||
			s.Manual.Reason != "the customer withdrew by phone" {
			t.Errorf("step %s manual = %+v, want the throw of WITHDRAWN on t", s.ElementID, *s.Manual)
		}
	}
	if marked != 1 {
		t.Errorf("%d steps marked manual, want 1", marked)
	}
}
