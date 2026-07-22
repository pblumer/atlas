package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// parallelBPMN forks into two pass-through branches and joins before the end, so
// it runs straight to completion — exercising the parallel gateway (fork + join)
// through the full HTTP server.
const parallelBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="forkjoin" name="Fork Join" isExecutable="true">
    <startEvent id="s"/>
    <parallelGateway id="fork"/>
    <task id="a" name="A"/>
    <task id="b" name="B"/>
    <parallelGateway id="join"/>
    <endEvent id="e"/>
    <sequenceFlow id="f0" sourceRef="s" targetRef="fork"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="b"/>
    <sequenceFlow id="f3" sourceRef="a" targetRef="join"/>
    <sequenceFlow id="f4" sourceRef="b" targetRef="join"/>
    <sequenceFlow id="f5" sourceRef="join" targetRef="e"/>
  </process>
</definitions>`

// TestDeployParallelForkJoinRuns deploys a fork/join model, starts an instance,
// and confirms it runs to completion — both branches fork, the join synchronizes
// them, and the single continuation reaches the end.
func TestDeployParallelForkJoinRuns(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", parallelBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}
	var ci struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &ci); err != nil {
		t.Fatalf("decode create: %v (%s)", err, body)
	}
	if ci.Stats.ActiveProcessInstances != 0 {
		t.Fatalf("after run: active=%d, want 0 (fork/join ran to completion)", ci.Stats.ActiveProcessInstances)
	}
}
