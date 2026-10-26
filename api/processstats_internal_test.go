package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pblumer/atlas/state"
)

// processStatsBPMN waits at a user task and then decides: the default branch is the
// only one any instance takes, so "low" is the arrow the heatmap must report as never
// taken (ADR-draft-operations-heatmap).
const processStatsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="stats" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="review"/>
    <exclusiveGateway id="g" default="high"/>
    <endEvent id="done"/>
    <endEvent id="rare"/>
    <sequenceFlow id="toReview" sourceRef="s" targetRef="review"/>
    <sequenceFlow id="toGate" sourceRef="review" targetRef="g"/>
    <sequenceFlow id="high" sourceRef="g" targetRef="done"/>
    <sequenceFlow id="low" sourceRef="g" targetRef="rare"><conditionExpression>=false</conditionExpression></sequenceFlow>
  </process>
</definitions>`

type statsSummaryView struct {
	Count       int   `json:"count"`
	MeanMillis  int64 `json:"meanMillis"`
	P50Millis   int64 `json:"p50Millis"`
	P90Millis   int64 `json:"p90Millis"`
	MaxMillis   int64 `json:"maxMillis"`
	TotalMillis int64 `json:"totalMillis"`
}

type statsView struct {
	From      int64 `json:"from"`
	To        int64 `json:"to"`
	Instances struct {
		Started  int               `json:"started"`
		Finished int               `json:"finished"`
		Running  int               `json:"running"`
		Duration *statsSummaryView `json:"duration"`
	} `json:"instances"`
	Elements []struct {
		ElementID  string            `json:"elementId"`
		Type       string            `json:"type"`
		Runs       int               `json:"runs"`
		Completed  int               `json:"completed"`
		Terminated int               `json:"terminated"`
		Duration   *statsSummaryView `json:"duration"`
		Waiting    *statsSummaryView `json:"waiting"`
		Working    *statsSummaryView `json:"working"`
	} `json:"elements"`
	Flows []struct {
		FlowID string `json:"flowId"`
		Runs   int    `json:"runs"`
	} `json:"flows"`
	Scanned   int  `json:"scanned"`
	Truncated bool `json:"truncated"`
}

func (v statsView) element(t *testing.T, id string) (runs, completed int, duration, waiting, working *statsSummaryView) {
	t.Helper()
	for _, e := range v.Elements {
		if e.ElementID == id {
			return e.Runs, e.Completed, e.Duration, e.Waiting, e.Working
		}
	}
	t.Fatalf("no element %q in %+v", id, v.Elements)
	return
}

func (v statsView) flows() map[string]int {
	m := map[string]int{}
	for _, f := range v.Flows {
		m[f.FlowID] = f.Runs
	}
	return m
}

// statsScenario runs three instances against a controlled clock and returns the server
// and the definition key:
//
//   - A starts at 1000s, its review is claimed at 1100s and completed at 1160s:
//     100s waiting, 60s working, 160s in all;
//   - B starts at 2000s and is completed at 2500s without being claimed — a duration,
//     but no split, because nobody is on record as having taken it;
//   - C starts at 3000s and is still waiting.
func statsScenario(t *testing.T) (*Server, uint64, uint64) {
	t.Helper()
	clk := &testClock{t: int64(1000 * time.Second)}
	srv := newServerWithClock(t, clk)
	defKey := forkPathsDeploy(t, srv, processStatsBPMN)
	at := func(sec int64) { srv.do(func() { clk.t = sec * int64(time.Second) }) }
	start := func() {
		t.Helper()
		if code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), `{}`, "application/json"); code != http.StatusOK {
			t.Fatalf("start: %d (%s)", code, body)
		}
	}
	post := func(path, body string) {
		t.Helper()
		if code, b := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusOK {
			t.Fatalf("%s: %d (%s)", path, code, b)
		}
	}

	start()
	taskA, instA := openUserTask(t, srv)
	at(1100)
	post(fmt.Sprintf("/api/v1/tasks/%d/claim", taskA), `{"assignee":"anna"}`)
	at(1160)
	post(fmt.Sprintf("/api/v1/tasks/%d/complete", taskA), `{}`)

	at(2000)
	start()
	taskB, _ := openUserTask(t, srv)
	at(2500)
	post(fmt.Sprintf("/api/v1/tasks/%d/complete", taskB), `{}`)

	at(3000)
	start()
	return srv, defKey, instA
}

func readProcessStats(t *testing.T, srv *Server, defKey uint64, query string) statsView {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/statistics%s", defKey, query), "", "")
	if code != http.StatusOK {
		t.Fatalf("statistics: %d (%s)", code, body)
	}
	var v statsView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode statistics: %v (%s)", err, body)
	}
	return v
}

// TestProcessStatisticsSplitsTheTime: over the whole history, the review ran three
// times and completed twice; its duration is both completions, its waiting and working
// time only A's, the one somebody claimed. Every element and every flow of the model is
// in the answer, the arrow nobody took at zero.
func TestProcessStatisticsSplitsTheTime(t *testing.T) {
	srv, defKey, _ := statsScenario(t)
	v := readProcessStats(t, srv, defKey, fmt.Sprintf("?to=%d", int64(10000*time.Second)))

	runs, completed, duration, waiting, working := v.element(t, "review")
	if runs != 3 || completed != 2 {
		t.Errorf("review runs/completed = %d/%d, want 3/2", runs, completed)
	}
	if duration == nil || duration.Count != 2 || duration.P50Millis != 160_000 || duration.P90Millis != 500_000 ||
		duration.MaxMillis != 500_000 || duration.TotalMillis != 660_000 || duration.MeanMillis != 330_000 {
		t.Errorf("review duration = %+v, want 2 of them: median 160s, p90 and max 500s, total 660s", duration)
	}
	if waiting == nil || waiting.Count != 1 || waiting.P50Millis != 100_000 {
		t.Errorf("review waiting = %+v, want one task waiting 100s (B was never claimed)", waiting)
	}
	if working == nil || working.Count != 1 || working.P50Millis != 60_000 {
		t.Errorf("review working = %+v, want one task worked 60s", working)
	}
	if _, _, d, w, _ := v.element(t, "rare"); d != nil || w != nil {
		t.Errorf("an element nothing reached carries times: duration %+v waiting %+v", d, w)
	}

	if got := v.flows(); got["toReview"] != 3 || got["toGate"] != 2 || got["high"] != 2 || got["low"] != 0 || len(got) != 4 {
		t.Errorf("flows = %v, want toReview 3, toGate 2, high 2 and low listed at 0", got)
	}
	if v.Instances.Started != 3 || v.Instances.Finished != 2 || v.Instances.Running != 1 {
		t.Errorf("instances = %+v, want 3 started, 2 finished, 1 running", v.Instances)
	}
	if d := v.Instances.Duration; d == nil || d.Count != 2 || d.P50Millis != 160_000 {
		t.Errorf("instance duration = %+v, want 2 finished, median 160s", d)
	}
	if v.Scanned != 3 || v.Truncated {
		t.Errorf("scanned %d truncated %v, want 3 and false", v.Scanned, v.Truncated)
	}
}

// TestProcessStatisticsReadsTheWindowOnly: a window that opens after A finished counts
// what happened inside it — B's run and completion, C's arrival — and nothing of A.
func TestProcessStatisticsReadsTheWindowOnly(t *testing.T) {
	srv, defKey, _ := statsScenario(t)
	v := readProcessStats(t, srv, defKey, fmt.Sprintf("?from=%d&to=%d", int64(1500*time.Second), int64(10000*time.Second)))

	runs, completed, duration, waiting, _ := v.element(t, "review")
	if runs != 2 || completed != 1 {
		t.Errorf("review runs/completed in the window = %d/%d, want 2/1 (B and C; A is before it)", runs, completed)
	}
	if duration == nil || duration.Count != 1 || duration.P50Millis != 500_000 {
		t.Errorf("review duration in the window = %+v, want B's 500s alone", duration)
	}
	if waiting != nil {
		t.Errorf("review waiting in the window = %+v, want none (only A was claimed)", waiting)
	}
	if got := v.flows(); got["toReview"] != 2 || got["toGate"] != 1 {
		t.Errorf("flows in the window = %v, want toReview 2, toGate 1", got)
	}
	if v.Instances.Started != 2 || v.Instances.Finished != 1 {
		t.Errorf("instances in the window = %+v, want 2 started, 1 finished", v.Instances)
	}
}

// TestProcessStatisticsSaysWhenItStopped: the walk is bounded, newest first, and a
// bound that cuts the window short is reported rather than passed off as the whole.
func TestProcessStatisticsSaysWhenItStopped(t *testing.T) {
	srv, defKey, _ := statsScenario(t)
	var resp processStatsResp
	err := srv.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		var err error
		resp, err = foldProcessStatistics(rv, defKey, defs[defKey].cp, 0, int64(10000*time.Second), 1)
		return err
	})
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if !resp.Truncated || resp.Scanned != 1 {
		t.Errorf("scanned %d truncated %v, want 1 and true", resp.Scanned, resp.Truncated)
	}
	// The newest instance is the one read: C, still waiting at the review.
	for _, e := range resp.Elements {
		if e.ElementID == "review" && (e.Runs != 1 || e.Completed != 0) {
			t.Errorf("review under a bound of one = %+v, want C's arrival alone", e)
		}
	}
}

// TestProcessStatisticsRefusesWhatItCannotRead: an unknown definition is a 404, a moment
// that is neither RFC 3339 nor unix nanoseconds is a 400, and so is a window that ends
// before it begins.
func TestProcessStatisticsRefusesWhatItCannotRead(t *testing.T) {
	srv := newServerWithClock(t, &testClock{t: int64(time.Second)})
	defKey := forkPathsDeploy(t, srv, processStatsBPMN)
	for _, c := range []struct {
		path string
		want int
	}{
		{"/api/v1/processes/987654/statistics", http.StatusNotFound},
		{"/api/v1/processes/x/statistics", http.StatusBadRequest},
		{fmt.Sprintf("/api/v1/processes/%d/statistics?from=yesterday", defKey), http.StatusBadRequest},
		{fmt.Sprintf("/api/v1/processes/%d/statistics?to=-5", defKey), http.StatusBadRequest},
		{fmt.Sprintf("/api/v1/processes/%d/statistics?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", defKey), http.StatusBadRequest},
		{fmt.Sprintf("/api/v1/processes/%d/statistics?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z", defKey), http.StatusOK},
	} {
		if code, body := serveInternal(t, srv, http.MethodGet, c.path, "", ""); code != c.want {
			t.Errorf("GET %s = %d (%s), want %d", c.path, code, body, c.want)
		}
	}
}

// TestRuntimeOverlayCarriesFlows: the always-on overlay reads the arrows from the
// maintained counter for the whole definition, and folds one isolated instance's trail
// for that instance alone — the same rule both ways.
func TestRuntimeOverlayCarriesFlows(t *testing.T) {
	srv, defKey, instA := statsScenario(t)
	read := func(query string) map[string]int {
		t.Helper()
		code, body := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime%s", defKey, query), "", "")
		if code != http.StatusOK {
			t.Fatalf("runtime: %d (%s)", code, body)
		}
		var rt struct {
			Flows []struct {
				FlowID string `json:"flowId"`
				Visits int    `json:"visits"`
			} `json:"flows"`
		}
		if err := json.Unmarshal(body, &rt); err != nil {
			t.Fatalf("decode runtime: %v (%s)", err, body)
		}
		m := map[string]int{}
		for _, f := range rt.Flows {
			m[f.FlowID] = f.Visits
		}
		return m
	}
	if got := read(""); got["toReview"] != 3 || got["toGate"] != 2 || got["high"] != 2 || got["low"] != 0 {
		t.Errorf("definition flows = %v, want toReview 3, toGate 2, high 2, low none", got)
	}
	if got := read(fmt.Sprintf("?instance=%d", instA)); got["toReview"] != 1 || got["toGate"] != 1 || got["high"] != 1 || len(got) != 3 {
		t.Errorf("instance A's flows = %v, want each of its three arrows once", got)
	}
}
