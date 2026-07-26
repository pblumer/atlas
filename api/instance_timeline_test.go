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

// scriptVarBPMN writes a variable mid-process (a script task sets greeting="hi"),
// so the per-step variable snapshots evolve across the timeline.
const scriptVarBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="scripted" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="script">
      <extensionElements><zeebe:script expression="= &quot;hi&quot;" resultVariable="greeting"/></extensionElements>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="script"/>
    <sequenceFlow id="f2" sourceRef="script" targetRef="end"/>
  </process>
</definitions>`

type timelineVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Kind  string `json:"kind"`
}

type timelineStep struct {
	At        int64         `json:"at"`
	ElementID string        `json:"elementId"`
	Type      string        `json:"type"`
	Variables []timelineVar `json:"variables"`
}

type instanceTimeline struct {
	InstanceKey   uint64          `json:"instanceKey"`
	ProcessDefKey uint64          `json:"processDefKey"`
	ProcessID     string          `json:"processId"`
	State         string          `json:"state"`
	Steps         []timelineStep  `json:"steps"`
	Frames        []timelineFrame `json:"frames"`
}

type timelineToken struct {
	TokenID   uint64 `json:"tokenId"`
	ElementID string `json:"elementId"`
	State     string `json:"state"`
}

type timelineFrame struct {
	Position uint64          `json:"position"`
	Tokens   []timelineToken `json:"tokens"`
}

const parallelReplayBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="parallel-replay" isExecutable="true">
    <startEvent id="start"/><parallelGateway id="fork"/>
    <scriptTask id="way1"><extensionElements><zeebe:script expression="= 1" resultVariable="one"/></extensionElements></scriptTask>
    <scriptTask id="way2"><extensionElements><zeebe:script expression="= 2" resultVariable="two"/></extensionElements></scriptTask>
    <parallelGateway id="join"/><endEvent id="end"/>
    <sequenceFlow id="to_fork" sourceRef="start" targetRef="fork"/>
    <sequenceFlow id="to_way1" sourceRef="fork" targetRef="way1"/>
    <sequenceFlow id="to_way2" sourceRef="fork" targetRef="way2"/>
    <sequenceFlow id="way1_join" sourceRef="way1" targetRef="join"/>
    <sequenceFlow id="way2_join" sourceRef="way2" targetRef="join"/>
    <sequenceFlow id="to_end" sourceRef="join" targetRef="end"/>
  </process>
</definitions>`

func TestInstanceTimelineParallelTokenFrames(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", parallelReplayBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatal(err)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "", "application/json"); code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", code, body)
	}
	key := onlyInstanceKey(t, ts)
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatal(err)
	}
	foundParallel, foundWaiting, foundEnd := false, false, false
	for _, frame := range tl.Frames {
		ways := map[string]uint64{}
		for _, tok := range frame.Tokens {
			if tok.ElementID == "way1" || tok.ElementID == "way2" {
				ways[tok.ElementID] = tok.TokenID
			}
			if tok.ElementID == "join" && tok.State == "waiting" {
				foundWaiting = true
			}
		}
		if len(ways) == 2 && ways["way1"] != ways["way2"] {
			foundParallel = true
		}
		if len(frame.Tokens) == 1 && frame.Tokens[0].ElementID == "end" {
			foundEnd = true
		}
	}
	if !foundParallel || !foundWaiting || !foundEnd {
		t.Fatalf("frames do not show fork/join semantics: parallel=%v waiting=%v end=%v frames=%+v", foundParallel, foundWaiting, foundEnd, tl.Frames)
	}
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

// TestInstanceTimelineVariableSnapshots runs a process that seeds a start variable
// and writes another mid-way (a script task), then checks each step carries the
// variable values as they stood when the token entered that element (ADR-0048):
// the seeded x is present from the start, and the script's greeting appears only
// from the step after the script task ran, not at the script task itself.
func TestInstanceTimelineVariableSnapshots(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", scriptVarBPMN, "application/xml")
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
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), `{"variables":{"x":1}}`, "application/json")
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

	if len(tl.Steps) != 3 {
		t.Fatalf("steps = %d (%+v), want 3 (start, script, end)", len(tl.Steps), tl.Steps)
	}
	// vars turns a step's variable snapshot into a name→value map.
	vars := func(i int) map[string]string {
		out := map[string]string{}
		for _, v := range tl.Steps[i].Variables {
			out[v.Name] = v.Value
		}
		return out
	}
	if tl.Steps[0].ElementID != "start" || tl.Steps[1].ElementID != "script" || tl.Steps[2].ElementID != "end" {
		t.Fatalf("step order = %s,%s,%s, want start,script,end", tl.Steps[0].ElementID, tl.Steps[1].ElementID, tl.Steps[2].ElementID)
	}
	// The seeded x is present from the very first step.
	if got := vars(0); got["x"] != "1" {
		t.Errorf("start-step vars = %v, want x=1", got)
	}
	// At the script task itself, its output is not written yet.
	if got := vars(1); got["x"] != "1" || len(got) != 1 {
		t.Errorf("script-step vars = %v, want only x=1 (greeting not yet written)", got)
	}
	// At the next step, the script's greeting is visible alongside x.
	if got := vars(2); got["x"] != "1" || got["greeting"] != "hi" {
		t.Errorf("end-step vars = %v, want x=1 and greeting=hi", got)
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
