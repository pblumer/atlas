package compiler

import (
	"strings"
	"testing"
)

// stornoTransactionBPMN is the cancellation shape a long-running order needs: the work runs
// inside a <transaction>, and an interrupting message event subprocess *inside* that
// transaction ends in a cancel end event. A customer's cancellation can arrive at any point
// of the work; the event subprocess is where a model says so, and the cancel end hands the
// rollback to the transaction (ADR-draft-scope-aware-messaging-and-compensation).
const stornoTransactionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="mStorno" name="storno"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <transaction id="tx">
      <startEvent id="ts"/>
      <serviceTask id="reserve"><extensionElements><zeebe:taskDefinition type="reserve"/></extensionElements></serviceTask>
      <boundaryEvent id="reserveComp" attachedToRef="reserve"><compensateEventDefinition/></boundaryEvent>
      <serviceTask id="unreserve" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="unreserve"/></extensionElements></serviceTask>
      <association id="a1" sourceRef="reserveComp" targetRef="unreserve"/>
      <endEvent id="te"/>
      <sequenceFlow id="tf1" sourceRef="ts" targetRef="reserve"/>
      <sequenceFlow id="tf2" sourceRef="reserve" targetRef="te"/>
      <subProcess id="storno" triggeredByEvent="true">
        <startEvent id="stornoStart" isInterrupting="true"><messageEventDefinition messageRef="mStorno"/></startEvent>
        <endEvent id="stornoCancel"><cancelEventDefinition/></endEvent>
        <sequenceFlow id="sf1" sourceRef="stornoStart" targetRef="stornoCancel"/>
      </subProcess>
    </transaction>
    <boundaryEvent id="txCancelled" attachedToRef="tx"><cancelEventDefinition/></boundaryEvent>
    <endEvent id="done"/>
    <endEvent id="cancelled"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="tx"/>
    <sequenceFlow id="f2" sourceRef="tx" targetRef="done"/>
    <sequenceFlow id="f3" sourceRef="txCancelled" targetRef="cancelled"/>
  </process>
</definitions>`

// TestParseCancelEndInEventSubprocessOfTransactionAccepted: a cancel end event in an event
// subprocess that sits directly inside a transaction is within that transaction, as BPMN
// requires — so it deploys, and the model validates without a problem.
func TestParseCancelEndInEventSubprocessOfTransactionAccepted(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(stornoTransactionBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cancelEnd := nodeByBpmnId(t, cp, "stornoCancel")
	if cancelEnd.Type != TypeCancelEndEvent {
		t.Fatalf("stornoCancel type = %s, want CancelEndEvent", cancelEnd.Type)
	}
	handler := nodeByBpmnId(t, cp, "storno")
	if cancelEnd.FlowScope != handler.ElementId || !cp.IsEventSubProcess(handler.ElementId) {
		t.Fatalf("stornoCancel is not scoped by the event subprocess")
	}
	if ps := Validate(cp); len(ps) != 0 {
		t.Errorf("Validate = %+v, want no problems", ps)
	}
}

// TestParseCancelEndInEventSubprocessAtRootRejected: an event subprocess is only "inside a
// transaction" when its own scope is the transaction. At the process root the cancel end
// has no transaction to cancel and stays a deploy error.
func TestParseCancelEndInEventSubprocessAtRootRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m" name="storno"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="w"><extensionElements><zeebe:taskDefinition type="w"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="w"/>
    <sequenceFlow id="f2" sourceRef="w" targetRef="e"/>
    <subProcess id="es" triggeredByEvent="true">
      <startEvent id="ess"><messageEventDefinition messageRef="m"/></startEvent>
      <endEvent id="c"><cancelEventDefinition/></endEvent>
      <sequenceFlow id="ef1" sourceRef="ess" targetRef="c"/>
    </subProcess>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for a cancel end event in an event subprocess outside a transaction")
	}
	if !strings.Contains(err.Error(), "transaction") {
		t.Errorf("error = %v, want it to mention a transaction", err)
	}
}

// TestParseCancelEndInEventSubprocessOfPlainSubprocessRejected: the event subprocess must
// sit directly in the transaction. One whose scope is a plain subprocess — even a plain
// subprocess inside a transaction — is not, because cancelling would have to tear through
// a scope the model never said belongs to the transaction's rollback.
func TestParseCancelEndInEventSubprocessOfPlainSubprocessRejected(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m" name="storno"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <transaction id="tx">
      <startEvent id="ts"/>
      <subProcess id="inner">
        <startEvent id="is"/>
        <endEvent id="ie"/>
        <sequenceFlow id="if1" sourceRef="is" targetRef="ie"/>
        <subProcess id="es" triggeredByEvent="true">
          <startEvent id="ess"><messageEventDefinition messageRef="m"/></startEvent>
          <endEvent id="c"><cancelEventDefinition/></endEvent>
          <sequenceFlow id="ef1" sourceRef="ess" targetRef="c"/>
        </subProcess>
      </subProcess>
      <endEvent id="te"/>
      <sequenceFlow id="tf1" sourceRef="ts" targetRef="inner"/>
      <sequenceFlow id="tf2" sourceRef="inner" targetRef="te"/>
    </transaction>
    <boundaryEvent id="txc" attachedToRef="tx"><cancelEventDefinition/></boundaryEvent>
    <endEvent id="e"/>
    <endEvent id="ec"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="tx"/>
    <sequenceFlow id="f2" sourceRef="tx" targetRef="e"/>
    <sequenceFlow id="f3" sourceRef="txc" targetRef="ec"/>
  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(bpmn))
	if err == nil {
		t.Fatal("expected a compile error for a cancel end event two scopes below the transaction")
	}
}

// TestValidateTransactionWithEventSubCancelEndNoBoundaryWarns: a cancel end inside the
// transaction's event subprocess counts as the transaction's cancel end, so a transaction
// without a cancel boundary gets the same warning a direct cancel end earns.
func TestValidateTransactionWithEventSubCancelEndNoBoundaryWarns(t *testing.T) {
	bpmn := strings.Replace(stornoTransactionBPMN,
		`<boundaryEvent id="txCancelled" attachedToRef="tx"><cancelEventDefinition/></boundaryEvent>`, "", 1)
	bpmn = strings.Replace(bpmn, `<sequenceFlow id="f3" sourceRef="txCancelled" targetRef="cancelled"/>`, "", 1)
	bpmn = strings.Replace(bpmn, `<endEvent id="cancelled"/>`, "", 1)
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ps := Validate(cp); !hasProblem(ps, RuleTransactionNoCancelBoundary, SeverityWarning, "tx") {
		t.Errorf("Validate = %+v, want a no-cancel-boundary warning on tx", ps)
	}
}

// TestParseCompensationWaitForCompletion: BPMN's waitForCompletion defaults to true — a
// compensation throw or end event waits for the handlers it started before it goes on.
// Only an explicit "false" keeps the fire-and-continue behaviour.
func TestParseCompensationWaitForCompletion(t *testing.T) {
	const bpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="charge"><extensionElements><zeebe:taskDefinition type="charge"/></extensionElements></serviceTask>
    <boundaryEvent id="cb" attachedToRef="charge"><compensateEventDefinition/></boundaryEvent>
    <serviceTask id="refund" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="refund"/></extensionElements></serviceTask>
    <association id="a" sourceRef="cb" targetRef="refund"/>
    <intermediateThrowEvent id="waits"><compensateEventDefinition activityRef="charge"/></intermediateThrowEvent>
    <intermediateThrowEvent id="explicit"><compensateEventDefinition waitForCompletion="true"/></intermediateThrowEvent>
    <intermediateThrowEvent id="continues"><compensateEventDefinition waitForCompletion="false"/></intermediateThrowEvent>
    <endEvent id="endWaits"><compensateEventDefinition/></endEvent>
    <sequenceFlow id="f1" sourceRef="s" targetRef="charge"/>
    <sequenceFlow id="f2" sourceRef="charge" targetRef="waits"/>
    <sequenceFlow id="f3" sourceRef="waits" targetRef="explicit"/>
    <sequenceFlow id="f4" sourceRef="explicit" targetRef="continues"/>
    <sequenceFlow id="f5" sourceRef="continues" targetRef="endWaits"/>
  </process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for id, want := range map[string]bool{"waits": true, "explicit": true, "continues": false, "endWaits": true} {
		n := nodeByBpmnId(t, cp, id)
		if got := cp.CompensationThrow(n.Detail).WaitForCompletion; got != want {
			t.Errorf("%s WaitForCompletion = %v, want %v", id, got, want)
		}
	}
	if !cp.Node(nodeByBpmnId(t, cp, "refund").ElementId).CompensationHandler {
		t.Error("refund is not marked as a compensation handler")
	}
	if cp.Node(nodeByBpmnId(t, cp, "charge").ElementId).CompensationHandler {
		t.Error("charge wrongly marked as a compensation handler")
	}
}
