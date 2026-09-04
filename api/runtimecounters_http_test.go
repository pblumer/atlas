package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestProcessRuntimeReportsFinishedCount: the runtime response carries the
// definition's finished-instance count alongside its live one, both from the O(1)
// counters (ADR-0083). The live view needs it to say how many instances the page
// it is showing was drawn from — a panel that reports the length of its own page
// as the total is how "50 instances" gets read as the whole truth.
func TestProcessRuntimeReportsFinishedCount(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	runtime := func() (active, finished int) {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime", dep.Key), "", "")
		if code != http.StatusOK {
			t.Fatalf("runtime: status=%d body=%s", code, body)
		}
		var rt struct {
			Instances int `json:"instances"`
			Finished  int `json:"finished"`
		}
		if err := json.Unmarshal(body, &rt); err != nil {
			t.Fatalf("decode runtime: %v (%s)", err, body)
		}
		return rt.Instances, rt.Finished
	}

	if a, f := runtime(); a != 0 || f != 0 {
		t.Fatalf("fresh definition = %d active / %d finished, want 0/0", a, f)
	}
	for i := 0; i < 2; i++ {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, b)
		}
	}
	if a, f := runtime(); a != 2 || f != 0 {
		t.Fatalf("after two starts = %d active / %d finished, want 2/0", a, f)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil || len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %v (%s)", err, body)
	}
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", tasks[0].Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("complete task: status=%d body=%s", code, b)
	}
	if a, f := runtime(); a != 1 || f != 1 {
		t.Errorf("after one completion = %d active / %d finished, want 1/1", a, f)
	}

	// Isolating one instance on the diagram must not change what the total means:
	// the live view labels the same list from it either way.
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime?instance=%d", dep.Key, tasks[1].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime (filtered): status=%d body=%s", code, body)
	}
	var filtered struct {
		Finished int `json:"finished"`
	}
	if err := json.Unmarshal(body, &filtered); err != nil {
		t.Fatalf("decode filtered runtime: %v (%s)", err, body)
	}
	if filtered.Finished != 1 {
		t.Errorf("filtered runtime finished = %d, want 1", filtered.Finished)
	}
}

// eventGatewayBPMN races a message catch against a timer catch behind an event-based
// gateway (ADR-0110): both branches are armed at once, the first event wins, the other
// is cancelled. It is the model the "passed through" count reads wrong without a
// termination counter of its own.
const eventGatewayBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="Msg_go" name="go"/>
  <process id="race" isExecutable="true">
    <startEvent id="start"/>
    <eventBasedGateway id="gw"/>
    <intermediateCatchEvent id="reply"><messageEventDefinition messageRef="Msg_go"/></intermediateCatchEvent>
    <intermediateCatchEvent id="timeout"><timerEventDefinition><timeDuration>PT30S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="end_reply"/>
    <endEvent id="end_timeout"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="gw"/>
    <sequenceFlow id="f2" sourceRef="gw" targetRef="reply"/>
    <sequenceFlow id="f3" sourceRef="gw" targetRef="timeout"/>
    <sequenceFlow id="f4" sourceRef="reply" targetRef="end_reply"/>
    <sequenceFlow id="f5" sourceRef="timeout" targetRef="end_timeout"/>
  </process>
</definitions>`

// runtimeByElement reads the runtime overlay (optionally isolated to one instance) and
// indexes its elements by BPMN id, which is how every assertion below reads it.
func runtimeByElement(t *testing.T, ts *httptest.Server, query string) map[string]struct{ Tokens, Visits, Terminated int } {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes/1/runtime"+query, "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime%s: %d %s", query, code, body)
	}
	var rt struct {
		Elements []struct {
			ElementID  string `json:"elementId"`
			Tokens     int    `json:"tokens"`
			Visits     int    `json:"visits"`
			Terminated int    `json:"terminated"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	out := map[string]struct{ Tokens, Visits, Terminated int }{}
	for _, e := range rt.Elements {
		out[e.ElementID] = struct{ Tokens, Visits, Terminated int }{e.Tokens, e.Visits, e.Terminated}
	}
	return out
}

// TestRuntimeReportsCancelledTokens: the overlay must be able to say which branch of a
// deferred choice actually won. Both catches are visited and both leave, so visits and
// live tokens alone are identical on the two branches whatever happened; the winner and
// the cancelled loser differ only in the termination count. Asserted on the aggregate
// read and, because it is a separate code path, on the single-instance one too.
func TestRuntimeReportsCancelledTokens(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", eventGatewayBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, body)
	}
	instance := onlyInstanceKey(t, ts)

	// Armed: one token on each branch, nothing cancelled yet — the state in which the
	// two identical green counts are one race, not two.
	armed := runtimeByElement(t, ts, "")
	for _, id := range []string{"reply", "timeout"} {
		if got := armed[id]; got.Tokens != 1 || got.Visits != 1 || got.Terminated != 0 {
			t.Fatalf("armed %s = %+v, want 1 token, 1 visit, 0 terminated", id, got)
		}
	}
	if got := armed["gw"]; got.Tokens != 0 || got.Visits != 1 {
		t.Fatalf("armed gw = %+v, want 0 tokens and 1 visit (it completes into the waits)", got)
	}

	// The message wins; the timer branch is cancelled.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"go"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, body)
	}

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"aggregate", ""},
		{"single instance", fmt.Sprintf("?instance=%d", instance)},
	} {
		got := runtimeByElement(t, ts, tc.query)
		if e := got["reply"]; e.Visits != 1 || e.Terminated != 0 {
			t.Errorf("%s: winning branch reply = %+v, want 1 visit and 0 terminated", tc.name, e)
		}
		if e := got["timeout"]; e.Visits != 1 || e.Terminated != 1 {
			t.Errorf("%s: losing branch timeout = %+v, want 1 visit and 1 terminated", tc.name, e)
		}
		if e := got["end_reply"]; e.Visits != 1 || e.Terminated != 0 {
			t.Errorf("%s: end_reply = %+v, want 1 visit and 0 terminated", tc.name, e)
		}
		if e := got["end_timeout"]; e.Visits != 0 || e.Terminated != 0 {
			t.Errorf("%s: end_timeout = %+v, want an untouched branch", tc.name, e)
		}
	}
}
