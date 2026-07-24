package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// autoDoneBPMN runs straight from start to end with no wait, so creating an
// instance completes it in one drain — the finished-instance timeline path.
const autoDoneBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="greet" isExecutable="true">
    <startEvent id="start"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </process>
</definitions>`

type timelineStep struct {
	At        int64  `json:"at"`
	ElementID string `json:"elementId"`
	Type      string `json:"type"`
}

type instanceTimeline struct {
	InstanceKey   uint64         `json:"instanceKey"`
	ProcessDefKey uint64         `json:"processDefKey"`
	ProcessID     string         `json:"processId"`
	State         string         `json:"state"`
	Steps         []timelineStep `json:"steps"`
}

// onlyInstanceKey lists instances and returns the key of the single one the test
// created.
func onlyInstanceKey(t *testing.T, ts *httptest.Server) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var insts []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 {
		t.Fatalf("instances = %d, want exactly 1", len(insts))
	}
	return insts[0].Key
}

// TestInstanceTimelineActive starts a process that parks at its service task and
// checks the step timeline reports the walked elements in order (start → task),
// their diagram ids and types, and the instance's active state (ADR-0046).
func TestInstanceTimelineActive(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "", "application/json")
	if code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}

	instKey := onlyInstanceKey(t, ts)

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", instKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}

	if tl.InstanceKey != instKey || tl.ProcessDefKey != dep.Key || tl.ProcessID != "order" {
		t.Errorf("timeline meta = %+v, want instance %d def %d id order", tl, instKey, dep.Key)
	}
	if tl.State != "active" {
		t.Errorf("state = %q, want active", tl.State)
	}
	// Parked at the service task, the token has walked start → task.
	if len(tl.Steps) != 2 {
		t.Fatalf("steps = %d (%+v), want 2 (start, task)", len(tl.Steps), tl.Steps)
	}
	if tl.Steps[0].ElementID != "start" || tl.Steps[1].ElementID != "task" {
		t.Errorf("step order = [%s, %s], want [start, task]", tl.Steps[0].ElementID, tl.Steps[1].ElementID)
	}
	for i, st := range tl.Steps {
		if st.At <= 0 || st.Type == "" {
			t.Errorf("step %d = %+v, want a timestamp and a type", i, st)
		}
	}
	if tl.Steps[1].At < tl.Steps[0].At {
		t.Errorf("timeline out of order: %d before %d", tl.Steps[1].At, tl.Steps[0].At)
	}
}

// TestInstanceTimelineCompleted runs a process to completion and checks the
// timeline resolves the finished instance from the history index and reports its
// full trail (start → end) with the completed state.
func TestInstanceTimelineCompleted(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", autoDoneBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "", "application/json")
	if code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}

	instKey := onlyInstanceKey(t, ts)

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", instKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if tl.State != "completed" {
		t.Errorf("state = %q, want completed", tl.State)
	}
	if len(tl.Steps) != 2 || tl.Steps[0].ElementID != "start" || tl.Steps[1].ElementID != "end" {
		t.Errorf("steps = %+v, want start → end", tl.Steps)
	}
}

// TestInstanceTimelineDefinitionDeleted runs an instance to completion, deletes
// its (now instance-free) definition, and checks the timeline reports a 404: the
// instance's element indices can no longer be mapped to a diagram once the
// definition is gone.
func TestInstanceTimelineDefinitionDeleted(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", autoDoneBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, body := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "", "application/json"); code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}
	instKey := onlyInstanceKey(t, ts)

	// The instance has finished, so the definition (with no active instances) deletes.
	if code, body := doReq(t, ts, http.MethodDelete, fmt.Sprintf("/api/v1/processes/%d", dep.Key), "", ""); code != http.StatusNoContent {
		t.Fatalf("delete process: status=%d body=%s", code, body)
	}
	if code, _ := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", instKey), "", ""); code != http.StatusNotFound {
		t.Errorf("timeline after definition delete = %d, want 404", code)
	}
}

// TestInstanceTimelineErrors covers the request-error branches: a non-numeric key
// is a 400, and a well-formed but unknown key is a 404.
func TestInstanceTimelineErrors(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/not-a-number/timeline", "", ""); code != http.StatusBadRequest {
		t.Errorf("bad key status = %d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/999999/timeline", "", ""); code != http.StatusNotFound {
		t.Errorf("unknown key status = %d, want 404", code)
	}
}
