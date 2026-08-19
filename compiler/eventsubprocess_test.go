package compiler

import (
	"strings"
	"testing"
)

// A process with a message-triggered, interrupting event subprocess at the root: the
// main flow start → work → end, plus a <subProcess triggeredByEvent="true"> whose start
// event carries a messageEventDefinition and isInterrupting="true" (ADR-0082 Phase 1).
const eventSubMessageBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_cancel" name="cancel">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="work">
      <bpmn:extensionElements><zeebe:taskDefinition type="w"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/>
    <bpmn:sequenceFlow id="f2" sourceRef="work" targetRef="end"/>
    <bpmn:subProcess id="handler" triggeredByEvent="true">
      <bpmn:startEvent id="es" isInterrupting="true">
        <bpmn:messageEventDefinition messageRef="Msg_cancel"/>
      </bpmn:startEvent>
      <bpmn:endEvent id="he"/>
      <bpmn:sequenceFlow id="hf" sourceRef="es" targetRef="he"/>
    </bpmn:subProcess>
  </bpmn:process>
</bpmn:definitions>`

// TestParseEventSubprocessMessage checks the Phase-1 compiler contract for a
// message-triggered interrupting event subprocess (ADR-0082): the container compiles
// as an event subprocess carrying its trigger, it is listed under its parent scope, and
// its inner message start is NOT a process entry point.
func TestParseEventSubprocessMessage(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(eventSubMessageBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	handler := nodeByBpmnId(t, cp, "handler")
	if handler.Type != TypeSubProcess {
		t.Fatalf("handler type = %v, want SubProcess", handler.Type)
	}
	if !cp.IsEventSubProcess(handler.ElementId) {
		t.Fatalf("handler is not recognised as an event subprocess (EventSub=%d)", handler.EventSub)
	}
	d := cp.EventSubProcess(handler.EventSub)
	if !d.Interrupting {
		t.Errorf("Interrupting = false, want true")
	}
	if d.Kind != BoundaryMessage {
		t.Errorf("Kind = %v, want message", d.Kind)
	}
	if d.MessageName != "cancel" {
		t.Errorf("MessageName = %q, want cancel", d.MessageName)
	}
	if d.CorrelationKey == nil {
		t.Errorf("CorrelationKey = nil, want a compiled expression")
	}
	if d.StartNode != nodeByBpmnId(t, cp, "es").ElementId {
		t.Errorf("StartNode = %d, want the es node", d.StartNode)
	}
	// Listed under the root scope's event subprocesses.
	roots := cp.RootEventSubprocesses()
	if len(roots) != 1 || roots[0] != handler.ElementId {
		t.Errorf("RootEventSubprocesses = %v, want [handler]", roots)
	}
	// The inner message start is NOT a process entry point: it neither instantiates the
	// process (MessageStartEvents) nor is a root start (StartEvents).
	if ms := cp.MessageStartEvents(); len(ms) != 0 {
		t.Errorf("MessageStartEvents = %v, want none (an event-sub start does not start the process)", ms)
	}
	starts := cp.StartEvents()
	if len(starts) != 1 || starts[0] != nodeByBpmnId(t, cp, "start").ElementId {
		t.Errorf("StartEvents = %v, want just the root start", starts)
	}
}

// TestParseEventSubprocessTimerNonInterrupting checks a timer-triggered,
// non-interrupting event subprocess: interrupting defaults off via isInterrupting="false",
// and the trigger is a timer schedule (ADR-0082).
func TestParseEventSubprocessTimerNonInterrupting(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="work">
      <bpmn:extensionElements><zeebe:taskDefinition type="w"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/>
    <bpmn:sequenceFlow id="f2" sourceRef="work" targetRef="end"/>
    <bpmn:subProcess id="reminder" triggeredByEvent="true">
      <bpmn:startEvent id="rs" isInterrupting="false">
        <bpmn:timerEventDefinition><bpmn:timeDuration>PT1H</bpmn:timeDuration></bpmn:timerEventDefinition>
      </bpmn:startEvent>
      <bpmn:endEvent id="re"/>
      <bpmn:sequenceFlow id="rf" sourceRef="rs" targetRef="re"/>
    </bpmn:subProcess>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	handler := nodeByBpmnId(t, cp, "reminder")
	if !cp.IsEventSubProcess(handler.ElementId) {
		t.Fatalf("reminder is not an event subprocess")
	}
	d := cp.EventSubProcess(handler.EventSub)
	if d.Interrupting {
		t.Errorf("Interrupting = true, want false (isInterrupting=false)")
	}
	if d.Kind != BoundaryTimer {
		t.Errorf("Kind = %v, want timer", d.Kind)
	}
	// A timer start nested in an event subprocess is not a process-level timer start.
	if ts := cp.TimerStartEvents(); len(ts) != 0 {
		t.Errorf("TimerStartEvents = %v, want none", ts)
	}
}

// TestParseEventSubprocessNested checks that an event subprocess inside an embedded
// subprocess is grouped under that subprocess's scope, not the root (ADR-0082).
func TestParseEventSubprocessNested(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_c" name="cancel"><bpmn:extensionElements><zeebe:subscription correlationKey="= id"/></bpmn:extensionElements></bpmn:message>
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:subProcess id="sub">
      <bpmn:startEvent id="is"/>
      <bpmn:endEvent id="ie"/>
      <bpmn:sequenceFlow id="if" sourceRef="is" targetRef="ie"/>
      <bpmn:subProcess id="handler" triggeredByEvent="true">
        <bpmn:startEvent id="es"><bpmn:messageEventDefinition messageRef="Msg_c"/></bpmn:startEvent>
        <bpmn:endEvent id="he"/>
        <bpmn:sequenceFlow id="hf" sourceRef="es" targetRef="he"/>
      </bpmn:subProcess>
    </bpmn:subProcess>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="sub"/>
    <bpmn:sequenceFlow id="f2" sourceRef="sub" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sub := nodeByBpmnId(t, cp, "sub")
	handler := nodeByBpmnId(t, cp, "handler")
	if cp.IsEventSubProcess(sub.ElementId) {
		t.Errorf("the plain subprocess must not be an event subprocess")
	}
	if !cp.IsEventSubProcess(handler.ElementId) {
		t.Fatalf("handler is not an event subprocess")
	}
	// Grouped under the enclosing subprocess, not the root.
	inSub := cp.EventSubprocesses(sub.ElementId)
	if len(inSub) != 1 || inSub[0] != handler.ElementId {
		t.Errorf("EventSubprocesses(sub) = %v, want [handler]", inSub)
	}
	if roots := cp.RootEventSubprocesses(); len(roots) != 0 {
		t.Errorf("RootEventSubprocesses = %v, want none (the event sub is nested)", roots)
	}
	// interrupting default (absent isInterrupting) is true.
	if !cp.EventSubProcess(handler.EventSub).Interrupting {
		t.Errorf("absent isInterrupting should default to interrupting=true")
	}
}

// TestParseEventSubprocessRequiresTrigger fails deploy when a triggeredByEvent
// subprocess's start event carries no event definition — it has no way to be triggered.
func TestParseEventSubprocessRequiresTrigger(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
    <bpmn:subProcess id="handler" triggeredByEvent="true">
      <bpmn:startEvent id="es"/>
      <bpmn:endEvent id="he"/>
      <bpmn:sequenceFlow id="hf" sourceRef="es" targetRef="he"/>
    </bpmn:subProcess>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("Parse: want an error for a triggeredByEvent subprocess with a none start, got nil")
	}
}
