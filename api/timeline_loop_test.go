package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// standardLoopBPMN repeats one script task five times. The script reads loopCounter and
// writes a result, so the test can assert both that the round is identified and that
// what the round wrote is visible against that round.
const standardLoopBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="looper" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="gruss" name="Gruss" scriptFormat="feel">
      <extensionElements>
        <zeebe:script expression="=&quot;Gruss Nr &quot; + string(loopCounter)" resultVariable="letzterGruss"/>
      </extensionElements>
      <standardLoopCharacteristics testBefore="false" loopMaximum="5"/>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="gruss"/>
    <sequenceFlow id="f2" sourceRef="gruss" targetRef="end"/>
  </process>
</definitions>`

// loopTimeline runs the looping definition and returns its finished instance's steps.
func loopTimeline(t *testing.T) []loopStep {
	t.Helper()
	ts := newTestServer(t)
	c := newClient(t)

	code, dep := cReqCT(t, c, ts, http.MethodPost, "/api/v1/deployments", standardLoopBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, raw := cReq(t, c, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), `{}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, raw)
	}
	// The loop is capped and runs to the end, so the instance is already completed.
	code, raw = cReq(t, c, ts, http.MethodGet, "/api/v1/instances?state=completed", "")
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
		t.Fatal("the looping instance did not complete")
	}
	code, raw = cReq(t, c, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", list[0].Key), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, raw)
	}
	var tl struct {
		Steps []loopStep `json:"steps"`
	}
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	return tl.Steps
}

type loopVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Scope string `json:"scope"`
}

type loopStep struct {
	ElementID      string    `json:"elementId"`
	Iteration      int       `json:"iteration"`
	Inputs         []loopVar `json:"inputs"`
	Variables      []loopVar `json:"variables"`
	VariablesAfter []loopVar `json:"variablesAfter"`
}

// TestLoopRoundsAreIdentified is the fix for a replay that showed a looping task as a
// column of identical rows: every round binds its 1-based loopCounter into its own scope
// (ADR-0077/0133), which the timeline never read, so nothing on a step said which round
// it was. The counter now rides along as an input and as the step's own iteration
// number.
func TestLoopRoundsAreIdentified(t *testing.T) {
	steps := loopTimeline(t)

	var rounds []int
	body := 0
	for _, s := range steps {
		if s.ElementID != "gruss" {
			if s.Iteration != 0 {
				t.Errorf("step %s reports iteration %d, but it is not a loop round", s.ElementID, s.Iteration)
			}
			continue
		}
		if s.Iteration == 0 {
			body++ // the loop body itself: the scope the rounds run under, not a round
			continue
		}
		rounds = append(rounds, s.Iteration)
		var counter string
		for _, v := range s.Inputs {
			if v.Name == "loopCounter" {
				counter = v.Value
			}
		}
		if counter != fmt.Sprint(s.Iteration) {
			t.Errorf("round %d carries loopCounter %q among its inputs, want them to agree", s.Iteration, counter)
		}
	}
	if body != 1 {
		t.Errorf("%d activations of the looping task carry no round, want exactly the body", body)
	}
	want := []int{1, 2, 3, 4, 5}
	if len(rounds) != len(want) {
		t.Fatalf("rounds = %v, want %v", rounds, want)
	}
	for i, n := range want {
		if rounds[i] != n {
			t.Fatalf("rounds = %v, want %v — they must be numbered in order", rounds, want)
		}
	}
}

// TestLoopRoundShowsWhatItWrote covers the other half: a standard loop keeps what its
// rounds write at the *body* scope so each round can read the one before it (ADR-0133).
// That scope was not folded either, so a finished round reported no change at all —
// which the replay stated as "wrote nothing" about a round that had done work.
func TestLoopRoundShowsWhatItWrote(t *testing.T) {
	steps := loopTimeline(t)

	find := func(vars []loopVar, name string) (loopVar, bool) {
		for _, v := range vars {
			if v.Name == name {
				return v, true
			}
		}
		return loopVar{}, false
	}
	seen := 0
	for _, s := range steps {
		if s.ElementID != "gruss" || s.Iteration == 0 {
			continue
		}
		seen++
		after, ok := find(s.VariablesAfter, "letzterGruss")
		if !ok {
			t.Fatalf("round %d left no letzterGruss behind: %+v", s.Iteration, s.VariablesAfter)
		}
		if want := fmt.Sprintf("Gruss Nr %d", s.Iteration); after.Value != want {
			t.Errorf("round %d wrote letzterGruss = %q, want %q", s.Iteration, after.Value, want)
		}
		// Held at the loop's own scope, labeled with the looping element — not silently
		// mixed in with the process variables.
		if after.Scope != "gruss" {
			t.Errorf("round %d: letzterGruss carries scope %q, want the looping element", s.Iteration, after.Scope)
		}
		// And what it saw on entry is the previous round's result, which is exactly why
		// the body scope is shared (ADR-0133). The first round inherits nothing.
		before, ok := find(s.Variables, "letzterGruss")
		if s.Iteration == 1 {
			if ok {
				t.Errorf("the first round already saw letzterGruss = %q", before.Value)
			}
			continue
		}
		if want := fmt.Sprintf("Gruss Nr %d", s.Iteration-1); !ok || before.Value != want {
			t.Errorf("round %d saw letzterGruss = %q (present=%v), want the previous round's %q",
				s.Iteration, before.Value, ok, want)
		}
	}
	if seen != 5 {
		t.Fatalf("checked %d rounds, want 5", seen)
	}
}
