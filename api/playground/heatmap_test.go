package playground

import (
	"net/http"
	"testing"
)

// gatewayXML branches on a start variable, so a run over a dataset leaves one
// path hot and — depending on the data — the other cold.
const gatewayXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d3" targetNamespace="http://atlas/test">
  <process id="branching" name="Branching" isExecutable="true">
    <startEvent id="start"/>
    <exclusiveGateway id="decide" default="f_small"/>
    <userTask id="review"/>
    <userTask id="autopay"/>
    <endEvent id="end"/>
    <sequenceFlow id="f_in" sourceRef="start" targetRef="decide"/>
    <sequenceFlow id="f_large" sourceRef="decide" targetRef="review">
      <conditionExpression>= amount &gt; 1000</conditionExpression>
    </sequenceFlow>
    <sequenceFlow id="f_small" sourceRef="decide" targetRef="autopay"/>
    <sequenceFlow id="f_reviewed" sourceRef="review" targetRef="end"/>
    <sequenceFlow id="f_paid" sourceRef="autopay" targetRef="end"/>
  </process>
</definitions>`

// The heat map endpoint answers what the diagram's own overlay cannot: how often
// each *sequence flow* carried a token, and which parts of the model the data
// never reached.
func TestHeatMapEndpointCountsFlowsAndNamesTheColdOnes(t *testing.T) {
	svc := newService(t)
	body := `{"source":"xml","xml":` + jsonString(gatewayXML) + `,"seed":1,` +
		`"startTime":"2026-03-05T08:00:00Z","stubs":{"human":{"minMillis":60000,"maxMillis":60000}}}`
	var sess sessionResp
	decodeInto(t, call(t, svc.HandleOpen, http.MethodPost, body, nil), &sess)
	vals := map[string]string{"id": sess.ID}

	rec := call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"amount":10},{"amount":20},{"amount":5000}]}`, vals)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d, body %s", rec.Code, rec.Body)
	}
	waitForRun(t, svc, sess.ID, "finished")

	var hm heatMapResp
	decodeInto(t, call(t, svc.HandleHeatMap, http.MethodGet, "", vals), &hm)

	elements := map[string]int64{}
	for _, e := range hm.Elements {
		elements[e.Id] = e.Count
	}
	if elements["decide"] != 3 {
		t.Errorf("decide = %d, want 3; got %+v", elements["decide"], hm.Elements)
	}
	if elements["autopay"] != 2 || elements["review"] != 1 {
		t.Errorf("branches = %d small / %d large, want 2 and 1", elements["autopay"], elements["review"])
	}

	flows := map[string]int64{}
	for _, f := range hm.Flows {
		flows[f.From+"→"+f.To] = f.Count
	}
	if flows["decide→autopay"] != 2 || flows["decide→review"] != 1 {
		t.Errorf("flows = %+v, want the default taken twice and the condition once", hm.Flows)
	}
	// The scale a client colours by is the busiest part of the diagram, and it is
	// stated rather than left for every client to derive differently.
	if hm.MaxCount != 3 {
		t.Errorf("maxCount = %d, want 3 — the start, the gateway and the end", hm.MaxCount)
	}
}

// A model whose branches the data never reached still lists them, at zero. That
// is the coverage half: a client draws a cold path only if it is told there is
// one.
func TestHeatMapEndpointListsTheUntouchedBranch(t *testing.T) {
	svc := newService(t)
	body := `{"source":"xml","xml":` + jsonString(gatewayXML) + `,"seed":1,` +
		`"startTime":"2026-03-05T08:00:00Z","stubs":{"human":{"minMillis":60000,"maxMillis":60000}}}`
	var sess sessionResp
	decodeInto(t, call(t, svc.HandleOpen, http.MethodPost, body, nil), &sess)
	vals := map[string]string{"id": sess.ID}

	call(t, svc.HandleStartRun, http.MethodPost, `{"cases":[{"amount":10},{"amount":20}]}`, vals)
	waitForRun(t, svc, sess.ID, "finished")

	var hm heatMapResp
	decodeInto(t, call(t, svc.HandleHeatMap, http.MethodGet, "", vals), &hm)

	cold := false
	for _, f := range hm.Flows {
		if f.From == "decide" && f.To == "review" {
			cold = true
			if f.Count != 0 {
				t.Errorf("the large branch was taken %d times, want 0", f.Count)
			}
		}
	}
	if !cold {
		t.Errorf("the untaken branch is missing from %+v; a cold path has to be listed to be drawn", hm.Flows)
	}
}

// The report carries the run laid out over simulated time, so a client can draw
// when the work arrived and when it drained.
func TestReportCarriesTheTimeline(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"n":1},{"n":2},{"n":3}],"arrival":{"mode":"allAtOnce"}}`, vals)
	waitForRun(t, svc, id, "finished")

	var rep reportResp
	decodeInto(t, call(t, svc.HandleReport, http.MethodGet, "", vals), &rep)

	if len(rep.Timeline.Buckets) == 0 {
		t.Fatalf("the report carries no timeline: %+v", rep.Timeline)
	}
	if rep.Timeline.WidthMillis <= 0 {
		t.Errorf("bucket width = %d ms, want the run's span divided up", rep.Timeline.WidthMillis)
	}
	var started, completed int
	for _, b := range rep.Timeline.Buckets {
		started += b.Started
		completed += b.Completed
	}
	if started != 3 || completed != 3 {
		t.Errorf("timeline totals = %d/%d, want three started and three completed", started, completed)
	}
	if rep.Timeline.Buckets[0].At == "" {
		t.Error("a bucket without an instant cannot be drawn on a time axis")
	}
}
