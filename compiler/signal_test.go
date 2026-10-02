package compiler

import (
	"strings"
	"testing"
)

// A model with a top-level <signal> and a signal throw + catch referencing it
// (ADR-0088 Phase 1).
const signalThrowCatchXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <signal id="Sig_cancel" name="order-cancelled"/>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="throw"><signalEventDefinition signalRef="Sig_cancel"/></intermediateThrowEvent>
    <intermediateCatchEvent id="catch"><signalEventDefinition signalRef="Sig_cancel"/></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="throw"/>
    <sequenceFlow id="f2" sourceRef="throw" targetRef="catch"/>
    <sequenceFlow id="f3" sourceRef="catch" targetRef="e"/>
  </process>
</definitions>`

// TestParseSignalThrowAndCatch checks that a signal throw and catch compile to their own
// node types with the resolved signal name.
func TestParseSignalThrowAndCatch(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(signalThrowCatchXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	throw := nodeByBpmnId(t, cp, "throw")
	if throw.Type != TypeSignalThrowEvent {
		t.Fatalf("throw type = %v, want SignalThrowEvent", throw.Type)
	}
	if got := throw.Type.String(); got != "SignalThrowEvent" {
		t.Errorf("SignalThrowEvent.String() = %q", got)
	}
	if got := cp.SignalThrow(throw.Detail).SignalName; got != "order-cancelled" {
		t.Errorf("throw signal name = %q, want order-cancelled", got)
	}
	catch := nodeByBpmnId(t, cp, "catch")
	if catch.Type != TypeSignalCatchEvent || catch.Type.String() != "SignalCatchEvent" {
		t.Fatalf("catch type = %v (%q), want SignalCatchEvent", catch.Type, catch.Type.String())
	}
	if got := cp.SignalCatch(catch.Detail).SignalName; got != "order-cancelled" {
		t.Errorf("catch signal name = %q, want order-cancelled", got)
	}
}

// TestParseSignalBoundary checks that a signal boundary event compiles as a
// TypeBoundaryEvent with the BoundarySignal kind and the resolved name.
func TestParseSignalBoundary(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <signal id="Sig" name="abort"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="w"/></extensionElements></serviceTask>
	    <boundaryEvent id="b" attachedToRef="work" cancelActivity="false"><signalEventDefinition signalRef="Sig"/></boundaryEvent>
	    <endEvent id="e"/>
	    <endEvent id="esc"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="esc"/>
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
	if d.Kind != BoundarySignal {
		t.Errorf("boundary kind = %v, want BoundarySignal", d.Kind)
	}
	if d.SignalName != "abort" {
		t.Errorf("boundary signal name = %q, want abort", d.SignalName)
	}
	if d.Interrupting {
		t.Errorf("boundary interrupting = true, want false (cancelActivity=false)")
	}
}

// TestParseSignalStartAndEnd checks a signal start event (a process entry point recorded in
// SignalStartEvents) and a signal end event (reusing the throw detail table).
func TestParseSignalStartAndEnd(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <signal id="Sig" name="kickoff"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"><signalEventDefinition signalRef="Sig"/></startEvent>
	    <endEvent id="e"><signalEventDefinition signalRef="Sig"/></endEvent>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := nodeByBpmnId(t, cp, "s")
	if s.Type != TypeSignalStartEvent || s.Type.String() != "SignalStartEvent" {
		t.Fatalf("start type = %v (%q), want SignalStartEvent", s.Type, s.Type.String())
	}
	starts := cp.SignalStartEvents()
	if len(starts) != 1 || starts[0].SignalName != "kickoff" || starts[0].ElementId != s.ElementId {
		t.Errorf("SignalStartEvents() = %+v, want one {kickoff, %d}", starts, s.ElementId)
	}
	if got := cp.SignalStart(s.Detail).SignalName; got != "kickoff" {
		t.Errorf("SignalStart(detail) name = %q, want kickoff", got)
	}
	e := nodeByBpmnId(t, cp, "e")
	if e.Type != TypeSignalEndEvent || e.Type.String() != "SignalEndEvent" {
		t.Fatalf("end type = %v (%q), want SignalEndEvent", e.Type, e.Type.String())
	}
	if got := cp.SignalThrow(e.Detail).SignalName; got != "kickoff" {
		t.Errorf("signal end name = %q, want kickoff", got)
	}
}

// TestParseSignalEventSubprocess checks that an event subprocess triggered by a signal
// start compiles with a BoundarySignal event-subprocess detail (ADR-0088, reusing ADR-0082).
func TestParseSignalEventSubprocess(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <signal id="Sig" name="cancel"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="w"/></extensionElements></serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	    <subProcess id="es" triggeredByEvent="true">
	      <startEvent id="es_start"><signalEventDefinition signalRef="Sig"/></startEvent>
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
	if d.Kind != BoundarySignal || d.SignalName != "cancel" {
		t.Errorf("event-sub trigger = {kind:%v name:%q}, want {BoundarySignal cancel}", d.Kind, d.SignalName)
	}
}

// TestParseSignalUnknownRef fails deploy when a signal event references an undeclared
// signal, mirroring the message-ref check.
func TestParseSignalUnknownRef(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateThrowEvent id="throw"><signalEventDefinition signalRef="Nope"/></intermediateThrowEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="throw"/>
	    <sequenceFlow id="f2" sourceRef="throw" targetRef="e"/>
	  </process>
	</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("Parse: want an error for a signal event referencing an unknown signal, got nil")
	}
}

// TestParseSignalUnknownRefEverywhere rejects an unresolvable signalRef in every signal
// position — start, catch, throw, end, boundary, and event-subprocess start — exercising
// each resolveSignal error path (ADR-0088).
func TestParseSignalUnknownRefEverywhere(t *testing.T) {
	wrap := func(body string) string {
		return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
		         xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
		  <process id="p" isExecutable="true">` + body + `</process></definitions>`
	}
	cases := map[string]string{
		"start": `<startEvent id="s"><signalEventDefinition signalRef="X"/></startEvent>
			<endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>`,
		"catch": `<startEvent id="s"/><intermediateCatchEvent id="c"><signalEventDefinition signalRef="X"/></intermediateCatchEvent>
			<endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="c"/><sequenceFlow id="f2" sourceRef="c" targetRef="e"/>`,
		"end": `<startEvent id="s"/><endEvent id="e"><signalEventDefinition signalRef="X"/></endEvent>
			<sequenceFlow id="f" sourceRef="s" targetRef="e"/>`,
		"boundary": `<startEvent id="s"/><serviceTask id="w"><extensionElements><zeebe:taskDefinition type="t"/></extensionElements></serviceTask>
			<boundaryEvent id="b" attachedToRef="w"><signalEventDefinition signalRef="X"/></boundaryEvent>
			<endEvent id="e"/><endEvent id="be"/>
			<sequenceFlow id="f1" sourceRef="s" targetRef="w"/><sequenceFlow id="f2" sourceRef="w" targetRef="e"/><sequenceFlow id="f3" sourceRef="b" targetRef="be"/>`,
		"event subprocess": `<startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
			<subProcess id="es" triggeredByEvent="true"><startEvent id="es_s"><signalEventDefinition signalRef="X"/></startEvent>
			<endEvent id="es_e"/><sequenceFlow id="ef" sourceRef="es_s" targetRef="es_e"/></subProcess>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(wrap(body))); err == nil {
				t.Fatalf("Parse: want an error for an unknown signalRef in a %s, got nil", name)
			}
		})
	}
}

// TestParseSignalDuplicateId rejects a duplicate element id at every signal position,
// exercising each signal register error path (ADR-0088). Each case has two signal
// elements of the same kind sharing an id; registering the second fails.
func TestParseSignalDuplicateId(t *testing.T) {
	wrap := func(body string) string {
		return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
		         xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
		  <signal id="Sig" name="x"/>
		  <process id="p" isExecutable="true">` + body + `</process></definitions>`
	}
	sig := `<signalEventDefinition signalRef="Sig"/>`
	cases := map[string]string{
		"start": `<startEvent id="dup">` + sig + `</startEvent><startEvent id="dup">` + sig + `</startEvent><endEvent id="e"/>`,
		"catch": `<startEvent id="s"/>
			<intermediateCatchEvent id="dup">` + sig + `</intermediateCatchEvent>
			<intermediateCatchEvent id="dup">` + sig + `</intermediateCatchEvent>`,
		"throw": `<startEvent id="s"/>
			<intermediateThrowEvent id="dup">` + sig + `</intermediateThrowEvent>
			<intermediateThrowEvent id="dup">` + sig + `</intermediateThrowEvent>`,
		"end": `<startEvent id="s"/><endEvent id="dup">` + sig + `</endEvent><endEvent id="dup">` + sig + `</endEvent>`,
		"boundary": `<startEvent id="s"/>
			<serviceTask id="w"><extensionElements><zeebe:taskDefinition type="t"/></extensionElements></serviceTask>
			<boundaryEvent id="dup" attachedToRef="w">` + sig + `</boundaryEvent>
			<boundaryEvent id="dup" attachedToRef="w">` + sig + `</boundaryEvent>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(wrap(body))); err == nil {
				t.Fatalf("Parse: want a duplicate-id error for a %s, got nil", name)
			}
		})
	}
}

// TestParseSignalNoName fails deploy when a referenced signal declares no name — there is
// nothing for a catch to subscribe on or a throw to broadcast.
func TestParseSignalNoName(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <signal id="Sig"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateThrowEvent id="throw"><signalEventDefinition signalRef="Sig"/></intermediateThrowEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="throw"/>
	    <sequenceFlow id="f2" sourceRef="throw" targetRef="e"/>
	  </process>
	</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("Parse: want an error for a signal with no name, got nil")
	}
}

// TestSignalListenersAndThrowers checks that every way a model can wait for a signal
// is listed as a listener — a root start, an intermediate catch, a boundary event and
// an event subprocess's start — and every way it can broadcast one as a thrower. The
// access rule on catalogued events (ADR-0435 §6) relies on the listener list being
// complete: an element it missed would be a listener nobody checked.
func TestSignalListenersAndThrowers(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <signal id="SigStart" name="start-it"/>
	  <signal id="SigCatch" name="catch-it"/>
	  <signal id="SigBoundary" name="bound-it"/>
	  <signal id="SigSub" name="sub-it"/>
	  <signal id="SigThrow" name="throw-it"/>
	  <signal id="SigEnd" name="end-it"/>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"><signalEventDefinition signalRef="SigStart"/></startEvent>
	    <intermediateCatchEvent id="c"><signalEventDefinition signalRef="SigCatch"/></intermediateCatchEvent>
	    <intermediateThrowEvent id="t"><signalEventDefinition signalRef="SigThrow"/></intermediateThrowEvent>
	    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="w"/></extensionElements></serviceTask>
	    <boundaryEvent id="b" attachedToRef="work"><signalEventDefinition signalRef="SigBoundary"/></boundaryEvent>
	    <endEvent id="e"><signalEventDefinition signalRef="SigEnd"/></endEvent>
	    <endEvent id="be"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="c"/>
	    <sequenceFlow id="f2" sourceRef="c" targetRef="t"/>
	    <sequenceFlow id="f3" sourceRef="t" targetRef="work"/>
	    <sequenceFlow id="f4" sourceRef="work" targetRef="e"/>
	    <sequenceFlow id="f5" sourceRef="b" targetRef="be"/>
	    <subProcess id="es" triggeredByEvent="true">
	      <startEvent id="es_start"><signalEventDefinition signalRef="SigSub"/></startEvent>
	      <endEvent id="es_end"/>
	      <sequenceFlow id="ef" sourceRef="es_start" targetRef="es_end"/>
	    </subProcess>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	collect := func(uses []SignalUse) map[string]string {
		out := map[string]string{}
		for _, u := range uses {
			out[cp.ElementBpmnId(u.ElementId)] = u.SignalName
		}
		return out
	}
	listeners := collect(cp.SignalListeners())
	for el, name := range map[string]string{"s": "start-it", "c": "catch-it", "b": "bound-it", "es_start": "sub-it"} {
		if listeners[el] != name {
			t.Errorf("listener %s = %q, want %q (all: %v)", el, listeners[el], name, listeners)
		}
	}
	if len(listeners) != 4 {
		t.Errorf("listeners = %v, want exactly the four waiting elements", listeners)
	}
	throwers := collect(cp.SignalThrowers())
	if throwers["t"] != "throw-it" || throwers["e"] != "end-it" || len(throwers) != 2 {
		t.Errorf("throwers = %v, want t:throw-it and e:end-it", throwers)
	}
}
