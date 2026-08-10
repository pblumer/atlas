package compiler

import (
	"strings"
	"testing"
)

// eventGatewayBPMN is the request/timeout pattern: after a token reaches the event-based
// gateway "wait", it arms a message catch ("reply") and a timer catch ("timeout") at once;
// whichever fires first wins (ADR-0109).
const eventGatewayBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_reply" name="reply">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= id"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="req" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:eventBasedGateway id="wait"/>
    <bpmn:intermediateCatchEvent id="reply">
      <bpmn:messageEventDefinition id="ced" messageRef="Msg_reply"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:intermediateCatchEvent id="timeout">
      <bpmn:timerEventDefinition><bpmn:timeDuration>PT5M</bpmn:timeDuration></bpmn:timerEventDefinition>
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="gotReply"/>
    <bpmn:endEvent id="timedOut"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="wait"/>
    <bpmn:sequenceFlow id="f2" sourceRef="wait" targetRef="reply"/>
    <bpmn:sequenceFlow id="f3" sourceRef="wait" targetRef="timeout"/>
    <bpmn:sequenceFlow id="f4" sourceRef="reply" targetRef="gotReply"/>
    <bpmn:sequenceFlow id="f5" sourceRef="timeout" targetRef="timedOut"/>
  </bpmn:process>
</bpmn:definitions>`

// TestParseEventBasedGateway: an <eventBasedGateway> compiles to TypeEventBasedGateway, its
// outgoing flows lead to the two catch events, and the model validates clean (ADR-0109).
func TestParseEventBasedGateway(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(eventGatewayBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wait := nodeByBpmnId(t, cp, "wait")
	if wait.Type != TypeEventBasedGateway || wait.Type.String() != "EventBasedGateway" {
		t.Fatalf("wait type = %s, want EventBasedGateway", wait.Type)
	}
	// Both outgoing branches are catch events.
	targets := map[BpmnType]bool{}
	for _, fid := range cp.Outgoing(wait.ElementId) {
		targets[cp.Node(cp.Flow(fid).Target).Type] = true
	}
	if !targets[TypeMessageCatchEvent] || !targets[TypeTimerCatchEvent] {
		t.Errorf("event-gateway targets = %v, want a message and a timer catch", targets)
	}
	if ps := Validate(cp); len(ps) != 0 {
		t.Errorf("Validate = %+v, want no problems", ps)
	}
}

// TestParseEventGatewayNonCatchTargetRejected: an event-based gateway whose outgoing flow
// leads to a non-catch element (here a pass-through task) is a deploy error — a deferred
// choice can only race catch events (ADR-0109).
func TestParseEventGatewayNonCatchTargetRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="start"/>
    <eventBasedGateway id="wait"/>
    <task id="doStuff"/>
    <intermediateCatchEvent id="timeout">
      <timerEventDefinition><timeDuration>PT1M</timeDuration></timerEventDefinition>
    </intermediateCatchEvent>
    <endEvent id="e1"/>
    <endEvent id="e2"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="doStuff"/>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="timeout"/>
    <sequenceFlow id="f4" sourceRef="doStuff" targetRef="e1"/>
    <sequenceFlow id="f5" sourceRef="timeout" targetRef="e2"/>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for an event gateway targeting a non-catch element")
	}
	if !strings.Contains(err.Error(), "catch event") {
		t.Errorf("error = %v, want it to mention a catch event", err)
	}
}

// TestValidateEventGatewayTargetRule checks the structured Problem: the rule slug and
// severity for a non-catch target, via the Builder (which bypasses the deploy gate) so the
// Problem is observable rather than folded into a fatal ValidationError (ADR-0109).
func TestValidateEventGatewayTargetRule(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	wait := b.AddEventBasedGateway()
	task := b.AddTask() // a non-catch target
	catch := b.AddSignalCatchEvent("go")
	e1 := b.AddEndEvent()
	e2 := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, task)
	b.Connect(wait, catch)
	b.Connect(task, e1)
	b.Connect(catch, e2)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ps := Validate(cp)
	if !hasProblem(ps, RuleEventGatewayTarget, SeverityError, "") {
		t.Errorf("Validate = %+v, want an event-gateway.invalid-target error", ps)
	}
	// The signal-catch branch is a valid target and raises no target error.
	if len(ps) != 1 {
		t.Errorf("Validate = %+v, want exactly the one invalid-target error", ps)
	}
}
