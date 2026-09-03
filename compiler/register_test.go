package compiler

import (
	"strings"
	"testing"
)

// TestParseRejectsBadElementIds covers the two ways an id can be unusable. Every
// step after registration resolves elements through the id table, so a model that
// cannot populate it is rejected at the door.
func TestParseRejectsBadElementIds(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{
			"duplicate id",
			`<startEvent id="s"/><task id="dup"/><task id="dup"/><endEvent id="e"/>`,
			`duplicate element id "dup"`,
		},
		{
			"empty id",
			`<startEvent id="s"/><task id=""/><endEvent id="e"/>`,
			"element with empty id",
		},
		{
			"missing id attribute",
			`<startEvent id="s"/><task/><endEvent id="e"/>`,
			"element with empty id",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
			  <process id="p" isExecutable="true">` + tc.body + `</process></definitions>`
			_, err := Parse(1, 1, strings.NewReader(xml))
			if err == nil {
				t.Fatal("Parse: want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestParseReportsRejectedIdAheadOfLaterErrors pins which error an author sees
// when a model has a bad id *and* an unrelated problem further along. The scope
// walk carries on past a rejected id — so that valid ids are still recorded and
// nothing downstream reports a phantom failure caused by the gap — which means
// both errors exist by the time the walk ends. The id wins: everything after
// registration resolves elements through the table, so the id has to be fixed
// first regardless, and naming the later problem would send the author to the
// wrong line.
func TestParseReportsRejectedIdAheadOfLaterErrors(t *testing.T) {
	// Start events are walked before service tasks, so the duplicate is rejected
	// first and the service task's own error is raised after it.
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="dup"/>
	    <startEvent id="dup"/>
	    <serviceTask id="later"/>
	    <endEvent id="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want an error, got nil")
	}
	if !strings.Contains(err.Error(), `duplicate element id "dup"`) {
		t.Errorf("error = %q, want the duplicate id reported first", err.Error())
	}

	// Without the duplicate, the later problem is what the author is told about —
	// so the test above is not passing because the service task compiles fine.
	clean := strings.Replace(xml, `<startEvent id="dup"/>
	    <startEvent id="dup"/>`, `<startEvent id="s"/>`, 1)
	if _, err := Parse(1, 1, strings.NewReader(clean)); err == nil ||
		!strings.Contains(err.Error(), "no task definition type") {
		t.Errorf("without the duplicate, error = %v, want the service task's own error", err)
	}
}

// TestParseRegistersEveryValidIdDespiteARejection is the reason the walk does not
// stop at the first rejected id: the ids that are fine still have to land in the
// table, so a node that resolves another through it — a boundary event finding its
// host — cannot fail for a second, invented reason. The rejection is reported; it
// is just not allowed to turn one bad id into two errors.
func TestParseRegistersEveryValidIdDespiteARejection(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="dup"/>
	    <startEvent id="dup"/>
	    <serviceTask id="host"><extensionElements><zeebe:taskDefinition type="t"/></extensionElements></serviceTask>
	    <boundaryEvent id="b" attachedToRef="host"><timerEventDefinition><timeDuration>PT1M</timeDuration></timerEventDefinition></boundaryEvent>
	    <endEvent id="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want the duplicate reported, got nil")
	}
	// "host" is registered after the rejection and the boundary event resolves it,
	// so the duplicate is the only thing reported — not an "unknown host" as well.
	if !strings.Contains(err.Error(), `duplicate element id "dup"`) {
		t.Errorf("error = %q, want only the duplicate id", err.Error())
	}
	for _, phantom := range []string{"host", "attachedToRef", "unknown"} {
		if strings.Contains(err.Error(), phantom) {
			t.Errorf("error = %q mentions %q — the rejection leaked into a second failure", err.Error(), phantom)
		}
	}
}

// TestParseReportsFirstWiringRejection pins which error an author sees when a
// model has more than one wiring problem. The four wiring passes (data
// associations, io mappings, loop characteristics) no longer return at the
// failing call site — they keep going so every valid association still wires
// against a complete id table — so more than one rejection can exist by the time
// the passes finish. The first one wins, which is the one the old code would
// have returned at.
func TestParseReportsFirstWiringRejection(t *testing.T) {
	// Two ioMapping inputs with no target, on two different tasks. The first task
	// in the walk is the one reported.
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="first">
	      <extensionElements>
	        <zeebe:taskDefinition type="t"/>
	        <zeebe:ioMapping><zeebe:input source="= 1" target=""/></zeebe:ioMapping>
	      </extensionElements>
	    </serviceTask>
	    <serviceTask id="second">
	      <extensionElements>
	        <zeebe:taskDefinition type="t"/>
	        <zeebe:ioMapping><zeebe:input source="= 2" target=""/></zeebe:ioMapping>
	      </extensionElements>
	    </serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="first"/>
	    <sequenceFlow id="f2" sourceRef="first" targetRef="second"/>
	    <sequenceFlow id="f3" sourceRef="second" targetRef="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want an error for the target-less ioMapping input, got nil")
	}
	if !strings.Contains(err.Error(), `"first"`) {
		t.Errorf("error = %q, want the first offending task reported", err.Error())
	}
	if strings.Contains(err.Error(), `"second"`) {
		t.Errorf("error = %q, want only the first rejection, not the later one", err.Error())
	}
}

// TestParseWiringRejectionSurvivesLaterPasses checks a rejection raised by an
// early wiring pass is still the error reported after the later passes have run.
// The passes carry on past a rejection, so a bug that let a later pass overwrite
// or swallow the kept error would otherwise only show up as a confusing message
// on a model with two unrelated problems.
func TestParseWiringRejectionSurvivesLaterPasses(t *testing.T) {
	// A data output association naming an unknown target (first pass) plus an
	// activity carrying both loop markers (last pass).
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="task">
	      <extensionElements><zeebe:taskDefinition type="t"/></extensionElements>
	      <dataOutputAssociation id="doa"><targetRef>ghost</targetRef></dataOutputAssociation>
	      <multiInstanceLoopCharacteristics>
	        <extensionElements><zeebe:loopCharacteristics inputCollection="= [1]"/></extensionElements>
	      </multiInstanceLoopCharacteristics>
	      <standardLoopCharacteristics/>
	    </serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="task"/>
	    <sequenceFlow id="f2" sourceRef="task" targetRef="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want an error, got nil")
	}
	// On what was rejected rather than on one word of the wording: the message is
	// shared with the data-*input* path, so it cannot name targetRef.
	if !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("error = %q, want the data-association rejection from the first pass", err.Error())
	}
}
