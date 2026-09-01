package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// forkedWritersBPMN is the shape that made the diagram's in/out card lie: a parallel
// fork whose two branches each write one variable and rejoin. Both branches are live at
// the same time, so each one's completion snapshot already contains the other's write.
const forkedWritersBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="forked" isExecutable="true">
    <startEvent id="start"/>
    <parallelGateway id="fork"/>
    <scriptTask id="erstelle" name="erstelle ein Ticket" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=&quot;PAT-9&quot;" resultVariable="newTicket"/></extensionElements>
    </scriptTask>
    <scriptTask id="hole" name="alle Tickets holen" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=[1, 2, 3, 4]" resultVariable="tickets"/></extensionElements>
    </scriptTask>
    <parallelGateway id="join"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="fork"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="erstelle"/>
    <sequenceFlow id="f3" sourceRef="fork" targetRef="hole"/>
    <sequenceFlow id="f4" sourceRef="erstelle" targetRef="join"/>
    <sequenceFlow id="f5" sourceRef="hole" targetRef="join"/>
    <sequenceFlow id="f6" sourceRef="join" targetRef="end"/>
  </process>
</definitions>`

// writesReplay is the part of the timeline these tests read: per step, what the element
// was holding on entry, what stood after it, and what it itself wrote.
type writesReplay struct {
	VariableAttribution bool `json:"variableAttribution"`
	Steps               []struct {
		ElementID      string     `json:"elementId"`
		Variables      []namedVar `json:"variables"`
		VariablesAfter []namedVar `json:"variablesAfter"`
		Writes         []namedVar `json:"writes"`
	} `json:"steps"`
}

type namedVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Scope string `json:"scope"`
}

func names(vars []namedVar) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Name)
	}
	return out
}

// runCompletedTimeline deploys one definition, runs an instance of it to the end, and
// returns that instance's replay.
func runCompletedTimeline(t *testing.T, bpmn, start string) writesReplay {
	t.Helper()
	ts := newTestServer(t)
	c := newClient(t)

	code, dep := cReqCT(t, c, ts, http.MethodPost, "/api/v1/deployments", bpmn, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, raw := cReq(t, c, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), start); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, raw)
	}
	code, raw := cReq(t, c, ts, http.MethodGet, "/api/v1/instances?state=completed", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d (%s)", code, raw)
	}
	var list []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("the instance did not complete")
	}
	code, raw = cReq(t, c, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", list[0].Key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, raw)
	}
	var tl writesReplay
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	return tl
}

// TestTimelineWritesAreTheElementsOwn is the regression test for the report from the
// Operations replay: selecting "alle Tickets holen" on a fork showed newTicket — the
// variable the *other* branch produced — in its out section. That came from inferring
// the out list by diffing the element's entry and exit snapshots, which on a fork spans
// the sibling's writes too. Each step now carries what its own element wrote, and each
// branch must carry exactly its own variable.
func TestTimelineWritesAreTheElementsOwn(t *testing.T) {
	// Seeded at creation, so the fold also has a variable no element wrote: it must be
	// claimed by nobody, least of all by the start event it happens to precede.
	tl := runCompletedTimeline(t, forkedWritersBPMN, `{"seed":"s"}`)
	if !tl.VariableAttribution {
		t.Fatal("variableAttribution = false, want true — this instance's writes were recorded with their producer")
	}

	want := map[string][]string{
		"erstelle": {"newTicket"},
		"hole":     {"tickets"},
		// Everything else on the fork is pass-through: a gateway or an event writes
		// nothing, so it must claim nothing — before this, each of them "produced"
		// whatever happened to change while it was open.
		"start": nil,
		"fork":  nil,
		"join":  nil,
		"end":   nil,
	}
	seen := map[string]bool{}
	for _, s := range tl.Steps {
		got, ok := want[s.ElementID]
		if !ok {
			t.Fatalf("unexpected step %q", s.ElementID)
		}
		seen[s.ElementID] = true
		if have := names(s.Writes); !equalStrings(have, got) {
			t.Errorf("%s wrote %v, want %v", s.ElementID, have, got)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("no step for %s", id)
		}
	}
}

// TestTimelineVariablesAfterStillSnapshots pins what did not change, and why the
// snapshots could never answer the question on their own: variablesAfter is the whole
// instance as it stood when the element finished, so on a fork it holds the sibling
// branch's work as well — which is exactly what a diff against it would hand back as
// "this element's output". The Variables tab shows that set; only "what did this
// produce" reads writes.
func TestTimelineVariablesAfterStillSnapshots(t *testing.T) {
	tl := runCompletedTimeline(t, forkedWritersBPMN, `{}`)
	seen := false
	for _, s := range tl.Steps {
		if s.ElementID != "hole" {
			continue
		}
		seen = true
		if have := names(s.VariablesAfter); len(have) != 2 {
			t.Fatalf("variablesAfter for hole = %v, want both branches' variables — it is a snapshot", have)
		}
		// The sibling branch ran first in this batch, so its write is already in the
		// snapshot this element entered on: the very overlap that made the old diff
		// credit "hole" with the ticket "erstelle" created.
		if have := names(s.Variables); !equalStrings(have, []string{"newTicket"}) {
			t.Errorf("variables on entry for hole = %v, want the sibling's newTicket", have)
		}
	}
	if !seen {
		t.Fatal("no step for hole")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
