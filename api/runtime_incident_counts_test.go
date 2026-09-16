package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deployParallelTaskProcess deploys a process that fans one token out to n user tasks
// and returns its definition key — the shape that puts more parked tokens in a *single*
// instance than the detail page holds, which is what the single-instance overlay's own
// counting has to survive.
func deployParallelTaskProcess(t *testing.T, ts *httptest.Server, processID string, n int) uint64 {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="%s" isExecutable="true">
    <startEvent id="start"/>
    <parallelGateway id="fork"/>
    <sequenceFlow id="f0" sourceRef="start" targetRef="fork"/>
`, processID)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `    <userTask id="t%d" name="Task %d"/>
    <sequenceFlow id="fin%d" sourceRef="fork" targetRef="t%d"/>
    <sequenceFlow id="fout%d" sourceRef="t%d" targetRef="join"/>
`, i, i, i, i, i, i)
	}
	fmt.Fprintf(&b, `    <parallelGateway id="join"/>
    <endEvent id="end"/>
    <sequenceFlow id="fend" sourceRef="join" targetRef="end"/>
  </process>
</definitions>`)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", b.String(), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy %s: status=%d body=%s", processID, code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	return deploy.Key
}

// What the live diagram says about a definition under a flood, and about the
// definition standing next to one (ADR-draft-the-live-diagram-counts-every-parked-token).
//
// The reported reading: an operations overview counting 10 910 parked tokens over two
// versions of one process, the live view of the current version reporting none at all,
// and the previous version reporting "50" on each of two tasks that held some 5 400
// each. Three surfaces, three different numbers, and the one an operator opens after
// the overview told them something is wrong was the one saying nothing is.

// runtimeCounts is the overlay payload, read for its counts rather than its details.
type runtimeCounts struct {
	Elements []struct {
		ElementID string `json:"elementId"`
		Tokens    int    `json:"tokens"`
		Incidents int    `json:"incidents"`
	} `json:"elements"`
	Incidents []struct {
		ElementInstanceKey uint64 `json:"elementInstanceKey"`
		ElementID          string `json:"elementId"`
	} `json:"incidents"`
	IncidentTotal       int  `json:"incidentTotal"`
	IncidentsTruncated  bool `json:"incidentsTruncated"`
	IncidentCountsExact bool `json:"incidentCountsExact"`
}

func runtimeCountsOf(t *testing.T, ts *httptest.Server, path string) runtimeCounts {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, path, "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime %s: status=%d body=%s", path, code, body)
	}
	var rt runtimeCounts
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	return rt
}

func incidentsOn(rt runtimeCounts, elementID string) int {
	for _, e := range rt.Elements {
		if e.ElementID == elementID {
			return e.Incidents
		}
	}
	return -1
}

// floodDefinition starts n instances of defKey and parks every one of them, returning
// how many it parked. It fails the jobs in one listing rather than one per instance,
// which keeps a few thousand of them a second rather than a minute.
func floodDefinition(t *testing.T, ts *httptest.Server, defKey uint64, n int) int {
	t.Helper()
	for i := 0; i < n; i++ {
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, body)
		}
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks?limit=5000", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key           uint64 `json:"key"`
		ProcessDefKey uint64 `json:"processDefKey"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	parked := 0
	for _, task := range tasks {
		if task.ProcessDefKey != defKey {
			continue
		}
		payload := `{"retries":0,"message":"mockup task simulated failure"}`
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", task.Key), payload, "application/json"); code != http.StatusOK {
			t.Fatalf("fail job: status=%d body=%s", code, body)
		}
		parked++
	}
	if parked != n {
		t.Fatalf("parked %d tokens, wanted %d", parked, n)
	}
	return parked
}

// TestAggregateOverlayCountsEveryParkedTokenOfItsOwnDefinition is the reported bug,
// and the reason the counts left the bounded scan.
//
// The scan walks the incident family in key order and attributes each entry to its
// definition only after reading it. Element-instance keys ascend, so a definition
// deployed after a flood sits entirely past the scan budget: the overlay saw none of
// its incidents, reported zero, and the browser drew a healthy diagram over a process
// whose every token was parked.
func TestAggregateOverlayCountsEveryParkedTokenOfItsOwnDefinition(t *testing.T) {
	ts := newTestServer(t)

	// The older version, flooded well past maxRuntimeIncidentScan (2000).
	older := deployTaskProcess(t, ts, "older")
	const flood = 2100
	floodDefinition(t, ts, older, flood)

	// The newer one, deployed afterwards, with a handful of its own parked tokens —
	// every one of them keyed above the whole flood.
	newer := deployTaskProcess(t, ts, "newer")
	const few = 3
	floodDefinition(t, ts, newer, few)

	// The definition behind the flood reports its own tokens, not a zero.
	rt := runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", newer))
	if !rt.IncidentCountsExact {
		t.Fatalf("incidentCountsExact = false; the counts below would be a floor")
	}
	if rt.IncidentTotal != few {
		t.Errorf("incidentTotal = %d, want %d — the flood in front of it is another definition's", rt.IncidentTotal, few)
	}
	if got := incidentsOn(rt, "review"); got != few {
		t.Errorf("element review: incidents = %d, want %d", got, few)
	}
	// And the panel is handed rows it can actually resolve from. A count without them
	// would be the same failure one step along: the diagram says a task is stuck and
	// the block beside it offers nothing to do about it.
	if len(rt.Incidents) != few {
		t.Fatalf("details = %d rows, want %d", len(rt.Incidents), few)
	}
	for _, inc := range rt.Incidents {
		if inc.ElementID != "review" {
			t.Errorf("detail row on %q, want the task this definition parks on", inc.ElementID)
		}
		if inc.ElementInstanceKey == 0 {
			t.Errorf("detail row carries no element instance key, so nothing can be resolved from it")
		}
	}

	// The flooded definition reports what it actually holds, not its detail page's size.
	rt = runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", older))
	if rt.IncidentTotal != flood {
		t.Errorf("incidentTotal = %d, want %d", rt.IncidentTotal, flood)
	}
	if got := incidentsOn(rt, "review"); got != flood {
		t.Errorf("element review: incidents = %d, want %d (not the %d rows the panel was handed)", got, flood, len(rt.Incidents))
	}
	// The details stay a page, and stay marked as one: the count is the overlay's job,
	// the rows are the resolve panel's.
	if len(rt.Incidents) != 100 {
		t.Errorf("details = %d rows, want the 100-row page", len(rt.Incidents))
	}
	if !rt.IncidentsTruncated {
		t.Errorf("incidentsTruncated = false on a page holding 100 of %d", flood)
	}
}

// TestHealthyDefinitionReportsZeroRatherThanSilence pins the other half: "none" has to
// be a statement. A count that is only ever a floor cannot say a process is fine, and
// the overlay is where an operator looks to conclude exactly that.
func TestHealthyDefinitionReportsZeroRatherThanSilence(t *testing.T) {
	ts := newTestServer(t)
	quiet := deployTaskProcess(t, ts, "quiet")
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", quiet), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	rt := runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", quiet))
	switch {
	case !rt.IncidentCountsExact:
		t.Errorf("incidentCountsExact = false on an engine holding no incident at all")
	case rt.IncidentTotal != 0:
		t.Errorf("incidentTotal = %d, want 0", rt.IncidentTotal)
	case rt.IncidentsTruncated:
		t.Errorf("incidentsTruncated = true with nothing to truncate")
	case incidentsOn(rt, "review") != 0:
		t.Errorf("element review: incidents = %d, want 0", incidentsOn(rt, "review"))
	}
}

// TestOverlayAndSummaryAgree is the property the report was actually about: three
// surfaces, one number. The operations overview counts a definition's incidents off
// /incidents/summary (ADR-0337); the live diagram counts them off its own overlay; and
// an operator moves from the first to the second expecting to find the same flood.
func TestOverlayAndSummaryAgree(t *testing.T) {
	ts := newTestServer(t)
	older := deployTaskProcess(t, ts, "older")
	floodDefinition(t, ts, older, 2100)
	newer := deployTaskProcess(t, ts, "newer")
	floodDefinition(t, ts, newer, 4)

	for _, defKey := range []uint64{older, newer} {
		sum := incidentSummaryQuery(t, ts, fmt.Sprintf("?process=%d", defKey))
		rt := runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", defKey))
		if sum.Total != rt.IncidentTotal {
			t.Errorf("definition %d: summary says %d, overlay says %d", defKey, sum.Total, rt.IncidentTotal)
		}
		for _, g := range sum.Groups {
			if got := incidentsOn(rt, g.ElementID); got != g.Count {
				t.Errorf("definition %d element %s: summary says %d, overlay says %d",
					defKey, g.ElementID, g.Count, got)
			}
		}
	}
}

// TestSingleInstanceOverlayCountsPastItsDetailPage covers the branch the exact reading
// does not correct. Isolating one instance walks that instance's own element instances,
// so its counts are its own — but the walk used to stop looking for incidents once the
// detail page was full, which made its counts a floor too, on the one view where
// nothing else would ever fix them.
func TestSingleInstanceOverlayCountsPastItsDetailPage(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployParallelTaskProcess(t, ts, "fanout", 120)

	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks?limit=5000", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key                uint64 `json:"key"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	if len(tasks) != 120 {
		t.Fatalf("waiting tasks = %d, want the 120 the fan-out opened", len(tasks))
	}
	instKey := tasks[0].ProcessInstanceKey
	for _, task := range tasks {
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", task.Key), `{"retries":0,"message":"mockup task simulated failure"}`, "application/json"); code != http.StatusOK {
			t.Fatalf("fail job: status=%d body=%s", code, body)
		}
	}

	rt := runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime?instance=%d", defKey, instKey))
	if rt.IncidentTotal != 120 {
		t.Errorf("incidentTotal = %d, want the 120 parked in this instance", rt.IncidentTotal)
	}
	if len(rt.Incidents) != 100 {
		t.Errorf("details = %d rows, want the 100-row page", len(rt.Incidents))
	}
	if !rt.IncidentsTruncated {
		t.Errorf("incidentsTruncated = false on a page holding 100 of 120")
	}
	var counted int
	for _, e := range rt.Elements {
		counted += e.Incidents
	}
	if counted != 120 {
		t.Errorf("per-element incidents sum to %d, want 120", counted)
	}
}

// TestResolvingClearsTheOverlayCountsAtOnce. The counts are cached for five seconds so
// a flood costs one walk per four polls; an operator who has just resolved something
// must not spend those seconds looking at what they cleared.
func TestResolvingClearsTheOverlayCountsAtOnce(t *testing.T) {
	ts := newTestServer(t)
	defKey := deployTaskProcess(t, ts, "approval")
	parkTask(t, ts, defKey)
	parkTask(t, ts, defKey)

	rt := runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", defKey))
	if rt.IncidentTotal != 2 {
		t.Fatalf("incidentTotal = %d, want 2", rt.IncidentTotal)
	}

	// Single resolve.
	elKey := rt.Incidents[0].ElementInstanceKey
	if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/incidents/%d/resolve", elKey), `{"retries":1}`, "application/json"); code != http.StatusOK {
		t.Fatalf("resolve: status=%d body=%s", code, body)
	}
	if rt = runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", defKey)); rt.IncidentTotal != 1 {
		t.Errorf("after a single resolve: incidentTotal = %d, want 1", rt.IncidentTotal)
	}

	// Bulk resolve clears the rest.
	resolveIncidents(t, ts, fmt.Sprintf(`{"processDefKey":%d,"retries":1}`, defKey))
	if rt = runtimeCountsOf(t, ts, fmt.Sprintf("/api/v1/processes/%d/runtime", defKey)); rt.IncidentTotal != 0 {
		t.Errorf("after a bulk resolve: incidentTotal = %d, want 0", rt.IncidentTotal)
	}
	if got := incidentsOn(rt, "review"); got != 0 {
		t.Errorf("element review still marked with %d incidents", got)
	}
}
