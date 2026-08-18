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
