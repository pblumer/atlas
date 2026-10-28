package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// The tests in this file come from one model a garage runs (a repair order) and the four
// places where Atlas got it wrong: a correlation key read only the instance root, a message
// payload landed on the root under a local variable of the same name, a compensation throw
// in an event subprocess compensated nothing, and a cancel end event could not sit in the
// event subprocess that handles a cancellation. Each test is the smallest model that shows
// one of them (ADR-0508).

func str(name, text string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarString, Text: text}
}

func boolean(name string, b bool) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarBool, Bool: b}
}

// parseXML compiles a BPMN document, failing the test on any error.
func parseXML(t testing.TB, key uint64, src string) *compiler.CompiledProcess {
	t.Helper()
	cp, err := compiler.Parse(key, 1, strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

// jobTypeOf is the job type of the service task with the given BPMN id.
func jobTypeByID(t testing.TB, cp *compiler.CompiledProcess, bpmnID string) int32 {
	t.Helper()
	id, ok := cp.ElementIndexOf(bpmnID)
	if !ok {
		t.Fatalf("no element %q", bpmnID)
	}
	return cp.ServiceTask(cp.Node(id).Detail).JobType
}

// startEngine deploys cp on a fresh processor over h and recovers it.
func startEngine(t *testing.T, h *harness, clk *manualClock, cp *compiler.CompiledProcess) *engine.Processor {
	t.Helper()
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

// wantJobs asserts how many activatable jobs of a job type there are and returns them.
func wantJobs(t *testing.T, h *harness, jobType int32, want int, when string) []uint64 {
	t.Helper()
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != want {
		t.Fatalf("%s: %d jobs, want %d", when, len(jobs), want)
	}
	return jobs
}

// zusatzbefundBPMN is a repair order whose main work waits on a service task while a
// non-interrupting message event subprocess handles every additional finding the mechanic
// reports. Each finding is its own round: its id is mapped into the round's local scope,
// the customer's answer is correlated by that id, and the answer — also declared locally,
// as null — decides whether the extra work is done.
const zusatzbefundBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="mBefund" name="befund"><extensionElements><zeebe:subscription correlationKey="=auftragsNr"/></extensionElements></message>
  <message id="mFreigabe" name="freigabe"><extensionElements><zeebe:subscription correlationKey="=befundId"/></extensionElements></message>
  <process id="auftrag" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="work"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
    <subProcess id="zusatz" triggeredByEvent="true">
      <extensionElements><zeebe:ioMapping>
        <zeebe:input source="=befundId" target="befundId"/>
        <zeebe:input source="=null" target="zusatzFreigegeben"/>
      </zeebe:ioMapping></extensionElements>
      <startEvent id="zs" isInterrupting="false"><messageEventDefinition messageRef="mBefund"/></startEvent>
      <receiveTask id="warten" messageRef="mFreigabe"/>
      <exclusiveGateway id="gw" default="nein"/>
      <serviceTask id="arbeit"><extensionElements><zeebe:taskDefinition type="arbeit"/></extensionElements></serviceTask>
      <endEvent id="ze"/>
      <endEvent id="zabgelehnt"/>
      <sequenceFlow id="z1" sourceRef="zs" targetRef="warten"/>
      <sequenceFlow id="z2" sourceRef="warten" targetRef="gw"/>
      <sequenceFlow id="ja" sourceRef="gw" targetRef="arbeit"><conditionExpression>=zusatzFreigegeben = true</conditionExpression></sequenceFlow>
      <sequenceFlow id="nein" sourceRef="gw" targetRef="zabgelehnt"/>
      <sequenceFlow id="z3" sourceRef="arbeit" targetRef="ze"/>
    </subProcess>
  </process>
</definitions>`

// TestCorrelationKeyResolvesInWaitingElementsScope: two findings arrive one after the
// other, so two rounds wait at the same receive task. Each must wait for the answer to its
// own finding. The key is evaluated where the round waits — its local befundId — not on
// the instance root, where the second finding's id has overwritten the first's.
func TestCorrelationKeyResolvesInWaitingElementsScope(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 301, zusatzbefundBPMN)
	arbeit := jobTypeByID(t, cp, "arbeit")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("auftragsNr", "A1"))
	run(t, p)
	p.PublishMessage("befund", "A1", str("befundId", "BF-1"))
	p.PublishMessage("befund", "A1", str("befundId", "BF-2"))
	run(t, p)

	// The answer to the first finding reaches exactly the first round.
	p.PublishMessage("freigabe", "BF-1", boolean("zusatzFreigegeben", true))
	run(t, p)
	wantJobs(t, h, arbeit, 1, "after the answer to BF-1")

	// And the answer to the second reaches the second.
	p.PublishMessage("freigabe", "BF-2", boolean("zusatzFreigegeben", true))
	run(t, p)
	wantJobs(t, h, arbeit, 2, "after the answer to BF-2")
}

// TestMessagePayloadUpdatesNearestDefiningScope: the round declares zusatzFreigegeben
// locally. The customer's answer must update that variable — the one the gateway reads —
// rather than land on the instance root, hidden behind the local null.
func TestMessagePayloadUpdatesNearestDefiningScope(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 302, zusatzbefundBPMN)
	arbeit := jobTypeByID(t, cp, "arbeit")
	p := startEngine(t, h, &manualClock{}, cp)

	var piKey uint64
	p.CreateInstanceReporting(cp.Key, &piKey, str("auftragsNr", "A1"))
	run(t, p)
	p.PublishMessage("befund", "A1", str("befundId", "BF-1"))
	run(t, p)
	p.PublishMessage("freigabe", "BF-1", boolean("zusatzFreigegeben", true), str("kommentar", "ok"))
	run(t, p)

	wantJobs(t, h, arbeit, 1, "after the approving answer")
	if v := readVar(t, h.store, piKey, "zusatzFreigegeben"); v != nil {
		t.Errorf("zusatzFreigegeben leaked onto the root: %+v", v)
	}
	// A payload variable no scope declares still lands on the root, as it always has.
	if v := readVar(t, h.store, piKey, "kommentar"); v == nil || v.Text != "ok" {
		t.Errorf("kommentar on the root = %+v, want \"ok\"", v)
	}
}

// TestCorrelationKeyAndPayloadRecover: the subscription's key is frozen into its event and
// the payload into variable events, so a restart between the finding and the answer
// changes nothing.
func TestCorrelationKeyAndPayloadRecover(t *testing.T) {
	dir := t.TempDir()
	clk := &manualClock{}
	cp := parseXML(t, 303, zusatzbefundBPMN)
	arbeit := jobTypeByID(t, cp, "arbeit")

	h1 := openHarness(t, dir)
	p1 := startEngine(t, h1, clk, cp)
	p1.CreateInstance(cp.Key, str("auftragsNr", "A1"))
	run(t, p1)
	p1.PublishMessage("befund", "A1", str("befundId", "BF-1"))
	p1.PublishMessage("befund", "A1", str("befundId", "BF-2"))
	run(t, p1)
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := startEngine(t, h2, clk, cp)
	p2.PublishMessage("freigabe", "BF-1", boolean("zusatzFreigegeben", true))
	run(t, p2)
	wantJobs(t, h2, arbeit, 1, "after recovery and the answer to BF-1")
}

// TestReceiveTaskCorrelatesOnItsOwnInputMapping: a receive task's input mappings are
// evaluated before it subscribes, so its correlation key can use them.
func TestReceiveTaskCorrelatesOnItsOwnInputMapping(t *testing.T) {
	const src = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m" name="lieferung"><extensionElements><zeebe:subscription correlationKey="=bestellNr"/></extensionElements></message>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <receiveTask id="r" messageRef="m">
      <extensionElements><zeebe:ioMapping><zeebe:input source="=&quot;B-&quot; + nr" target="bestellNr"/></zeebe:ioMapping></extensionElements>
    </receiveTask>
    <serviceTask id="next"><extensionElements><zeebe:taskDefinition type="next"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="r"/>
    <sequenceFlow id="f2" sourceRef="r" targetRef="next"/>
    <sequenceFlow id="f3" sourceRef="next" targetRef="e"/>
  </process></definitions>`
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 304, src)
	next := jobTypeByID(t, cp, "next")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("nr", "7"))
	run(t, p)
	p.PublishMessage("lieferung", "B-7")
	run(t, p)
	wantJobs(t, h, next, 1, "after the delivery for B-7")
}

// TestReceiveTaskOutputMappingsKeepPayloadLocal: with output mappings, a receive task's
// payload is the task's own result, like a worker's — the mappings decide what reaches
// the instance, and the raw payload does not.
func TestReceiveTaskOutputMappingsKeepPayloadLocal(t *testing.T) {
	const src = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="m" name="antwort"><extensionElements><zeebe:subscription correlationKey="=&quot;k&quot;"/></extensionElements></message>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <receiveTask id="r" messageRef="m">
      <extensionElements><zeebe:ioMapping><zeebe:output source="=answer" target="antwort"/></zeebe:ioMapping></extensionElements>
    </receiveTask>
    <serviceTask id="next"><extensionElements><zeebe:taskDefinition type="next"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="r"/>
    <sequenceFlow id="f2" sourceRef="r" targetRef="next"/>
    <sequenceFlow id="f3" sourceRef="next" targetRef="e"/>
  </process></definitions>`
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 305, src)
	next := jobTypeByID(t, cp, "next")
	p := startEngine(t, h, &manualClock{}, cp)

	var piKey uint64
	p.CreateInstanceReporting(cp.Key, &piKey)
	run(t, p)
	p.PublishMessage("antwort", "k", str("answer", "42"), str("raw", "x"))
	run(t, p)
	wantJobs(t, h, next, 1, "after the answer")

	if v := readVar(t, h.store, piKey, "antwort"); v == nil || v.Text != "42" {
		t.Errorf("antwort = %+v, want \"42\"", v)
	}
	for _, name := range []string{"answer", "raw"} {
		if v := readVar(t, h.store, piKey, name); v != nil {
			t.Errorf("raw payload variable %q reached the root: %+v", name, v)
		}
	}
}

// TestSignalPayloadUpdatesNearestDefiningScope: a broadcast's payload follows the same
// rule as a message's.
func TestSignalPayloadUpdatesNearestDefiningScope(t *testing.T) {
	const src = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <signal id="sig" name="go"/>
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <subProcess id="sub">
      <extensionElements><zeebe:ioMapping><zeebe:input source="=null" target="status"/></zeebe:ioMapping></extensionElements>
      <startEvent id="ss"/>
      <intermediateCatchEvent id="c"><signalEventDefinition signalRef="sig"/></intermediateCatchEvent>
      <exclusiveGateway id="gw" default="other"/>
      <serviceTask id="ok"><extensionElements><zeebe:taskDefinition type="ok"/></extensionElements></serviceTask>
      <endEvent id="se"/>
      <endEvent id="so"/>
      <sequenceFlow id="s1" sourceRef="ss" targetRef="c"/>
      <sequenceFlow id="s2" sourceRef="c" targetRef="gw"/>
      <sequenceFlow id="yes" sourceRef="gw" targetRef="ok"><conditionExpression>=status = "ok"</conditionExpression></sequenceFlow>
      <sequenceFlow id="other" sourceRef="gw" targetRef="so"/>
      <sequenceFlow id="s3" sourceRef="ok" targetRef="se"/>
    </subProcess>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
    <sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>
  </process></definitions>`
	const thrower = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <signal id="sig" name="go"/>
  <process id="t" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="th"><signalEventDefinition signalRef="sig"/></intermediateThrowEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="th"/>
    <sequenceFlow id="f2" sourceRef="th" targetRef="e"/>
  </process></definitions>`
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 306, src)
	tp := parseXML(t, 316, thrower)
	ok := jobTypeByID(t, cp, "ok")
	p := startEngine(t, h, &manualClock{}, cp)
	p.Deploy(tp)

	p.CreateInstance(cp.Key)
	run(t, p)
	// The broadcast carries the throwing instance's variables as its payload.
	p.CreateInstance(tp.Key, str("status", "ok"))
	run(t, p)
	wantJobs(t, h, ok, 1, "after the broadcast")
}

// stornoEventSubBPMN is an order whose cancellation is handled by an interrupting event
// subprocess at the root: it compensates what was done, then bills the effort. The throw
// sits in the event subprocess, the compensable activity in the scope around it.
const stornoEventSubBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="mStorno" name="storno"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <process id="order" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="charge"><extensionElements><zeebe:taskDefinition type="charge"/></extensionElements></serviceTask>
    <boundaryEvent id="cb" attachedToRef="charge"><compensateEventDefinition/></boundaryEvent>
    <serviceTask id="refund" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="refund"/></extensionElements></serviceTask>
    <association id="a" sourceRef="cb" targetRef="refund"/>
    <serviceTask id="wait"><extensionElements><zeebe:taskDefinition type="wait"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="charge"/>
    <sequenceFlow id="f2" sourceRef="charge" targetRef="wait"/>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>
    <subProcess id="storno" triggeredByEvent="true">
      <startEvent id="ss" isInterrupting="true"><messageEventDefinition messageRef="mStorno"/></startEvent>
      <intermediateThrowEvent id="undo"><compensateEventDefinition/></intermediateThrowEvent>
      <serviceTask id="bill"><extensionElements><zeebe:taskDefinition type="bill"/></extensionElements></serviceTask>
      <endEvent id="se"/>
      <sequenceFlow id="s1" sourceRef="ss" targetRef="undo"/>
      <sequenceFlow id="s2" sourceRef="undo" targetRef="bill"/>
      <sequenceFlow id="s3" sourceRef="bill" targetRef="se"/>
    </subProcess>
  </process>
</definitions>`

// TestCompensationThrowInEventSubprocessCompensatesEnclosingScope: a compensation throw
// directly in an event subprocess compensates the scope the event subprocess handles —
// here the charge in the instance root — and, waiting for completion, bills only after
// the refund is done.
func TestCompensationThrowInEventSubprocessCompensatesEnclosingScope(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 307, stornoEventSubBPMN)
	charge, refund, wait, bill := jobTypeByID(t, cp, "charge"), jobTypeByID(t, cp, "refund"), jobTypeByID(t, cp, "wait"), jobTypeByID(t, cp, "bill")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	p.CompleteJob(wantJobs(t, h, charge, 1, "at charge")[0])
	run(t, p)
	wantJobs(t, h, wait, 1, "at wait")

	p.PublishMessage("storno", "O1")
	run(t, p)
	wantJobs(t, h, wait, 0, "after the cancellation interrupted the work")
	refunds := wantJobs(t, h, refund, 1, "after the cancellation")
	wantJobs(t, h, bill, 0, "while the refund runs")

	p.CompleteJob(refunds[0])
	run(t, p)
	p.CompleteJob(wantJobs(t, h, bill, 1, "after the refund")[0])
	run(t, p)
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after billing: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// waitProcessBPMN is charge → throw → next with the throw's waitForCompletion attribute
// substituted for WAIT (empty for the default).
const waitProcessBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="w" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="charge"><extensionElements><zeebe:taskDefinition type="charge"/></extensionElements></serviceTask>
    <boundaryEvent id="cb" attachedToRef="charge"><compensateEventDefinition/></boundaryEvent>
    <serviceTask id="refund" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="refund"/></extensionElements></serviceTask>
    <association id="a" sourceRef="cb" targetRef="refund"/>
    <intermediateThrowEvent id="undo"><compensateEventDefinition activityRef="charge" WAIT/></intermediateThrowEvent>
    <serviceTask id="next"><extensionElements><zeebe:taskDefinition type="next"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="charge"/>
    <sequenceFlow id="f2" sourceRef="charge" targetRef="undo"/>
    <sequenceFlow id="f3" sourceRef="undo" targetRef="next"/>
    <sequenceFlow id="f4" sourceRef="next" targetRef="e"/>
  </process></definitions>`

// TestCompensationThrowWaitsForItsHandlers: by default a throw goes on only once the
// handlers it started have finished (BPMN's waitForCompletion="true").
func TestCompensationThrowWaitsForItsHandlers(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 308, strings.Replace(waitProcessBPMN, "WAIT", "", 1))
	charge, refund, next := jobTypeByID(t, cp, "charge"), jobTypeByID(t, cp, "refund"), jobTypeByID(t, cp, "next")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key)
	run(t, p)
	p.CompleteJob(wantJobs(t, h, charge, 1, "at charge")[0])
	run(t, p)
	refunds := wantJobs(t, h, refund, 1, "after the throw")
	wantJobs(t, h, next, 0, "while the refund runs")

	p.CompleteJob(refunds[0])
	run(t, p)
	wantJobs(t, h, next, 1, "after the refund")
}

// TestCompensationThrowWithoutWaitGoesOn: waitForCompletion="false" keeps the
// fire-and-continue behaviour — the handler and the next step run side by side.
func TestCompensationThrowWithoutWaitGoesOn(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 309, strings.Replace(waitProcessBPMN, "WAIT", `waitForCompletion="false"`, 1))
	charge, refund, next := jobTypeByID(t, cp, "charge"), jobTypeByID(t, cp, "refund"), jobTypeByID(t, cp, "next")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key)
	run(t, p)
	p.CompleteJob(wantJobs(t, h, charge, 1, "at charge")[0])
	run(t, p)
	wantJobs(t, h, refund, 1, "after the throw")
	wantJobs(t, h, next, 1, "after the throw, without waiting")
}

// TestCompensationThrowWithNothingToCompensateGoesOn: a throw that starts no handler has
// nothing to wait for.
func TestCompensationThrowWithNothingToCompensateGoesOn(t *testing.T) {
	src := strings.Replace(waitProcessBPMN, "WAIT", "", 1)
	// Throw before charge runs: nothing has completed yet.
	src = strings.Replace(src, `<sequenceFlow id="f1" sourceRef="s" targetRef="charge"/>`, `<sequenceFlow id="f1" sourceRef="s" targetRef="undo"/>`, 1)
	src = strings.Replace(src, `<sequenceFlow id="f2" sourceRef="charge" targetRef="undo"/>`, `<sequenceFlow id="f2" sourceRef="charge" targetRef="next"/>`, 1)
	src = strings.Replace(src, `<sequenceFlow id="f3" sourceRef="undo" targetRef="next"/>`, `<sequenceFlow id="f3" sourceRef="undo" targetRef="charge"/>`, 1)
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 310, src)
	charge := jobTypeByID(t, cp, "charge")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key)
	run(t, p)
	wantJobs(t, h, charge, 1, "after a throw with nothing to compensate")
}

// TestCompensationThrowWaitRecovers: a throw waiting for its handler is rebuilt from the
// log — after a restart, the handler's completion still releases it.
func TestCompensationThrowWaitRecovers(t *testing.T) {
	dir := t.TempDir()
	clk := &manualClock{}
	cp := parseXML(t, 311, strings.Replace(waitProcessBPMN, "WAIT", "", 1))
	charge, refund, next := jobTypeByID(t, cp, "charge"), jobTypeByID(t, cp, "refund"), jobTypeByID(t, cp, "next")

	h1 := openHarness(t, dir)
	p1 := startEngine(t, h1, clk, cp)
	p1.CreateInstance(cp.Key)
	run(t, p1)
	p1.CompleteJob(wantJobs(t, h1, charge, 1, "at charge")[0])
	run(t, p1)
	wantJobs(t, h1, refund, 1, "before the crash")
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := startEngine(t, h2, clk, cp)
	wantJobs(t, h2, next, 0, "after recovery, still waiting")
	p2.CompleteJob(wantJobs(t, h2, refund, 1, "after recovery")[0])
	run(t, p2)
	wantJobs(t, h2, next, 1, "after the recovered refund")
}

// stornoTransactionBPMN is the cancellation the repair order needs: the work runs in a
// transaction; an interrupting message event subprocess inside it ends in a cancel end
// event; the transaction's cancel boundary bills the effort. A normal finish invoices.
const stornoTransactionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="mStorno" name="storno"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <process id="order" isExecutable="true">
    <startEvent id="s"/>
    <transaction id="tx">
      <startEvent id="ts"/>
      <serviceTask id="reserve"><extensionElements><zeebe:taskDefinition type="reserve"/></extensionElements></serviceTask>
      <boundaryEvent id="rb" attachedToRef="reserve"><compensateEventDefinition/></boundaryEvent>
      <serviceTask id="unreserve" isForCompensation="true"><extensionElements><zeebe:taskDefinition type="unreserve"/></extensionElements></serviceTask>
      <association id="a" sourceRef="rb" targetRef="unreserve"/>
      <serviceTask id="wait"><extensionElements><zeebe:taskDefinition type="wait"/></extensionElements></serviceTask>
      <endEvent id="te"/>
      <sequenceFlow id="t1" sourceRef="ts" targetRef="reserve"/>
      <sequenceFlow id="t2" sourceRef="reserve" targetRef="wait"/>
      <sequenceFlow id="t3" sourceRef="wait" targetRef="te"/>
      <subProcess id="storno" triggeredByEvent="true">
        <startEvent id="ss" isInterrupting="true"><messageEventDefinition messageRef="mStorno"/></startEvent>
        <endEvent id="sc"><cancelEventDefinition/></endEvent>
        <sequenceFlow id="s1" sourceRef="ss" targetRef="sc"/>
      </subProcess>
    </transaction>
    <boundaryEvent id="txc" attachedToRef="tx"><cancelEventDefinition/></boundaryEvent>
    <serviceTask id="invoice"><extensionElements><zeebe:taskDefinition type="invoice"/></extensionElements></serviceTask>
    <serviceTask id="effort"><extensionElements><zeebe:taskDefinition type="effort"/></extensionElements></serviceTask>
    <endEvent id="done"/>
    <endEvent id="cancelled"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="tx"/>
    <sequenceFlow id="f2" sourceRef="tx" targetRef="invoice"/>
    <sequenceFlow id="f3" sourceRef="invoice" targetRef="done"/>
    <sequenceFlow id="f4" sourceRef="txc" targetRef="effort"/>
    <sequenceFlow id="f5" sourceRef="effort" targetRef="cancelled"/>
  </process>
</definitions>`

// TestCancelEndInEventSubprocessCancelsTransaction: the cancellation arrives while the
// transaction waits. The event subprocess's cancel end rolls the transaction back —
// compensating the reservation, and only that — and the cancel boundary takes over once
// the rollback is done.
func TestCancelEndInEventSubprocessCancelsTransaction(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 312, stornoTransactionBPMN)
	reserve, unreserve, wait := jobTypeByID(t, cp, "reserve"), jobTypeByID(t, cp, "unreserve"), jobTypeByID(t, cp, "wait")
	invoice, effort := jobTypeByID(t, cp, "invoice"), jobTypeByID(t, cp, "effort")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	p.CompleteJob(wantJobs(t, h, reserve, 1, "at reserve")[0])
	run(t, p)
	wantJobs(t, h, wait, 1, "at wait")

	p.PublishMessage("storno", "O1")
	run(t, p)
	wantJobs(t, h, wait, 0, "after the cancellation")
	undo := wantJobs(t, h, unreserve, 1, "after the cancellation")
	wantJobs(t, h, effort, 0, "while the rollback runs")

	p.CompleteJob(undo[0])
	run(t, p)
	wantJobs(t, h, invoice, 0, "after the rollback")
	p.CompleteJob(wantJobs(t, h, effort, 1, "after the rollback")[0])
	run(t, p)
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the cancellation path: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestCancelEndInEventSubprocessWithNothingDone: a cancellation before anything compensable
// completed has nothing to roll back and goes straight out the cancel boundary.
func TestCancelEndInEventSubprocessWithNothingDone(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 313, stornoTransactionBPMN)
	reserve, unreserve, effort := jobTypeByID(t, cp, "reserve"), jobTypeByID(t, cp, "unreserve"), jobTypeByID(t, cp, "effort")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	wantJobs(t, h, reserve, 1, "at reserve")
	p.PublishMessage("storno", "O1")
	run(t, p)
	wantJobs(t, h, reserve, 0, "after the cancellation")
	wantJobs(t, h, unreserve, 0, "with nothing to roll back")
	wantJobs(t, h, effort, 1, "after the cancellation")
}

// TestTransactionWithStornoEventSubCommits: without a cancellation the transaction commits
// as before, its armed event subprocess disarmed.
func TestTransactionWithStornoEventSubCommits(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 314, stornoTransactionBPMN)
	reserve, wait, invoice, effort := jobTypeByID(t, cp, "reserve"), jobTypeByID(t, cp, "wait"), jobTypeByID(t, cp, "invoice"), jobTypeByID(t, cp, "effort")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	p.CompleteJob(wantJobs(t, h, reserve, 1, "at reserve")[0])
	run(t, p)
	p.CompleteJob(wantJobs(t, h, wait, 1, "at wait")[0])
	run(t, p)
	wantJobs(t, h, invoice, 1, "after the commit")
	wantJobs(t, h, effort, 0, "after the commit")

	// A late cancellation reaches nothing.
	p.PublishMessage("storno", "O1")
	run(t, p)
	wantJobs(t, h, effort, 0, "after a late cancellation")
}

// TestCancelEndInEventSubprocessRecovers: a crash while the rollback runs. The cancelling
// mark is derived from committed events, so after recovery the rollback's completion still
// routes out the cancel boundary rather than committing the transaction.
func TestCancelEndInEventSubprocessRecovers(t *testing.T) {
	dir := t.TempDir()
	clk := &manualClock{}
	cp := parseXML(t, 315, stornoTransactionBPMN)
	reserve, unreserve, invoice, effort := jobTypeByID(t, cp, "reserve"), jobTypeByID(t, cp, "unreserve"), jobTypeByID(t, cp, "invoice"), jobTypeByID(t, cp, "effort")

	h1 := openHarness(t, dir)
	p1 := startEngine(t, h1, clk, cp)
	p1.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p1)
	p1.CompleteJob(wantJobs(t, h1, reserve, 1, "at reserve")[0])
	run(t, p1)
	p1.PublishMessage("storno", "O1")
	run(t, p1)
	wantJobs(t, h1, unreserve, 1, "before the crash")
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := startEngine(t, h2, clk, cp)
	p2.CompleteJob(wantJobs(t, h2, unreserve, 1, "after recovery")[0])
	run(t, p2)
	wantJobs(t, h2, invoice, 0, "after the recovered rollback")
	wantJobs(t, h2, effort, 1, "after the recovered rollback")
}

// rearmBPMN is a running order with a non-interrupting event subprocess per incoming
// "note" message and per "ping" signal, each of whose runs parks on a service task.
const rearmBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="mNote" name="note"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <signal id="sPing" name="ping"/>
  <process id="o" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="work"><extensionElements><zeebe:taskDefinition type="work"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
    <subProcess id="notes" triggeredByEvent="true">
      <startEvent id="ns" isInterrupting="false"><messageEventDefinition messageRef="mNote"/></startEvent>
      <serviceTask id="book"><extensionElements><zeebe:taskDefinition type="book"/></extensionElements></serviceTask>
      <endEvent id="ne"/>
      <sequenceFlow id="n1" sourceRef="ns" targetRef="book"/>
      <sequenceFlow id="n2" sourceRef="book" targetRef="ne"/>
    </subProcess>
    <subProcess id="pings" triggeredByEvent="true">
      <startEvent id="ps" isInterrupting="false"><signalEventDefinition signalRef="sPing"/></startEvent>
      <serviceTask id="answer"><extensionElements><zeebe:taskDefinition type="answer"/></extensionElements></serviceTask>
      <endEvent id="pe"/>
      <sequenceFlow id="p1" sourceRef="ps" targetRef="answer"/>
      <sequenceFlow id="p2" sourceRef="answer" targetRef="pe"/>
    </subProcess>
  </process></definitions>`

// TestNonInterruptingEventSubprocessTakesBackToBackMessages: two messages published one
// right behind the other each start a run. Before the trigger re-armed in the step that
// fired it, the second arrived while no subscription existed and was lost.
func TestNonInterruptingEventSubprocessTakesBackToBackMessages(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 320, rearmBPMN)
	book := jobTypeByID(t, cp, "book")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	p.PublishMessage("note", "O1", str("text", "eins"))
	p.PublishMessage("note", "O1", str("text", "zwei"))
	p.PublishMessage("note", "O1", str("text", "drei"))
	run(t, p)
	wantJobs(t, h, book, 3, "after three back-to-back messages")

	// Exactly one trigger stays armed: a fourth message starts a fourth run.
	p.PublishMessage("note", "O1", str("text", "vier"))
	run(t, p)
	wantJobs(t, h, book, 4, "after a fourth message")
}

// TestNonInterruptingEventSubprocessRunReadsItsOwnMessage: each run reads the payload of
// the message that started it, even when all of them were delivered before the first run
// activated.
func TestNonInterruptingEventSubprocessRunReadsItsOwnMessage(t *testing.T) {
	src := strings.Replace(rearmBPMN,
		`<serviceTask id="book"><extensionElements><zeebe:taskDefinition type="book"/></extensionElements></serviceTask>`,
		`<serviceTask id="book"><extensionElements><zeebe:taskDefinition type="book"/><zeebe:ioMapping><zeebe:input source="=text" target="mine"/></zeebe:ioMapping></extensionElements></serviceTask>`, 1)
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 321, src)
	book := jobTypeByID(t, cp, "book")
	p := startEngine(t, h, &manualClock{}, cp)

	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	p.PublishMessage("note", "O1", str("text", "eins"))
	p.PublishMessage("note", "O1", str("text", "zwei"))
	run(t, p)

	got := map[string]bool{}
	for _, job := range wantJobs(t, h, book, 2, "after two messages") {
		j, ok, err := h.store.GetJob(job)
		if err != nil || !ok {
			t.Fatalf("GetJob(%d) = %v, %v, %v", job, j, ok, err)
		}
		if v := readVar(t, h.store, j.ElementInstanceKey, "mine"); v != nil {
			got[v.Text] = true
		}
	}
	if !got["eins"] || !got["zwei"] {
		t.Errorf("the runs read %v, want one run per message: eins and zwei", got)
	}
}

// TestNonInterruptingEventSubprocessTakesBufferedMessages: messages published with a time
// to live before the instance exists are taken one per run when its trigger arms — the
// buffer path re-arms through the fired trigger's completion, and arms one trigger, not two.
func TestNonInterruptingEventSubprocessTakesBufferedMessages(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 322, rearmBPMN)
	book := jobTypeByID(t, cp, "book")
	p := startEngine(t, h, &manualClock{}, cp)

	p.PublishMessageWithTTL("note", "O1", "m1", time.Hour, str("text", "eins"))
	p.PublishMessageWithTTL("note", "O1", "m2", time.Hour, str("text", "zwei"))
	run(t, p)
	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	wantJobs(t, h, book, 2, "after both buffered messages were taken")

	p.PublishMessage("note", "O1", str("text", "drei"))
	run(t, p)
	wantJobs(t, h, book, 3, "after a live message")
}

// TestNonInterruptingSignalEventSubprocessTakesBackToBackBroadcasts: the same for signals,
// which have no buffer to fall back on.
func TestNonInterruptingSignalEventSubprocessTakesBackToBackBroadcasts(t *testing.T) {
	const thrower = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <signal id="sPing" name="ping"/>
  <process id="t" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="th"><signalEventDefinition signalRef="sPing"/></intermediateThrowEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="th"/>
    <sequenceFlow id="f2" sourceRef="th" targetRef="e"/>
  </process></definitions>`
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := parseXML(t, 323, rearmBPMN)
	tp := parseXML(t, 324, thrower)
	answer := jobTypeByID(t, cp, "answer")
	p := startEngine(t, h, &manualClock{}, cp)
	p.Deploy(tp)

	p.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p)
	p.CreateInstance(tp.Key)
	p.CreateInstance(tp.Key)
	run(t, p)
	wantJobs(t, h, answer, 2, "after two back-to-back broadcasts")
}

// TestRearmedTriggerRecovers: the trigger re-armed in the step that fired is rebuilt from
// its events, so after a restart a message still starts a run.
func TestRearmedTriggerRecovers(t *testing.T) {
	dir := t.TempDir()
	clk := &manualClock{}
	cp := parseXML(t, 325, rearmBPMN)
	book := jobTypeByID(t, cp, "book")

	h1 := openHarness(t, dir)
	p1 := startEngine(t, h1, clk, cp)
	p1.CreateInstance(cp.Key, str("orderId", "O1"))
	run(t, p1)
	p1.PublishMessage("note", "O1", str("text", "eins"))
	run(t, p1)
	wantJobs(t, h1, book, 1, "before the crash")
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := startEngine(t, h2, clk, cp)
	p2.PublishMessage("note", "O1", str("text", "zwei"))
	run(t, p2)
	wantJobs(t, h2, book, 2, "after recovery and a second message")
}
