package compiler

import (
	"strings"
	"testing"
)

// TestParseEscalationEndAndBoundary checks that an escalation end event inside a subprocess
// compiles to TypeEscalationEndEvent with its code, and an escalation boundary on that
// subprocess compiles to a BoundaryEscalation boundary with the code that — unlike an error
// boundary — HONORS cancelActivity="false" as non-interrupting (ADR-0125).
func TestParseEscalationEndAndBoundary(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <escalation id="Esc_late" escalationCode="LATE" name="Late"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <endEvent id="ee"><escalationEventDefinition escalationRef="Esc_late"/></endEvent>
	      <sequenceFlow id="if" sourceRef="ss" targetRef="ee"/>
	    </subProcess>
	    <boundaryEvent id="b" attachedToRef="sub" cancelActivity="false"><escalationEventDefinition escalationRef="Esc_late"/></boundaryEvent>
	    <endEvent id="done"/>
	    <endEvent id="recovered"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="done"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="recovered"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ee := nodeByBpmnId(t, cp, "ee")
	if ee.Type != TypeEscalationEndEvent || ee.Type.String() != "EscalationEndEvent" {
		t.Fatalf("escalation end type = %v (%q), want EscalationEndEvent", ee.Type, ee.Type.String())
	}
	if got := cp.Escalation(ee.Detail).EscalationCode; got != "LATE" {
		t.Errorf("escalation end code = %q, want LATE", got)
	}
	b := nodeByBpmnId(t, cp, "b")
	if b.Type != TypeBoundaryEvent {
		t.Fatalf("boundary type = %v, want BoundaryEvent", b.Type)
	}
	d := cp.BoundaryEvent(b.Detail)
	if d.Kind != BoundaryEscalation {
		t.Errorf("boundary kind = %v, want BoundaryEscalation", d.Kind)
	}
	if d.EscalationCode != "LATE" {
		t.Errorf("boundary escalation code = %q, want LATE", d.EscalationCode)
	}
	// The whole point of escalation: cancelActivity="false" must be honored, so the boundary
	// is non-interrupting (the divergence from an error boundary, which is always interrupting).
	if d.Interrupting {
		t.Errorf("escalation boundary interrupting = true, want false (cancelActivity=\"false\" must be honored)")
	}
}

// TestParseEscalationThrowInterrupting checks that an escalation intermediate throw event
// compiles to TypeEscalationThrowEvent with its code, and that an escalation boundary WITHOUT
// cancelActivity defaults to interrupting (the BPMN default), just like other boundaries
// (ADR-0125).
func TestParseEscalationThrowInterrupting(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <escalation id="Esc" escalationCode="MGR" name="Manager"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <intermediateThrowEvent id="thr"><escalationEventDefinition escalationRef="Esc"/></intermediateThrowEvent>
	      <endEvent id="se"/>
	      <sequenceFlow id="i1" sourceRef="ss" targetRef="thr"/>
	      <sequenceFlow id="i2" sourceRef="thr" targetRef="se"/>
	    </subProcess>
	    <boundaryEvent id="b" attachedToRef="sub"><escalationEventDefinition escalationRef="Esc"/></boundaryEvent>
	    <endEvent id="done"/>
	    <endEvent id="rec"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="done"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="rec"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	thr := nodeByBpmnId(t, cp, "thr")
	if thr.Type != TypeEscalationThrowEvent || thr.Type.String() != "EscalationThrowEvent" {
		t.Fatalf("escalation throw type = %v (%q), want EscalationThrowEvent", thr.Type, thr.Type.String())
	}
	if got := cp.Escalation(thr.Detail).EscalationCode; got != "MGR" {
		t.Errorf("escalation throw code = %q, want MGR", got)
	}
	d := cp.BoundaryEvent(nodeByBpmnId(t, cp, "b").Detail)
	if d.Kind != BoundaryEscalation || d.EscalationCode != "MGR" {
		t.Errorf("boundary = {kind:%v code:%q}, want {BoundaryEscalation MGR}", d.Kind, d.EscalationCode)
	}
	if !d.Interrupting {
		t.Errorf("escalation boundary with no cancelActivity = non-interrupting, want interrupting (BPMN default)")
	}
}

// TestParseEscalationCatchAll checks that an escalation boundary with no escalationRef
// compiles as a code-less catch-all (EscalationCode ""), and an escalation end with a
// code-less escalation raises "" (ADR-0125).
func TestParseEscalationCatchAll(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <escalation id="Esc_bare" name="Bare"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <endEvent id="ee"><escalationEventDefinition escalationRef="Esc_bare"/></endEvent>
	      <sequenceFlow id="if" sourceRef="ss" targetRef="ee"/>
	    </subProcess>
	    <boundaryEvent id="b" attachedToRef="sub"><escalationEventDefinition/></boundaryEvent>
	    <endEvent id="done"/>
	    <endEvent id="recovered"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="done"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="recovered"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.Escalation(nodeByBpmnId(t, cp, "ee").Detail).EscalationCode; got != "" {
		t.Errorf("code-less escalation end code = %q, want empty", got)
	}
	d := cp.BoundaryEvent(nodeByBpmnId(t, cp, "b").Detail)
	if d.Kind != BoundaryEscalation || d.EscalationCode != "" {
		t.Errorf("catch-all boundary = {kind:%v code:%q}, want {BoundaryEscalation \"\"}", d.Kind, d.EscalationCode)
	}
}

// TestParseEscalationEventSubprocess checks that an event subprocess triggered by an
// escalation start compiles with a BoundaryEscalation event-subprocess detail carrying the
// code, and — unlike an error event subprocess — HONORS isInterrupting="false" as
// non-interrupting (ADR-0125, reusing ADR-0082).
func TestParseEscalationEventSubprocess(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <escalation id="Esc" escalationCode="E1" name="E1"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
	    <subProcess id="es" triggeredByEvent="true">
	      <startEvent id="es_start" isInterrupting="false"><escalationEventDefinition escalationRef="Esc"/></startEvent>
	      <endEvent id="es_end"/>
	      <sequenceFlow id="ef" sourceRef="es_start" targetRef="es_end"/>
	    </subProcess>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	es := nodeByBpmnId(t, cp, "es")
	if es.EventSub < 0 {
		t.Fatalf("event subprocess has no EventSub detail (%d)", es.EventSub)
	}
	d := cp.EventSubProcess(es.EventSub)
	if d.Kind != BoundaryEscalation || d.EscalationCode != "E1" {
		t.Errorf("event-sub trigger = {kind:%v code:%q}, want {BoundaryEscalation E1}", d.Kind, d.EscalationCode)
	}
	// The divergence from an error event subprocess: isInterrupting="false" is honored.
	if d.Interrupting {
		t.Errorf("escalation event subprocess interrupting = true, want false (isInterrupting=\"false\" must be honored)")
	}
}

// TestParseEscalationUnknownRef fails deploy when an escalation event references an
// undeclared escalation, in every position — end, intermediate throw, boundary, and
// event-subprocess start (ADR-0125).
func TestParseEscalationUnknownRef(t *testing.T) {
	wrap := func(body string) string {
		return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
		  <process id="p" isExecutable="true">` + body + `</process></definitions>`
	}
	cases := map[string]string{
		"end": `<startEvent id="s"/><endEvent id="e"><escalationEventDefinition escalationRef="Nope"/></endEvent>
		        <sequenceFlow id="f" sourceRef="s" targetRef="e"/>`,
		"throw": `<startEvent id="s"/><intermediateThrowEvent id="t"><escalationEventDefinition escalationRef="Nope"/></intermediateThrowEvent><endEvent id="e"/>
		        <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/>`,
		"boundary": `<startEvent id="s"/>
		        <subProcess id="sub"><startEvent id="ss"/><endEvent id="se"/><sequenceFlow id="isf" sourceRef="ss" targetRef="se"/></subProcess>
		        <boundaryEvent id="b" attachedToRef="sub"><escalationEventDefinition escalationRef="Nope"/></boundaryEvent>
		        <endEvent id="e"/><endEvent id="r"/>
		        <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/><sequenceFlow id="f2" sourceRef="sub" targetRef="e"/><sequenceFlow id="f3" sourceRef="b" targetRef="r"/>`,
		"eventsub": `<startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
		        <subProcess id="es" triggeredByEvent="true"><startEvent id="es_s"><escalationEventDefinition escalationRef="Nope"/></startEvent><endEvent id="es_e"/><sequenceFlow id="ef" sourceRef="es_s" targetRef="es_e"/></subProcess>`,
	}
	for name, body := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(body))); err == nil {
			t.Errorf("%s: Parse succeeded, want an error for an unknown escalationRef", name)
		}
	}
}
