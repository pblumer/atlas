package compiler

import (
	"strings"
	"testing"
)

// TestParseReceiveTask checks that a receive task compiles to a TypeReceiveTask carrying the
// referenced message's name and correlation-key expression, and that it is an activity — a
// boundary event may attach to it (ADR-0102).
func TestParseReceiveTask(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <message id="Msg_reply" name="reply">
	    <extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements>
	  </message>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <receiveTask id="await" messageRef="Msg_reply"/>
	    <boundaryEvent id="timeout" attachedToRef="await" cancelActivity="true">
	      <timerEventDefinition><timeDuration>PT1H</timeDuration></timerEventDefinition>
	    </boundaryEvent>
	    <endEvent id="e"/>
	    <endEvent id="late"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="await"/>
	    <sequenceFlow id="f2" sourceRef="await" targetRef="e"/>
	    <sequenceFlow id="f3" sourceRef="timeout" targetRef="late"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rt := nodeByBpmnId(t, cp, "await")
	if rt.Type != TypeReceiveTask || rt.Type.String() != "ReceiveTask" {
		t.Fatalf("receive task type = %v (%q), want ReceiveTask", rt.Type, rt.Type.String())
	}
	d := cp.ReceiveTask(rt.Detail)
	if d.MessageName != "reply" {
		t.Errorf("receive task message name = %q, want reply", d.MessageName)
	}
	if d.CorrelationKey == nil {
		t.Errorf("receive task correlation key = nil, want a compiled expression")
	}
	// The boundary attached to the receive task — proof it is treated as an activity.
	if b := nodeByBpmnId(t, cp, "timeout"); b.Type != TypeBoundaryEvent {
		t.Fatalf("boundary type = %v, want BoundaryEvent (a boundary must attach to the receive task)", b.Type)
	}
	if problems := Validate(cp); HasErrors(problems) {
		t.Errorf("Validate found errors on a receive task with a boundary: %+v", problems)
	}
}

// TestParseReceiveTaskUnknownMessage rejects a receive task whose messageRef names no
// declared message, mirroring the message-catch check (ADR-0102).
func TestParseReceiveTaskUnknownMessage(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <receiveTask id="await" messageRef="Nope"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="await"/>
	    <sequenceFlow id="f2" sourceRef="await" targetRef="e"/>
	  </process>
	</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("Parse: want an error for a receive task referencing an unknown message, got nil")
	}
}
