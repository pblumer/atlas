package api_test

import (
	"fmt"
	"testing"
)

// twoLoopsModel puts two looping activities one after the other on the same path: a
// standard loop that runs twice, then a multi-instance over three items. A token is not
// consumed by an activity — the same one flows on to the next — so both bodies are
// activated carrying the *same* token id, which is exactly the arrangement that made the
// replay attribute one loop's rounds to the other.
const twoLoopsModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="twoloops" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="zaehle" name="zaehle" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=n + 1" resultVariable="n"/></extensionElements>
      <standardLoopCharacteristics loopMaximum="2"/>
    </scriptTask>
    <scriptTask id="jeKunde" name="je Kunde" scriptFormat="feel">
      <extensionElements><zeebe:script expression="=kunde" resultVariable="letzter"/></extensionElements>
      <multiInstanceLoopCharacteristics isSequential="true">
        <extensionElements><zeebe:loopCharacteristics inputCollection="=kunden" inputElement="kunde"/></extensionElements>
      </multiInstanceLoopCharacteristics>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="zaehle"/>
    <sequenceFlow id="f2" sourceRef="zaehle" targetRef="jeKunde"/>
    <sequenceFlow id="f3" sourceRef="jeKunde" targetRef="end"/>
  </process>
</definitions>`

// bodyOfElement returns the step that is the named activity's loop body — the one
// summarising the whole loop rather than one round of it.
func bodyOfElement(t *testing.T, steps []loopStep, elementID string) loopStep {
	t.Helper()
	for _, s := range steps {
		if s.ElementID == elementID && s.Loop != nil && s.Loop.Round == 0 {
			return s
		}
	}
	t.Fatalf("no loop body for %s", elementID)
	return loopStep{}
}

// TestEachLoopCountsOnlyItsOwnRounds is the fix for a body that claimed more rounds than
// it ran. The rounds of a loop were grouped by the body's *token*, but a token flows on
// from one activity to the next, so every looping activity along a path shares one — and
// each body was handed the sum of them all. A five-round loop followed by two threes
// reported eleven rounds on each of the three.
func TestEachLoopCountsOnlyItsOwnRounds(t *testing.T) {
	steps := runLoopModelWith(t, twoLoopsModel, `{"variables":{"n":0,"kunden":["a","b","c"]}}`).Steps

	// The premise: both bodies really are on the same token. Without that this test would
	// pass for the wrong reason.
	zaehle, jeKunde := bodyOfElement(t, steps, "zaehle"), bodyOfElement(t, steps, "jeKunde")
	if zaehle.TokenID == 0 || zaehle.TokenID != jeKunde.TokenID {
		t.Fatalf("the two loop bodies carry tokens %d and %d; the bug needs them to share one",
			zaehle.TokenID, jeKunde.TokenID)
	}

	if zaehle.Loop.Rounds != 2 {
		t.Errorf("the standard loop reports %d rounds, want 2 — not the other loop's as well", zaehle.Loop.Rounds)
	}
	if jeKunde.Loop.Rounds != 3 {
		t.Errorf("the multi-instance reports %d rounds, want 3 — one per item of its collection", jeKunde.Loop.Rounds)
	}

	// The rounds themselves must be attributed the same way: counting them per element is
	// the other half of the same claim.
	perElement := map[string]int{}
	for _, s := range steps {
		if s.Loop != nil && s.Loop.Round > 0 {
			perElement[s.ElementID]++
		}
	}
	if perElement["zaehle"] != 2 || perElement["jeKunde"] != 3 {
		t.Errorf("rounds per element = %v, want zaehle 2 and jeKunde 3", perElement)
	}
}

// TestALoopsLastRoundIsNotRepeatedByTheNextLoop covers what the same mix-up did to a
// round's outcome. "Did another round follow this one" was asked of every round sharing
// the token, so the last round of the first loop saw the *second* loop's rounds further
// down the log and reported itself as repeated — the one round that had in fact stopped.
func TestALoopsLastRoundIsNotRepeatedByTheNextLoop(t *testing.T) {
	steps := runLoopModelWith(t, twoLoopsModel, `{"variables":{"n":0,"kunden":["a","b","c"]}}`).Steps

	for _, el := range []struct {
		id     string
		rounds int
	}{{"zaehle", 2}, {"jeKunde", 3}} {
		var outcomes []string
		for _, s := range steps {
			if s.ElementID == el.id && s.Loop != nil && s.Loop.Round > 0 {
				outcomes = append(outcomes, fmt.Sprintf("#%d=%s", s.Loop.Round, s.Loop.Outcome))
			}
		}
		if len(outcomes) != el.rounds {
			t.Fatalf("%s: %v, want %d rounds", el.id, outcomes, el.rounds)
		}
		want := fmt.Sprintf("#%d=stopped", el.rounds)
		if outcomes[len(outcomes)-1] != want {
			t.Errorf("%s: %v — the last round should be %q, not carried on by the loop after it",
				el.id, outcomes, want)
		}
		for _, o := range outcomes[:len(outcomes)-1] {
			if got := o[len(o)-len("repeated"):]; got != "repeated" {
				t.Errorf("%s: %v — every round before the last one went round again", el.id, outcomes)
				break
			}
		}
	}
}
