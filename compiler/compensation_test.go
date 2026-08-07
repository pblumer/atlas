package compiler

import (
	"strings"
	"testing"
)

// compensationBPMN is an order process whose "charge" activity is compensable: a
// compensation boundary links it (via a BPMN <association>) to the off-flow "refund"
// handler, and a compensation throw event named "cancel" compensates it (ADR-0103).
const compensationBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="charge">
      <extensionElements><zeebe:taskDefinition type="charge-card"/></extensionElements>
    </serviceTask>
    <boundaryEvent id="chargeComp" attachedToRef="charge">
      <compensateEventDefinition/>
    </boundaryEvent>
    <serviceTask id="refund" isForCompensation="true">
      <extensionElements><zeebe:taskDefinition type="refund-card"/></extensionElements>
    </serviceTask>
    <intermediateThrowEvent id="cancel">
      <compensateEventDefinition activityRef="charge"/>
    </intermediateThrowEvent>
    <endEvent id="done"/>
    <association id="a1" sourceRef="chargeComp" targetRef="refund"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="charge"/>
    <sequenceFlow id="f2" sourceRef="charge" targetRef="cancel"/>
    <sequenceFlow id="f3" sourceRef="cancel" targetRef="done"/>
  </process>
</definitions>`

// TestParseCompensationBoundaryLinksHandler: a compensation boundary compiles inert
// (BoundaryCompensation, never interrupting) and resolves its host→handler link from the
// <association>, and the throw compiles pointing at the named compensable activity.
func TestParseCompensationBoundaryLinksHandler(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(compensationBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	charge := nodeByBpmnId(t, cp, "charge")
	refund := nodeByBpmnId(t, cp, "refund")

	bevs := cp.BoundaryEvents(charge.ElementId)
	if len(bevs) != 1 {
		t.Fatalf("charge boundary events = %d, want 1", len(bevs))
	}
	be := cp.Node(bevs[0])
	if be.Type != TypeBoundaryEvent {
		t.Fatalf("boundary node type = %s, want BoundaryEvent", be.Type)
	}
	d := cp.BoundaryEvent(be.Detail)
	if d.Kind != BoundaryCompensation {
		t.Errorf("Kind = %d, want BoundaryCompensation", d.Kind)
	}
	if d.Interrupting {
		t.Error("a compensation boundary must never interrupt")
	}
	if d.CompensationHandler != refund.ElementId {
		t.Errorf("CompensationHandler = %d, want refund %d", d.CompensationHandler, refund.ElementId)
	}
	// A compensation boundary opens no outgoing flow — it is a marker, not a catch.
	if out := cp.Outgoing(be.ElementId); len(out) != 0 {
		t.Errorf("compensation boundary outgoing flows = %d, want 0", len(out))
	}

	// The handler activity is a normal service task, off the sequence flow.
	if refund.Type != TypeServiceTask {
		t.Errorf("refund type = %s, want ServiceTask", refund.Type)
	}
	if out := cp.Outgoing(refund.ElementId); len(out) != 0 {
		t.Errorf("handler outgoing flows = %d, want 0 (off the normal flow)", len(out))
	}

	// The throw event carries the resolved activity ref.
	cancel := nodeByBpmnId(t, cp, "cancel")
	if cancel.Type != TypeCompensationThrowEvent {
		t.Fatalf("cancel type = %s, want CompensationThrowEvent", cancel.Type)
	}
	if got := cp.CompensationThrow(cancel.Detail).ActivityRef; got != charge.ElementId {
		t.Errorf("throw ActivityRef = %d, want charge %d", got, charge.ElementId)
	}
}

// TestValidateCompensationHandlerReachable: the off-flow handler must NOT raise a
// reachability warning — it is reached via its compensation boundary, not a token (ADR-0103).
func TestValidateCompensationHandlerReachable(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(compensationBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ps := Validate(cp)
	if hasProblem(ps, RuleReachability, SeverityWarning, "refund") {
		t.Errorf("compensation handler flagged unreachable: %+v", ps)
	}
	if len(ps) != 0 {
		t.Errorf("Validate = %+v, want no problems", ps)
	}
}

// TestParseCompensationThrowWholeScope: a compensation throw with no activityRef
// compensates the whole scope, recorded as ActivityRef == -1 (ADR-0103).
func TestParseCompensationThrowWholeScope(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="do"/></extensionElements></serviceTask>
    <boundaryEvent id="ac" attachedToRef="a"><compensateEventDefinition/></boundaryEvent>
    <serviceTask id="undo" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="undo"/></extensionElements></serviceTask>
    <intermediateThrowEvent id="comp"><compensateEventDefinition/></intermediateThrowEvent>
    <endEvent id="e"/>
    <association id="a1" sourceRef="ac" targetRef="undo"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="a" targetRef="comp"/>
    <sequenceFlow id="f3" sourceRef="comp" targetRef="e"/>
  </process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	comp := nodeByBpmnId(t, cp, "comp")
	if comp.Type != TypeCompensationThrowEvent {
		t.Fatalf("comp type = %s, want CompensationThrowEvent", comp.Type)
	}
	if got := cp.CompensationThrow(comp.Detail).ActivityRef; got != -1 {
		t.Errorf("whole-scope throw ActivityRef = %d, want -1", got)
	}
}

// TestParseCompensationEndEvent: an end event bearing a <compensateEventDefinition>
// compiles as a compensation end event, reusing the throw detail table (ADR-0103).
func TestParseCompensationEndEvent(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="do"/></extensionElements></serviceTask>
    <boundaryEvent id="ac" attachedToRef="a"><compensateEventDefinition/></boundaryEvent>
    <serviceTask id="undo" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="undo"/></extensionElements></serviceTask>
    <endEvent id="rollback"><compensateEventDefinition/></endEvent>
    <association id="a1" sourceRef="ac" targetRef="undo"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="a" targetRef="rollback"/>
  </process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rollback := nodeByBpmnId(t, cp, "rollback")
	if rollback.Type != TypeCompensationEndEvent {
		t.Fatalf("rollback type = %s, want CompensationEndEvent", rollback.Type)
	}
	if got := cp.CompensationThrow(rollback.Detail).ActivityRef; got != -1 {
		t.Errorf("compensation end ActivityRef = %d, want -1", got)
	}
}

// TestParseCompensationBoundaryWithoutAssociationRejected: a compensation boundary with
// no <association> has no handler to run, which is a deploy error (ADR-0103).
func TestParseCompensationBoundaryWithoutAssociationRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="do"/></extensionElements></serviceTask>
    <boundaryEvent id="ac" attachedToRef="a"><compensateEventDefinition/></boundaryEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="a" targetRef="e"/>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for a compensation boundary with no association")
	}
	if !strings.Contains(err.Error(), "association") {
		t.Errorf("error = %v, want it to mention the missing association", err)
	}
}

// TestParseCompensationThrowUnknownActivityRejected: a compensation throw whose
// activityRef names no element is a deploy error (ADR-0103).
func TestParseCompensationThrowUnknownActivityRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="do"/></extensionElements></serviceTask>
    <boundaryEvent id="ac" attachedToRef="a"><compensateEventDefinition/></boundaryEvent>
    <serviceTask id="undo" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="undo"/></extensionElements></serviceTask>
    <intermediateThrowEvent id="comp"><compensateEventDefinition activityRef="ghost"/></intermediateThrowEvent>
    <endEvent id="e"/>
    <association id="a1" sourceRef="ac" targetRef="undo"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="a" targetRef="comp"/>
    <sequenceFlow id="f3" sourceRef="comp" targetRef="e"/>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for a compensation throw with an unknown activityRef")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown activity", err)
	}
}
