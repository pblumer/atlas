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

// loopTimeline runs the looping definition and returns its finished instance's replay:
// the steps and the token frames the diagram is drawn from.
func loopTimeline(t *testing.T) loopReplay {
	t.Helper()
	return runLoopModel(t, standardLoopBPMN)
}

// runLoopModel deploys one looping definition, runs an instance of it to the end, and
// returns that instance's replay.
func runLoopModel(t *testing.T, model string) loopReplay {
	t.Helper()
	ts := newTestServer(t)
	c := newClient(t)

	code, dep := cReqCT(t, c, ts, http.MethodPost, "/api/v1/deployments", model, "application/xml")
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
	var tl loopReplay
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	return tl
}

// loopReplay is the part of an instance's timeline these tests read: the steps, and the
// frames that say which tokens the replay draws at each position.
type loopReplay struct {
	Steps  []loopStep  `json:"steps"`
	Frames []loopFrame `json:"frames"`
}

type loopFrame struct {
	Position uint64      `json:"position"`
	Tokens   []loopToken `json:"tokens"`
}

type loopToken struct {
	ElementID          string `json:"elementId"`
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
	State              string `json:"state"`
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
	Loop           *loopInfo `json:"loop"`
}

// loopInfo mirrors the API's loopView: what the model told the loop to do, and what it
// did (ADR-0077/0133).
type loopInfo struct {
	Kind       string    `json:"kind"`
	Condition  string    `json:"condition"`
	TestBefore bool      `json:"testBefore"`
	Maximum    int32     `json:"maximum"`
	Round      int       `json:"round"`
	Rounds     int       `json:"rounds"`
	Outcome    string    `json:"outcome"`
	StopReason string    `json:"stopReason"`
	Reads      []loopVar `json:"reads"`
	Missing    []string  `json:"missing"`
}

// condLoopModel is one script task repeating while cond holds, capped at 4. The script
// writes a constant, so the condition's own value is fixed and each test can say exactly
// what the loop should have decided.
func condLoopModel(cond string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="condloop" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="rechne" name="rechne was" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=100" resultVariable="result"/></extensionElements>
      <standardLoopCharacteristics loopMaximum="4"><loopCondition>=` + cond + `</loopCondition></standardLoopCharacteristics>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="rechne"/>
    <sequenceFlow id="f2" sourceRef="rechne" targetRef="end"/>
  </process>
</definitions>`
}

// loopRounds picks the rounds of the looping task out of a replay, in order.
func loopRounds(t *testing.T, steps []loopStep) []loopStep {
	t.Helper()
	var out []loopStep
	for _, s := range steps {
		if s.ElementID == "rechne" && s.Iteration > 0 {
			out = append(out, s)
		}
	}
	return out
}

// TestLoopRoundsAreIdentified is the fix for a replay that showed a looping task as a
// column of identical rows: every round binds its 1-based loopCounter into its own scope
// (ADR-0077/0133), which the timeline never read, so nothing on a step said which round
// it was. The counter now rides along as an input and as the step's own iteration
// number.
func TestLoopRoundsAreIdentified(t *testing.T) {
	steps := loopTimeline(t).Steps

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
	steps := loopTimeline(t).Steps

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

// TestLoopReplayDropsAFinishedRoundsToken checks that replaying a loop does not pile up
// one ghost token per round. A completed element instance is normally kept visible until
// the activation it causes appears, so the token does not flicker between two log
// positions — but a loop round activates nothing: the body owns the activity's outgoing
// flow and takes it once, when the loop ends (ADR-0077/0133). Every finished round was
// therefore left waiting for a successor that never came, so a five-round loop drew six
// tokens on one shape and a runaway loop drew hundreds.
func TestLoopReplayDropsAFinishedRoundsToken(t *testing.T) {
	frames := loopTimeline(t).Frames
	if len(frames) == 0 {
		t.Fatal("the replay has no frames")
	}
	peak := 0
	for _, f := range frames {
		if len(f.Tokens) > peak {
			peak = len(f.Tokens)
		}
		live := map[uint64]bool{}
		for _, tk := range f.Tokens {
			if live[tk.ElementInstanceKey] {
				t.Errorf("frame at %d draws element instance %d twice", f.Position, tk.ElementInstanceKey)
			}
			live[tk.ElementInstanceKey] = true
		}
	}
	// The most a frame may hold is the loop body plus the one round running under it —
	// this loop is sequential and the process has no other branch.
	if peak != 2 {
		t.Errorf("the busiest frame holds %d tokens, want 2 (the loop body and its live round)", peak)
	}
	if last := frames[len(frames)-1]; len(last.Tokens) != 0 {
		t.Errorf("the finished instance still draws %d tokens", len(last.Tokens))
	}
}

// TestLoopRoundSaysWhyItStopped is the answer to the question a replay of a loop kept
// raising and could not settle: the activity ran N times — was that the model, or did
// something go wrong? Each round now carries the condition as the author wrote it, the
// values that condition's variables held for that round, and what the loop then did
// (ADR-0077/0133). Here the condition is false from the first round, so the loop runs
// once and stops — and says so, instead of leaving the reader to infer it.
func TestLoopRoundSaysWhyItStopped(t *testing.T) {
	rounds := loopRounds(t, runLoopModel(t, condLoopModel("result &gt;= 500")).Steps)
	if len(rounds) != 1 {
		t.Fatalf("rounds = %d, want 1 (result >= 500 is false after the first)", len(rounds))
	}
	l := rounds[0].Loop
	if l == nil {
		t.Fatal("the round carries no loop explanation")
	}
	if l.Kind != "loop" || l.Condition != "result >= 500" {
		t.Errorf("kind/condition = %q/%q, want loop/%q", l.Kind, l.Condition, "result >= 500")
	}
	if l.Outcome != "stopped" || l.StopReason != "condition" {
		t.Errorf("outcome/reason = %q/%q, want stopped/condition", l.Outcome, l.StopReason)
	}
	// And the value it read, which is the whole point: 100 is not >= 500.
	if len(l.Reads) != 1 || l.Reads[0].Name != "result" || l.Reads[0].Value != "100" {
		t.Errorf("reads = %+v, want the one value the condition read (result = 100)", l.Reads)
	}
}

// TestLoopRoundSaysWhenTheCapStoppedIt is the other half: a condition that stays true is
// bounded by the stated maximum, and the round that hit it says so. Without this the
// operator sees a loop that ran exactly nine times and cannot tell a cap from a
// coincidence.
func TestLoopRoundSaysWhenTheCapStoppedIt(t *testing.T) {
	rounds := loopRounds(t, runLoopModel(t, condLoopModel("result &lt; 500")).Steps)
	if len(rounds) != 4 {
		t.Fatalf("rounds = %d, want 4 (capped by loopMaximum)", len(rounds))
	}
	for _, r := range rounds[:3] {
		if r.Loop == nil || r.Loop.Outcome != "repeated" {
			t.Errorf("round %d: outcome = %+v, want repeated", r.Iteration, r.Loop)
		}
	}
	last := rounds[3].Loop
	if last == nil || last.Outcome != "stopped" || last.StopReason != "maximum" || last.Maximum != 4 {
		t.Errorf("last round = %+v, want stopped at the maximum of 4", last)
	}
}

// TestLoopConditionNamesWhatIsNotInScope covers the failure this whole surface exists
// for: a condition over a variable that is not there. FEEL is null-propagating, so such a
// condition is simply never true — the loop runs to its cap and looks like the condition
// was ignored. Naming the variable turns that into a typo the author can see.
func TestLoopConditionNamesWhatIsNotInScope(t *testing.T) {
	rounds := loopRounds(t, runLoopModel(t, condLoopModel("nichtDa &gt;= 1")).Steps)
	if len(rounds) == 0 {
		t.Fatal("the loop ran no rounds")
	}
	l := rounds[0].Loop
	if l == nil || len(l.Missing) != 1 || l.Missing[0] != "nichtDa" {
		t.Fatalf("missing = %+v, want the one name nothing in scope has", l)
	}
	if len(l.Reads) != 0 {
		t.Errorf("reads = %+v, want none — the condition's variable has no value here", l.Reads)
	}
}

// TestLoopBodySummarisesTheWholeLoop checks the other place the explanation belongs: the
// looping activity itself, which an operator selects on the diagram far more often than
// one round. It reports the model's bound and how many rounds ran — for a loop that
// states no condition, "no condition" *is* the answer to why it ran to its maximum.
func TestLoopBodySummarisesTheWholeLoop(t *testing.T) {
	var body *loopInfo
	for _, s := range loopTimeline(t).Steps {
		if s.ElementID == "gruss" && s.Iteration == 0 {
			body = s.Loop
		}
	}
	if body == nil {
		t.Fatal("the loop body carries no loop explanation")
	}
	if body.Rounds != 5 || body.Maximum != 5 {
		t.Errorf("body = %+v, want 5 rounds against a maximum of 5", body)
	}
	if body.Condition != "" {
		t.Errorf("condition = %q, want empty — this loop states none", body.Condition)
	}
	if body.Round != 0 || body.Outcome != "" {
		t.Errorf("body = %+v, want no round number and no outcome — it is the loop, not a round", body)
	}
}

// multiInstanceModel is the other loop marker: one script task per element of a
// collection, with a completion condition that ends it early.
const multiInstanceModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="miloop" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="setup" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=[10, 20, 30]" resultVariable="items"/></extensionElements>
    </scriptTask>
    <scriptTask id="rechne" name="rechne was" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=item * 2" resultVariable="doppelt"/></extensionElements>
      <multiInstanceLoopCharacteristics isSequential="true">
        <extensionElements><zeebe:loopCharacteristics inputCollection="=items" inputElement="item"/></extensionElements>
        <completionCondition>=doppelt &gt;= 40</completionCondition>
      </multiInstanceLoopCharacteristics>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f0" sourceRef="start" targetRef="setup"/>
    <sequenceFlow id="f1" sourceRef="setup" targetRef="rechne"/>
    <sequenceFlow id="f2" sourceRef="rechne" targetRef="end"/>
  </process>
</definitions>`

// TestMultiInstanceRoundsReadTheirOwnCondition checks the explanation on the other loop
// marker. A multi-instance reads its condition the opposite way round — it says when to
// *stop*, not when to go on — so labelling the two alike would teach the reader the wrong
// thing about one of them. It also stops for reasons the log does not distinguish (the
// collection ran out, or the condition held), and says nothing rather than guessing.
func TestMultiInstanceRoundsReadTheirOwnCondition(t *testing.T) {
	rounds := loopRounds(t, runLoopModel(t, multiInstanceModel).Steps)
	if len(rounds) == 0 {
		t.Fatal("the multi-instance ran no rounds")
	}
	first := rounds[0].Loop
	if first == nil || first.Kind != "multi-instance" {
		t.Fatalf("kind = %+v, want multi-instance", first)
	}
	if first.Condition != "doppelt >= 40" {
		t.Errorf("condition = %q, want the completion condition as written", first.Condition)
	}
	last := rounds[len(rounds)-1].Loop
	if last.Outcome != "stopped" {
		t.Errorf("last round outcome = %q, want stopped", last.Outcome)
	}
	if last.StopReason != "" {
		t.Errorf("stopReason = %q, want none — the record cannot say which bound ended it", last.StopReason)
	}
}
