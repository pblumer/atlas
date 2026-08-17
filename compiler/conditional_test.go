package compiler

import (
	"strings"
	"testing"
)

// TestParseConditionalCatch checks that a conditional intermediate catch event compiles to
// TypeConditionalCatchEvent with its FEEL condition, and that the compiled condition knows the
// variable it reads (ADR-0134).
func TestParseConditionalCatch(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateCatchEvent id="wait"><conditionalEventDefinition><condition>=ready</condition></conditionalEventDefinition></intermediateCatchEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
	    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	wait := nodeByBpmnId(t, cp, "wait")
	if wait.Type != TypeConditionalCatchEvent || wait.Type.String() != "ConditionalCatchEvent" {
		t.Fatalf("catch type = %v (%q), want ConditionalCatchEvent", wait.Type, wait.Type.String())
	}
	cond := cp.Conditional(wait.Detail).Condition
	if cond == nil {
		t.Fatal("conditional catch has no compiled condition")
	}
	if ins := cond.Inputs(); len(ins) != 1 || ins[0] != "ready" {
		t.Errorf("condition inputs = %v, want [ready]", ins)
	}
	if !cp.HasConditionalEvents() {
		t.Error("HasConditionalEvents = false, want true")
	}
}

// TestParseConditionalBoundary checks that a conditional boundary event compiles to a
// BoundaryConditional boundary carrying its FEEL condition, and — like an escalation boundary —
// HONORS cancelActivity="false" as non-interrupting (ADR-0134).
func TestParseConditionalBoundary(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <userTask id="task"/>
	    <boundaryEvent id="b" attachedToRef="task" cancelActivity="false"><conditionalEventDefinition><condition>=cancelled</condition></conditionalEventDefinition></boundaryEvent>
	    <endEvent id="done"/>
	    <endEvent id="rec"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="task"/>
	    <sequenceFlow id="f2" sourceRef="task" targetRef="done"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="rec"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	b := nodeByBpmnId(t, cp, "b")
	if b.Type != TypeBoundaryEvent {
		t.Fatalf("boundary type = %v, want BoundaryEvent", b.Type)
	}
	d := cp.BoundaryEvent(b.Detail)
	if d.Kind != BoundaryConditional {
		t.Errorf("boundary kind = %v, want BoundaryConditional", d.Kind)
	}
	if d.Condition == nil {
		t.Fatal("conditional boundary has no compiled condition")
	}
	if ins := d.Condition.Inputs(); len(ins) != 1 || ins[0] != "cancelled" {
		t.Errorf("condition inputs = %v, want [cancelled]", ins)
	}
	// The escalation-like divergence from error/cancel boundaries: cancelActivity="false" is honored.
	if d.Interrupting {
		t.Errorf("conditional boundary interrupting = true, want false (cancelActivity=\"false\" honored)")
	}
}

// TestParseConditionalBoundaryInterruptingDefault checks a conditional boundary with no
// cancelActivity defaults to interrupting (BPMN default), like other honoring boundaries.
func TestParseConditionalBoundaryInterruptingDefault(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <userTask id="task"/>
	    <boundaryEvent id="b" attachedToRef="task"><conditionalEventDefinition><condition>=x</condition></conditionalEventDefinition></boundaryEvent>
	    <endEvent id="done"/><endEvent id="rec"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="task"/>
	    <sequenceFlow id="f2" sourceRef="task" targetRef="done"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="rec"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d := cp.BoundaryEvent(nodeByBpmnId(t, cp, "b").Detail); !d.Interrupting {
		t.Errorf("conditional boundary with no cancelActivity = non-interrupting, want interrupting (BPMN default)")
	}
}

// TestParseConditionalEmptyIsError rejects a conditional event with no condition — a predicate
// that can never fire (ADR-0134).
func TestParseConditionalEmptyIsError(t *testing.T) {
	cases := map[string]string{
		"catch": `<startEvent id="s"/><intermediateCatchEvent id="w"><conditionalEventDefinition><condition></condition></conditionalEventDefinition></intermediateCatchEvent>
		          <sequenceFlow id="f" sourceRef="s" targetRef="w"/>`,
		"boundary": `<startEvent id="s"/><userTask id="t"/>
		          <boundaryEvent id="b" attachedToRef="t"><conditionalEventDefinition><condition>  </condition></conditionalEventDefinition></boundaryEvent>
		          <endEvent id="e"/><endEvent id="r"/>
		          <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/><sequenceFlow id="f3" sourceRef="b" targetRef="r"/>`,
	}
	for name, body := range cases {
		xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p" isExecutable="true">` + body + `</process></definitions>`
		_, err := Parse(1, 1, strings.NewReader(xml))
		if err == nil {
			t.Errorf("%s: Parse succeeded, want an error for an empty condition", name)
			continue
		}
		if !strings.Contains(err.Error(), "no condition") {
			t.Errorf("%s: error %q should mention %q", name, err.Error(), "no condition")
		}
	}
}

// TestParseNoConditional: a process without a conditional event has HasConditionalEvents false
// (ADR-0134) — so the runtime pays nothing on a variable write.
func TestParseNoConditional(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cp.HasConditionalEvents() {
		t.Error("HasConditionalEvents = true, want false (no conditional events)")
	}
}
