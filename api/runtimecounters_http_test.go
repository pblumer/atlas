package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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

// TestListAndSearchInstancesWithVariables exercises the instance-listing and search
// endpoints with an instance carrying variables — the enrichment (definition
// id/version/tag and per-scope variables), the search match, and the variables read.
func TestListAndSearchInstancesWithVariables(t *testing.T) {
	ts := newTestServer(t)
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	// An instance that parks at the task, carrying start variables.
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"orderId":"o-77","amount":42}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, b)
	}

	// List: the active instance appears, enriched with its process id and variables.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, body)
	}
	s := string(body)
	if !strings.Contains(s, `"orderId"`) || !strings.Contains(s, "o-77") {
		t.Fatalf("list instances missing variables: %s", s)
	}

	// Search by variable content: name=value (name exact, value substring).
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=orderId=o-77", "", "")
	if code != http.StatusOK {
		t.Fatalf("search instances: %d %s", code, body)
	}
	if !strings.Contains(string(body), "o-77") {
		t.Fatalf("search did not match the instance: %s", string(body))
	}

	// Free-text search over variable names and values.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=o-77", "", "")
	if code != http.StatusOK {
		t.Fatalf("free-text search: %d %s", code, body)
	}
	if !strings.Contains(string(body), "o-77") {
		t.Fatalf("free-text search did not match: %s", string(body))
	}

	// Read the instance's variables directly.
	var insts []struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(body, &insts)
	if len(insts) > 0 {
		code, vb := doReq(t, ts, http.MethodGet, "/api/v1/instances/"+strconv.FormatUint(insts[0].Key, 10)+"/variables", "", "")
		if code != http.StatusOK {
			t.Fatalf("instance variables: %d %s", code, vb)
		}
		if !strings.Contains(string(vb), "o-77") {
			t.Fatalf("instance variables missing orderId: %s", string(vb))
		}
	}
}

// TestSearchInstancesEdgeCases covers the search endpoint's reachable branches: an
// empty/invalid query returns nothing, and a query that matches no variable drops
// every instance — exercising the no-match skip paths deterministically.
func TestSearchInstancesEdgeCases(t *testing.T) {
	ts := newTestServer(t)
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"orderId":"o-99"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, b)
	}

	// An empty query returns an empty result (the parse-miss branch).
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=", "", ""); code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("empty query: %d %s, want []", code, body)
	}

	// A query that matches nothing drops the instance (the len(hits)==0 skip).
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=orderId=nope", "", ""); code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("non-matching query: %d %s, want []", code, body)
	}

	// Reading variables of a non-existent instance yields an empty object, not an error.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/999999/variables", "", ""); code != http.StatusOK {
		t.Fatalf("variables of a missing instance: code=%d, want 200", code)
	}
}

// TestSearchAndListFindCompletedInstance covers the completed-instance branches of
// list and search: an instance that completed with a variable is still found by a
// variable search and appears in the finished list.
func TestSearchAndListFindCompletedInstance(t *testing.T) {
	ts := newTestServer(t)
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments", completingBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"orderId":"o-done"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, b)
	}

	// The finished list carries the completed instance and its variable.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	if !strings.Contains(string(body), `"state":"completed"`) || !strings.Contains(string(body), "o-done") {
		t.Fatalf("finished list missing the completed instance/variable: %s", body)
	}

	// A variable search finds the completed instance.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=orderId=o-done", "", "")
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, body)
	}
	if !strings.Contains(string(body), "o-done") || !strings.Contains(string(body), `"state":"completed"`) {
		t.Fatalf("search did not find the completed instance: %s", body)
	}
}

// TestSearchCompletedSortAndSkip covers the finished-instance branches of search:
// two matching completed instances make the finished-sort comparator run, and a
// third completed instance that does not match is dropped (the len(hits)==0 skip).
func TestSearchCompletedSortAndSkip(t *testing.T) {
	ts := newTestServer(t)
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments", completingBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	mk := func(tag string) {
		if code, b := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances",
			`{"variables":{"tag":"`+tag+`"}}`, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance: %d %s", code, b)
		}
	}
	mk("keep")
	mk("keep")
	mk("drop")

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=tag=keep", "", "")
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, body)
	}
	var matches []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &matches); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(matches) != 2 {
		t.Fatalf("search matched %d, want the 2 keep instances (%s)", len(matches), body)
	}
}
