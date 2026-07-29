package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// completingBPMN runs straight from start to end, so an instance completes at once.
const completingBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="quick" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestRuntimeAggregateAfterCompletion covers the ADR-0080 runtime read once a
// definition's instances have finished: the maintained counters report no live
// instances or tokens, the cumulative-visit heatmap persists, and filtering by a
// non-live instance key reports nothing — all without scanning any instance.
func TestRuntimeAggregateAfterCompletion(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", completingBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	// Start an instance; it flows straight to the end event and completes.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, body)
	}

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime", "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime: %d %s", code, body)
	}
	var rt struct {
		Instances int `json:"instances"`
		Tokens    int `json:"tokens"`
		Elements  []struct {
			Visits int `json:"visits"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	if rt.Instances != 0 || rt.Tokens != 0 {
		t.Fatalf("after completion runtime = %+v, want 0 instances and 0 tokens", rt)
	}
	total := 0
	for _, e := range rt.Elements {
		total += e.Visits
	}
	if total == 0 {
		t.Fatal("expected a retained visit heatmap after completion")
	}

	// Filtering by an instance key that is not live reports no live state (the
	// single-instance path's not-found branch).
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime?instance=999999", "", "")
	if code != http.StatusOK {
		t.Fatalf("filter runtime: %d %s", code, body)
	}
	var rf struct {
		Instances int `json:"instances"`
		Tokens    int `json:"tokens"`
	}
	if err := json.Unmarshal(body, &rf); err != nil {
		t.Fatalf("decode filter runtime: %v (%s)", err, body)
	}
	if rf.Instances != 0 || rf.Tokens != 0 {
		t.Fatalf("filter by a non-live instance = %+v, want 0", rf)
	}
}

// TestRuntimeFilterWrongDefinition covers the single-instance path's guard that an
// instance belonging to another definition is not reported under this one: a second
// definition's instance filtered under the first yields no live state.
func TestRuntimeFilterWrongDefinition(t *testing.T) {
	ts := newTestServer(t)
	// Definition 1 completes at once; definition 2 parks at a task (stays live).
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments", completingBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy 1: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy 2: %d %s", code, b)
	}
	if code, ib := doReq(t, ts, http.MethodPost, "/api/v1/processes/2/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("instance of def 2: %d %s", code, ib)
	}
	// The only live instance is definition 2's (definition 1's completed at once).
	code, lb := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, lb)
	}
	var insts []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(lb, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, lb)
	}
	var liveKey uint64
	for _, in := range insts {
		if in.State == "active" {
			liveKey = in.Key
		}
	}
	if liveKey == 0 {
		t.Fatalf("no active instance found (%s)", lb)
	}

	// That live instance belongs to definition 2, so filtering it under definition 1
	// reports nothing.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime?instance="+strconv.FormatUint(liveKey, 10), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime: %d %s", code, body)
	}
	var rf struct {
		Instances int `json:"instances"`
	}
	if err := json.Unmarshal(body, &rf); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if rf.Instances != 0 {
		t.Fatalf("instance of another definition filtered here: instances=%d, want 0", rf.Instances)
	}
}
