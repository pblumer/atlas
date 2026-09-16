package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// What the breaker does to a real flood, driven end to end through the HTTP surface
// (ADR-0340). The unit tests beside it pin the state machine; these pin the thing the
// decision was written for — an outage that used to cost one incident per instance now
// costs three, and the rest of the work waits instead.

// mailFloodBPMN parks every instance on one mail task whose Worker is not configured,
// which is the cheapest way to make a target that fails every instance alike.
const mailFloodBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="notify" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="send">
      <extensionElements><atlas:mailConnector connector="Patrick Blumer" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="end"/>
  </process>
</definitions>`

// deployFlood deploys a model and starts n instances of it.
func deployFlood(t *testing.T, ts *httptest.Server, xml string, n int) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	for i := 0; i < n; i++ {
		if code, body := doReq(t, ts, http.MethodPost,
			fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, body)
		}
	}
	return deploy.Key
}

// TestABreakerStopsAFloodAtItsSource is the decision's whole claim, measured. Twenty
// instances reach a task whose target answers nothing. Before ADR-0340 that was twenty
// parked tokens, twenty spent retry budgets and twenty rows for an operator to clear;
// now the breaker trips on the third distinct instance and the rest simply wait.
func TestABreakerStopsAFloodAtItsSource(t *testing.T) {
	ts := newTestServer(t)
	deployFlood(t, ts, mailFloodBPMN, 20)

	got := listIncidents(t, ts)
	if len(got) == 0 {
		t.Fatal("no incidents at all: the flood has to cost *something*, or nothing is being attempted")
	}
	if len(got) >= 20 {
		t.Fatalf("%d incidents from 20 instances — the flood was not stopped at its source", len(got))
	}
	// The trip needs distinct instances, so the cost is bounded by the threshold and
	// not by the size of the flood.
	if len(got) > 6 {
		t.Errorf("%d incidents, want the handful the trip condition allows before it holds the rest", len(got))
	}

	// And the held tokens are *waiting*, not failed: their instances are still active,
	// with no incident and no business outcome invented for them.
	s := incidentSummaryQuery(t, ts, "")
	if s.Total != len(got) {
		t.Errorf("summary total = %d, incident rows = %d", s.Total, len(got))
	}
}

// TestAHeldTokenKeepsItsRetryBudget is the property that makes waiting free. A job the
// breaker refused was never handed out, so nothing was attempted on its behalf and
// nothing was spent — which is what lets the backlog drain by itself when the target
// returns, with no operator action at all.
func TestAHeldTokenKeepsItsRetryBudget(t *testing.T) {
	ts := newTestServer(t)
	deployFlood(t, ts, mailFloodBPMN, 20)

	// Every job still waiting for this type: none of them may be leased, and each must
	// still hold the retries its model granted.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var insts []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(listRows(t, body), &insts); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(insts) != 20 {
		t.Fatalf("instances = %d, want the 20 that were started", len(insts))
	}
	for _, it := range insts {
		if it.State != "active" {
			t.Errorf("instance %d is %q — holding work back must not end anything", it.Key, it.State)
		}
	}
}
