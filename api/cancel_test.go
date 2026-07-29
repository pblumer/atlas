package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// timerWaitBPMN parks an instance forever (a one-hour timer), the shape of a
// stuck instance an operator would want to cancel.
const timerWaitBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="waiter" name="Waiter" isExecutable="true">
    <startEvent id="s"/>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestCancelInstance parks an instance on a long timer, cancels it over HTTP, and
// checks it leaves the active set and appears as terminated in the instance list.
func TestCancelInstance(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", timerWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, b)
	}

	// Find the active instance key.
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var insts []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(insts) != 1 || insts[0].State != "active" {
		t.Fatalf("instances = %+v, want one active", insts)
	}
	instKey := insts[0].Key

	// Cancel it.
	code, body = doReq(t, ts, http.MethodDelete, fmt.Sprintf("/api/v1/instances/%d", instKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", code, body)
	}
	var cr struct {
		State string `json:"state"`
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatalf("decode cancel: %v (%s)", err, body)
	}
	if cr.State != "terminated" || cr.Stats.ActiveProcessInstances != 0 {
		t.Fatalf("cancel result = %+v, want terminated + 0 active", cr)
	}

	// It now lists as terminated, and cancelling again is a 404.
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if !strings.Contains(string(body), `"state":"terminated"`) {
		t.Fatalf("instance not shown terminated: %s", body)
	}
	if code, _ := doReq(t, ts, http.MethodDelete, fmt.Sprintf("/api/v1/instances/%d", instKey), "", ""); code != http.StatusNotFound {
		t.Fatalf("re-cancel status=%d, want 404", code)
	}
}

// TestCancelInstanceBadKey rejects a non-numeric instance key.
func TestCancelInstanceBadKey(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/instances/not-a-number", "", ""); code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}

// TestCancelInstancesOfProcess drains a definition's runaway instances in bounded
// batches: it starts several parked instances, then repeatedly POSTs
// .../cancel-instances until the response reports nothing remaining, checking the
// per-call cap is honored and the active set empties. This is the bulk-drain path for
// the reported /employees flood, where cancelling instances one by one is infeasible.
func TestCancelInstancesOfProcess(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", timerWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	const n = 5
	for i := 0; i < n; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}

	// Error paths: bad definition key → 400, unknown definition → 404, bad limit → 400.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/processes/not-a-number/cancel-instances", "", ""); code != http.StatusBadRequest {
		t.Fatalf("cancel-instances bad key: status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/processes/999999/cancel-instances", "", ""); code != http.StatusNotFound {
		t.Fatalf("cancel-instances unknown def: status=%d, want 404", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/cancel-instances?limit=0", dep.Key), "", ""); code != http.StatusBadRequest {
		t.Fatalf("cancel-instances bad limit: status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/cancel-instances?limit=nope", dep.Key), "", ""); code != http.StatusBadRequest {
		t.Fatalf("cancel-instances non-numeric limit: status=%d, want 400", code)
	}

	// Drain in batches of two: 2, 2, 1 — the cap is honored and the last batch clears.
	total, rounds := 0, 0
	for {
		code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/cancel-instances?limit=2", dep.Key), "", "")
		if code != http.StatusOK {
			t.Fatalf("cancel-instances round %d: status=%d body=%s", rounds, code, body)
		}
		var cr struct {
			Canceled  int  `json:"canceled"`
			Remaining bool `json:"remaining"`
			Stats     struct {
				ActiveProcessInstances int `json:"activeProcessInstances"`
			} `json:"stats"`
		}
		if err := json.Unmarshal(body, &cr); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if cr.Canceled > 2 {
			t.Fatalf("round %d canceled=%d, want <= 2 (per-call cap)", rounds, cr.Canceled)
		}
		total += cr.Canceled
		if !cr.Remaining {
			if cr.Stats.ActiveProcessInstances != 0 {
				t.Fatalf("drain finished with %d still active", cr.Stats.ActiveProcessInstances)
			}
			break
		}
		if rounds++; rounds > n+2 {
			t.Fatalf("drain did not converge; total=%d", total)
		}
	}
	if total != n {
		t.Fatalf("total canceled=%d, want %d", total, n)
	}

	// A drain on a definition with no live instances is a clean no-op — and an
	// over-ceiling limit is clamped rather than rejected.
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/cancel-instances?limit=999999", dep.Key), "", ""); code != http.StatusOK || !strings.Contains(string(body), `"canceled":0`) {
		t.Fatalf("empty drain (clamped limit): status=%d body=%s", code, body)
	}
}
