package compiler

import (
	"strings"
	"testing"
)

// TestParseNoneThrowEvent checks that an intermediate throw event with no event definition
// compiles to TypeNoneThrowEvent and keeps its real outgoing sequence flow — the milestone
// marker of the business-architecture method (ADR-0305): an element whose only job is to
// leave a named point in the instance's history where no task sits.
func TestParseNoneThrowEvent(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateThrowEvent id="m" name="Identity verification started"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="m"/>
	    <sequenceFlow id="f2" sourceRef="m" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	m := nodeByBpmnId(t, cp, "m")
	if m.Type != TypeNoneThrowEvent || m.Type.String() != "NoneThrowEvent" {
		t.Fatalf("throw type = %v (%q), want NoneThrowEvent", m.Type, m.Type.String())
	}
	// Unlike a link throw, a none throw keeps the edge the model drew: it is a point on
	// the path, not a goto.
	end := nodeByBpmnId(t, cp, "e")
	if tgts := linkTargets(cp, m.ElementId); len(tgts) != 1 || tgts[0] != end.ElementId {
		t.Errorf("none throw outgoing = %v, want [end=%d]", tgts, end.ElementId)
	}
}

// TestParseNoneThrowEventWithEmptyExtensions checks that the ordinary children a modeller
// leaves on an event — documentation, an empty <extensionElements/>, the incoming and
// outgoing hints bpmn-js writes — do not make it look like a definition the compiler does
// not implement. Only a *EventDefinition child does that, and this event has none.
func TestParseNoneThrowEventWithEmptyExtensions(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateThrowEvent id="m" name="Antrag vollständig">
	      <documentation>Fachlicher Meilenstein.</documentation>
	      <extensionElements/>
	      <incoming>f1</incoming>
	      <outgoing>f2</outgoing>
	    </intermediateThrowEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="m"/>
	    <sequenceFlow id="f2" sourceRef="m" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m := nodeByBpmnId(t, cp, "m"); m.Type != TypeNoneThrowEvent {
		t.Errorf("throw type = %v, want NoneThrowEvent", m.Type)
	}
}

// TestParseThrowEventRejectsUnimplementedDefinition is the guard that makes the none throw
// safe to add. Before it existed, "no event definition this compiler knows" and "no event
// definition at all" were the same state, and both were refused. Treating that state as a
// none throw would silently accept a throw event carrying a definition the engine does not
// implement — compiling it to a pass-through that quietly does nothing of what the model
// asks. So an unmatched *EventDefinition child is named and refused.
func TestParseThrowEventRejectsUnimplementedDefinition(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  string
		want string
	}{
		// Timer and conditional are catch-only in BPMN; on a throw they are a modelling
		// error, and one a reader would rather be told about than have ignored.
		{"timer", `<timerEventDefinition><timeDuration>PT5M</timeDuration></timerEventDefinition>`, "timerEventDefinition"},
		{"conditional", `<conditionalEventDefinition><condition>x</condition></conditionalEventDefinition>`, "conditionalEventDefinition"},
		// Error and cancel belong on an end or boundary event, never on an intermediate throw.
		{"error", `<errorEventDefinition errorRef="E"/>`, "errorEventDefinition"},
		{"cancel", `<cancelEventDefinition/>`, "cancelEventDefinition"},
		// A terminate throw is not BPMN at all, and is the shape most likely to be written
		// by hand in the belief that it stops the instance here.
		{"terminate", `<terminateEventDefinition/>`, "terminateEventDefinition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
			  <process id="p" isExecutable="true">
			    <startEvent id="s"/>
			    <intermediateThrowEvent id="m">` + tc.def + `</intermediateThrowEvent>
			    <endEvent id="e"/>
			    <sequenceFlow id="f1" sourceRef="s" targetRef="m"/>
			    <sequenceFlow id="f2" sourceRef="m" targetRef="e"/>
			  </process>
			</definitions>`
			_, err := Parse(1, 1, strings.NewReader(xml))
			if err == nil {
				t.Fatal("Parse succeeded; want a refusal naming the unimplemented event definition")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q so the author can see which child was refused", err, tc.want)
			}
			if !strings.Contains(err.Error(), `"m"`) {
				t.Errorf("error = %q, want it to name the offending element id", err)
			}
		})
	}
}
