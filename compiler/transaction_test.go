package compiler

import (
	"strings"
	"testing"
)

// transactionBPMN is a booking process whose "reserve" step runs inside a <transaction>.
// The transaction's "reserve" activity is compensable (a compensation boundary links it to
// the off-flow "unreserve" handler); a cancel end event inside the transaction rolls it
// back, and a cancel boundary on the transaction routes the cancellation to "notify"
// (ADR-0105).
const transactionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="booking" isExecutable="true">
    <startEvent id="start"/>
    <transaction id="book">
      <startEvent id="ts"/>
      <serviceTask id="reserve"><extensionElements><zeebe:taskDefinition type="reserve"/></extensionElements></serviceTask>
      <boundaryEvent id="reserveComp" attachedToRef="reserve"><compensateEventDefinition/></boundaryEvent>
      <serviceTask id="unreserve" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="unreserve"/></extensionElements></serviceTask>
      <endEvent id="cancelled"><cancelEventDefinition/></endEvent>
      <association id="ca1" sourceRef="reserveComp" targetRef="unreserve"/>
      <sequenceFlow id="tf1" sourceRef="ts" targetRef="reserve"/>
      <sequenceFlow id="tf2" sourceRef="reserve" targetRef="cancelled"/>
    </transaction>
    <boundaryEvent id="bookCancelled" attachedToRef="book"><cancelEventDefinition/></boundaryEvent>
    <serviceTask id="notify"><extensionElements><zeebe:taskDefinition type="notify"/></extensionElements></serviceTask>
    <endEvent id="done"/>
    <endEvent id="handled"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="book"/>
    <sequenceFlow id="f2" sourceRef="book" targetRef="done"/>
    <sequenceFlow id="f3" sourceRef="bookCancelled" targetRef="notify"/>
    <sequenceFlow id="f4" sourceRef="notify" targetRef="handled"/>
  </process>
</definitions>`

// TestParseTransactionCancelBoundaryAndEnd: a <transaction> compiles to an IsTransaction
// subprocess scope; its cancel end event is a TypeCancelEndEvent; and a cancel boundary on
// the transaction is a BoundaryCancel that is always interrupting and hosts on the
// transaction (ADR-0105).
func TestParseTransactionCancelBoundaryAndEnd(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(transactionBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	book := nodeByBpmnId(t, cp, "book")
	if book.Type != TypeSubProcess {
		t.Fatalf("book type = %s, want SubProcess (a transaction is a subprocess variant)", book.Type)
	}
	if !cp.IsTransaction(book.ElementId) {
		t.Errorf("book is not marked IsTransaction")
	}

	// A plain embedded subprocess is not a transaction.
	if cp.IsTransaction(nodeByBpmnId(t, cp, "reserve").ElementId) {
		t.Errorf("reserve wrongly marked IsTransaction")
	}

	// The cancel end event inside the transaction.
	cancelled := nodeByBpmnId(t, cp, "cancelled")
	if cancelled.Type != TypeCancelEndEvent || cancelled.Type.String() != "CancelEndEvent" {
		t.Fatalf("cancelled type = %s, want CancelEndEvent", cancelled.Type)
	}
	if cancelled.FlowScope != book.ElementId {
		t.Errorf("cancel end FlowScope = %d, want the transaction %d", cancelled.FlowScope, book.ElementId)
	}

	// The cancel boundary on the transaction: BoundaryCancel, always interrupting, hosted on book.
	bevs := cp.BoundaryEvents(book.ElementId)
	if len(bevs) != 1 {
		t.Fatalf("book boundary events = %d, want 1", len(bevs))
	}
	d := cp.BoundaryEvent(cp.Node(bevs[0]).Detail)
	if d.Kind != BoundaryCancel {
		t.Errorf("Kind = %d, want BoundaryCancel", d.Kind)
	}
	if !d.Interrupting {
		t.Error("a cancel boundary must always interrupt")
	}
	if d.HostNode != book.ElementId {
		t.Errorf("cancel boundary HostNode = %d, want book %d", d.HostNode, book.ElementId)
	}

	// The whole model validates cleanly (the cancel boundary's recovery flow makes notify
	// reachable; the compensation handler is reached via its boundary).
	if ps := Validate(cp); len(ps) != 0 {
		t.Errorf("Validate = %+v, want no problems", ps)
	}
}

// TestParseCancelEndOutsideTransactionRejected: a cancel end event that is not directly
// inside a transaction is a deploy error — BPMN allows it only within a transaction
// subprocess (ADR-0105).
func TestParseCancelEndOutsideTransactionRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="c"><cancelEventDefinition/></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="c"/>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for a cancel end event outside a transaction")
	}
	if !strings.Contains(err.Error(), "transaction") {
		t.Errorf("error = %v, want it to mention a transaction", err)
	}
}

// TestParseCancelEndInPlainSubProcessRejected: a cancel end event nested in a plain
// (non-transaction) subprocess is still a deploy error — the enclosing scope must be a
// transaction (ADR-0105).
func TestParseCancelEndInPlainSubProcessRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <subProcess id="sub">
      <startEvent id="ss"/>
      <endEvent id="c"><cancelEventDefinition/></endEvent>
      <sequenceFlow id="sf1" sourceRef="ss" targetRef="c"/>
    </subProcess>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
    <sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for a cancel end event in a plain subprocess")
	}
	if !strings.Contains(err.Error(), "transaction") {
		t.Errorf("error = %v, want it to mention a transaction", err)
	}
}

// TestParseCancelBoundaryOnNonTransactionRejected: a cancel boundary may attach only to a
// transaction; on a plain activity it is a deploy error (ADR-0105).
func TestParseCancelBoundaryOnNonTransactionRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="do"/></extensionElements></serviceTask>
    <boundaryEvent id="b" attachedToRef="a"><cancelEventDefinition/></boundaryEvent>
    <endEvent id="e"/>
    <endEvent id="e2"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="a" targetRef="e"/>
    <sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for a cancel boundary on a non-transaction activity")
	}
	if !strings.Contains(err.Error(), "transaction") {
		t.Errorf("error = %v, want it to mention a transaction", err)
	}
}

// TestValidateTransactionNoCancelBoundaryWarns: a transaction with a cancel end but no
// cancel boundary is deployable but raises a warning — the cancellation tears the
// transaction down with no recovery route (ADR-0105).
func TestValidateTransactionNoCancelBoundaryWarns(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="start"/>
    <transaction id="book">
      <startEvent id="ts"/>
      <serviceTask id="reserve"><extensionElements><zeebe:taskDefinition type="reserve"/></extensionElements></serviceTask>
      <endEvent id="cancelled"><cancelEventDefinition/></endEvent>
      <sequenceFlow id="tf1" sourceRef="ts" targetRef="reserve"/>
      <sequenceFlow id="tf2" sourceRef="reserve" targetRef="cancelled"/>
    </transaction>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="book"/>
    <sequenceFlow id="f2" sourceRef="book" targetRef="done"/>
  </process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ps := Validate(cp)
	if !hasProblem(ps, RuleTransactionNoCancelBoundary, SeverityWarning, "book") {
		t.Errorf("Validate = %+v, want a no-cancel-boundary warning on book", ps)
	}
}

// TestParseTransactionModelerRoundTrip parses a transaction model in the shape stock
// bpmn-js exports — namespaced (bpmn:) elements, a <transaction> with a cancel end event
// and a cancel boundary, incoming/outgoing children — to confirm a hand-drawn model deploys
// (ADR-0105).
func TestParseTransactionModelerRoundTrip(t *testing.T) {
	const bpmn = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Defs_1">
  <bpmn:process id="booking" isExecutable="true">
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:transaction id="book">
      <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing>
      <bpmn:startEvent id="ts"><bpmn:outgoing>tf1</bpmn:outgoing></bpmn:startEvent>
      <bpmn:serviceTask id="reserve">
        <bpmn:extensionElements><zeebe:taskDefinition type="reserve"/></bpmn:extensionElements>
        <bpmn:incoming>tf1</bpmn:incoming><bpmn:outgoing>tf2</bpmn:outgoing>
      </bpmn:serviceTask>
      <bpmn:boundaryEvent id="reserveComp" attachedToRef="reserve">
        <bpmn:compensateEventDefinition/>
      </bpmn:boundaryEvent>
      <bpmn:serviceTask id="unreserve" isForCompensation="true">
        <bpmn:extensionElements><zeebe:taskDefinition type="unreserve"/></bpmn:extensionElements>
      </bpmn:serviceTask>
      <bpmn:endEvent id="cancelled"><bpmn:incoming>tf2</bpmn:incoming><bpmn:cancelEventDefinition/></bpmn:endEvent>
      <bpmn:association id="ca1" associationDirection="One" sourceRef="reserveComp" targetRef="unreserve"/>
      <bpmn:sequenceFlow id="tf1" sourceRef="ts" targetRef="reserve"/>
      <bpmn:sequenceFlow id="tf2" sourceRef="reserve" targetRef="cancelled"/>
    </bpmn:transaction>
    <bpmn:boundaryEvent id="bookCancelled" attachedToRef="book">
      <bpmn:outgoing>f3</bpmn:outgoing><bpmn:cancelEventDefinition/>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="done"><bpmn:incoming>f2</bpmn:incoming></bpmn:endEvent>
    <bpmn:endEvent id="handled"><bpmn:incoming>f3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="book"/>
    <bpmn:sequenceFlow id="f2" sourceRef="book" targetRef="done"/>
    <bpmn:sequenceFlow id="f3" sourceRef="bookCancelled" targetRef="handled"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	book := nodeByBpmnId(t, cp, "book")
	if !cp.IsTransaction(book.ElementId) {
		t.Errorf("book not marked IsTransaction from the namespaced <transaction>")
	}
	if nodeByBpmnId(t, cp, "cancelled").Type != TypeCancelEndEvent {
		t.Errorf("cancelled did not compile to a cancel end event")
	}
	d := cp.BoundaryEvent(cp.Node(cp.BoundaryEvents(book.ElementId)[0]).Detail)
	if d.Kind != BoundaryCancel || !d.Interrupting {
		t.Errorf("cancel boundary = %+v, want BoundaryCancel + interrupting", d)
	}
	// The compensation handler inside the transaction still resolves its boundary link.
	rc := cp.BoundaryEvent(cp.Node(cp.BoundaryEvents(nodeByBpmnId(t, cp, "reserve").ElementId)[0]).Detail)
	if rc.CompensationHandler != nodeByBpmnId(t, cp, "unreserve").ElementId {
		t.Errorf("compensation boundary inside the transaction did not resolve its handler")
	}
	if ps := Validate(cp); len(ps) != 0 {
		t.Errorf("Validate = %+v, want no problems", ps)
	}
}
