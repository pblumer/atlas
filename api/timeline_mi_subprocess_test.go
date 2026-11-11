package api_test

import (
	"encoding/json"
	"testing"
)

// miSubprocessBPMN runs an embedded subprocess three times in parallel. Each iteration
// walks its own token through an inner script task and a nested multi-instance task of
// two rounds.
const miSubprocessBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="misub" isExecutable="true">
    <startEvent id="start"/>
    <subProcess id="korr" name="Datensatz korrigieren">
      <multiInstanceLoopCharacteristics isSequential="false"><loopCardinality>3</loopCardinality></multiInstanceLoopCharacteristics>
      <startEvent id="k_start"/>
      <scriptTask id="bearb" name="Datensatz bearbeiten" scriptFormat="feel">
        <extensionElements><zeebe:script expression="=1" resultVariable="r"/></extensionElements>
      </scriptTask>
      <scriptTask id="pruef" name="Feld pruefen" scriptFormat="feel">
        <extensionElements><zeebe:script expression="=2" resultVariable="p"/></extensionElements>
        <multiInstanceLoopCharacteristics isSequential="false"><loopCardinality>2</loopCardinality></multiInstanceLoopCharacteristics>
      </scriptTask>
      <endEvent id="k_end"/>
      <sequenceFlow id="k1" sourceRef="k_start" targetRef="bearb"/>
      <sequenceFlow id="k2" sourceRef="bearb" targetRef="pruef"/>
      <sequenceFlow id="k3" sourceRef="pruef" targetRef="k_end"/>
    </subProcess>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="korr"/>
    <sequenceFlow id="f2" sourceRef="korr" targetRef="end"/>
  </process>
</definitions>`

// TestTimelineNestsInnerStepsUnderTheirIteration: the inner start of an embedded
// subprocess gets a fresh token, so token lineage cannot say which iteration of a
// multi-instance subprocess an inner step belongs to. The timeline says it instead, per
// step, so the history can nest every iteration's inner steps under that iteration.
func TestTimelineNestsInnerStepsUnderTheirIteration(t *testing.T) {
	raw := runModelTimeline(t, miSubprocessBPMN, `{}`)
	var tl struct {
		Steps []struct {
			ElementID          string `json:"elementId"`
			ElementInstanceKey uint64 `json:"elementInstanceKey"`
			TokenID            uint64 `json:"tokenId"`
			ParentTokenID      uint64 `json:"parentTokenId"`
			Iteration          int    `json:"iteration"`
			InIteration        uint64 `json:"inIteration"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}

	// The outer iterations: the subprocess steps that are rounds.
	outer := map[uint64]bool{}
	for _, s := range tl.Steps {
		if s.ElementID == "korr" && s.Iteration > 0 {
			outer[s.ElementInstanceKey] = true
		}
	}
	if len(outer) != 3 {
		t.Fatalf("outer iterations = %d, want 3", len(outer))
	}

	inner := map[string]bool{"k_start": true, "bearb": true, "k_end": true}
	owned := map[uint64]map[string]int{}
	for _, s := range tl.Steps {
		switch {
		case s.ElementID == "start" || s.ElementID == "end" || s.ElementID == "korr":
			// Outside every iteration, or an iteration itself (its body groups it).
			if s.InIteration != 0 {
				t.Errorf("%s (eik %d): inIteration = %d, want 0", s.ElementID, s.ElementInstanceKey, s.InIteration)
			}
		case inner[s.ElementID] || (s.ElementID == "pruef" && s.Iteration == 0):
			// An inner element, or the nested body: owned by the outer iteration it ran in.
			if !outer[s.InIteration] {
				t.Errorf("%s (eik %d): inIteration = %d, not one of the outer iterations", s.ElementID, s.ElementInstanceKey, s.InIteration)
				continue
			}
			if owned[s.InIteration] == nil {
				owned[s.InIteration] = map[string]int{}
			}
			owned[s.InIteration][s.ElementID]++
		case s.ElementID == "pruef":
			// A nested round owns itself; its nested body groups it by token lineage.
			if s.InIteration != 0 {
				t.Errorf("nested round (eik %d): inIteration = %d, want 0", s.ElementInstanceKey, s.InIteration)
			}
		}
	}
	for it := range outer {
		got := owned[it]
		for _, id := range []string{"k_start", "bearb", "pruef", "k_end"} {
			if got[id] != 1 {
				t.Errorf("iteration %d holds %d %s steps, want 1 (%v)", it, got[id], id, got)
			}
		}
	}
}
