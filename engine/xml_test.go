package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

const orderBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="order" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="charge">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="payment" retries="3"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="charge"/>
    <bpmn:sequenceFlow id="f2" sourceRef="charge" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

// TestExecuteParsedBPMN deploys a process compiled straight from BPMN XML and
// runs it to completion, proving the XML front end feeds the engine.
func TestExecuteParsedBPMN(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, err := compiler.Parse(7, 1, strings.NewReader(orderBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Job type of the single service task (start's successor).
	taskID := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	jobType := cp.ServiceTask(cp.Node(taskID).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(jobs))
	}

	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// registerBPMN is the shape the identity-lifecycle test reached for and had to abandon:
// one long-running instance per identity, with a non-interrupting message event subprocess
// that books each incoming product event into a data object. The main flow parks on a
// service task; every "ev" message runs the handler, whose script task reads the register
// through a <dataInputAssociation> and writes it back through a <dataOutputAssociation>.
//
// Both halves used to fail silently. The associations sat inside a subprocess, and the
// compiler wired associations only over the process root's element lists, so they were
// dropped at compile time — the register stayed empty. And the handler's zeebe:ioMapping
// was run by the *armed trigger* as well as by the handler, evaluating at instance start
// and promoting that null back over the register on the first firing.
const registerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="ident" isExecutable="true">
    <bpmn:dataObject id="DO_reg" name="register"/>
    <bpmn:dataObjectReference id="Ref_reg" dataObjectRef="DO_reg"/>
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="hold">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="lifecycle" retries="3"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="hold"/>
    <bpmn:sequenceFlow id="f2" sourceRef="hold" targetRef="end"/>

    <bpmn:subProcess id="hub" triggeredByEvent="true">
      <bpmn:extensionElements>
        <zeebe:ioMapping>
          <zeebe:output source="=seen" target="seen"/>
        </zeebe:ioMapping>
      </bpmn:extensionElements>
      <bpmn:startEvent id="ev" isInterrupting="false">
        <bpmn:messageEventDefinition messageRef="Msg_ev"/>
      </bpmn:startEvent>
      <bpmn:scriptTask id="book">
        <bpmn:extensionElements>
          <zeebe:script expression="=serviceId" resultVariable="seen"/>
        </bpmn:extensionElements>
        <bpmn:dataInputAssociation>
          <bpmn:sourceRef>Ref_reg</bpmn:sourceRef>
          <bpmn:assignment><bpmn:to>previous</bpmn:to></bpmn:assignment>
        </bpmn:dataInputAssociation>
        <bpmn:dataOutputAssociation>
          <bpmn:targetRef>Ref_reg</bpmn:targetRef>
          <bpmn:assignment><bpmn:from>=serviceId</bpmn:from></bpmn:assignment>
        </bpmn:dataOutputAssociation>
      </bpmn:scriptTask>
      <bpmn:endEvent id="evEnd"/>
      <bpmn:sequenceFlow id="hf1" sourceRef="ev" targetRef="book"/>
      <bpmn:sequenceFlow id="hf2" sourceRef="book" targetRef="evEnd"/>
    </bpmn:subProcess>
  </bpmn:process>
  <bpmn:message id="Msg_ev" name="ev"/>
</bpmn:definitions>`

// TestEventSubprocessBooksIntoDataObject runs registerBPMN: two product events, each
// booked into the data object by an activity nested inside the event subprocess, with the
// second run reading back what the first one wrote.
func TestEventSubprocessBooksIntoDataObject(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, err := compiler.Parse(11, 1, strings.NewReader(registerBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := singleActiveInstance(t, h.store)

	// First event: the handler books m365 into the register, and reads back nothing —
	// the register was empty until this run wrote it.
	p.PublishMessage("ev", "", model.VariableValue{Name: "serviceId", Kind: model.VarString, Text: "m365"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after event 1: %v", err)
	}
	if obj := readDataObject(t, h.store, piKey, "register"); obj == nil || obj.Text != "m365" {
		t.Fatalf("register after event 1 = %+v, want m365 (the association inside the subprocess must write)", obj)
	}
	if v := readVar(t, h.store, piKey, "previous"); v == nil || v.Kind != model.VarNull {
		t.Errorf("previous after event 1 = %+v, want null (nothing was in the register yet)", v)
	}

	// Second event: the same handler reads the first run's entry back out of the register
	// before overwriting it — the read half of the association, on a re-armed trigger.
	p.PublishMessage("ev", "", model.VariableValue{Name: "serviceId", Kind: model.VarString, Text: "vpn"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after event 2: %v", err)
	}
	if v := readVar(t, h.store, piKey, "previous"); v == nil || v.Text != "m365" {
		t.Fatalf("previous after event 2 = %+v, want m365 (read back through the input association)", v)
	}
	if obj := readDataObject(t, h.store, piKey, "register"); obj == nil || obj.Text != "vpn" {
		t.Errorf("register after event 2 = %+v, want vpn", obj)
	}
	// The handler's own ioMapping promoted its result, and did so from the handler run.
	if v := readVar(t, h.store, piKey, "seen"); v == nil || v.Text != "vpn" {
		t.Errorf("seen = %+v, want vpn (promoted by the handler's output mapping)", v)
	}
	// Both bookings are in the object's history — the free audit trail this whole model
	// shape is chosen for — after the empty entry the declaration seeds at instance start.
	var history []string
	if err := h.store.DataObjectSnapshotHistory(piKey, func(_ int64, _ uint64, v *model.DataObjectValue) error {
		history = append(history, v.Text)
		return nil
	}); err != nil {
		t.Fatalf("DataObjectSnapshotHistory: %v", err)
	}
	if len(history) != 3 || history[1] != "m365" || history[2] != "vpn" {
		t.Errorf("register history = %q, want [\"\" m365 vpn] (seeded, then one entry per event)", history)
	}
	// The main flow never moved.
	jobType := cp.ServiceTask(cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target).Detail).JobType
	if jobGone(t, h.store, jobType) {
		t.Fatal("the non-interrupting event subprocess cancelled the main-flow job")
	}
	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the main flow completes: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
