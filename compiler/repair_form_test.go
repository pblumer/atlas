package compiler

import (
	"strings"
	"testing"
)

// A repair form is the modeler's answer to "if this task goes wrong, these are the
// values worth looking at" (ADR-0169). It binds with the same zeebe:formDefinition a
// user task binds its work form with — the tag says the element has a form, and what
// the form is *for* follows from the element carrying it.
const repairFormBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="repairing" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="charge">
      <extensionElements>
        <zeebe:taskDefinition type="charge-card"/>
        <zeebe:formDefinition formId="fix-payment"/>
      </extensionElements>
    </serviceTask>
    <serviceTask id="ship">
      <extensionElements><zeebe:taskDefinition type="ship-it"/></extensionElements>
    </serviceTask>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="charge"/>
    <sequenceFlow id="f2" sourceRef="charge" targetRef="ship"/>
    <sequenceFlow id="f3" sourceRef="ship" targetRef="done"/>
  </process>
</definitions>`

// nodeIndexByBpmnId finds a compiled node's *index* by the id the modeler wrote. The
// package's nodeByBpmnId hands back the node itself; the repair form is keyed by index,
// which is what the incident surface resolves an element to.
func nodeIndexByBpmnId(t *testing.T, cp *CompiledProcess, id string) int32 {
	t.Helper()
	for i := 0; i < cp.NodeCount(); i++ {
		if cp.ElementBpmnId(int32(i)) == id {
			return int32(i)
		}
	}
	t.Fatalf("no node with bpmn id %q", id)
	return -1
}

// TestServiceTaskCarriesItsRepairForm is the binding itself: the form id survives
// compilation and is readable off the node, which is what lets the incident surface
// offer it without re-parsing the model (invariant I5).
func TestServiceTaskCarriesItsRepairForm(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(repairFormBPMN))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cp.RepairForm(nodeIndexByBpmnId(t, cp, "charge")); got != "fix-payment" {
		t.Errorf("repair form of charge = %q, want %q", got, "fix-payment")
	}
	// A task that names none costs nothing and reads as none — not as the previous
	// task's form, which a parallel slice off by one would produce.
	if got := cp.RepairForm(nodeIndexByBpmnId(t, cp, "ship")); got != "" {
		t.Errorf("repair form of ship = %q, want none", got)
	}
	// Nor does it leak onto elements that never carried one.
	for _, id := range []string{"start", "done"} {
		if got := cp.RepairForm(nodeIndexByBpmnId(t, cp, id)); got != "" {
			t.Errorf("repair form of %s = %q, want none", id, got)
		}
	}
}

// TestRepairFormIsSeparateFromAUserTaskForm keeps the two bindings from being confused.
// A user task's form is its *work* form — what a person fills in to do the task — and it
// lives on UserTaskDetail. A repair form answers a different question on a different
// element, and reading one through the other would offer an operator the wrong form at
// the worst possible moment.
func TestRepairFormIsSeparateFromAUserTaskForm(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="both" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="approve">
      <extensionElements><zeebe:formDefinition formId="approval-form"/></extensionElements>
    </userTask>
    <serviceTask id="charge">
      <extensionElements>
        <zeebe:taskDefinition type="charge-card"/>
        <zeebe:formDefinition formId="fix-payment"/>
      </extensionElements>
    </serviceTask>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="approve"/>
    <sequenceFlow id="f2" sourceRef="approve" targetRef="charge"/>
    <sequenceFlow id="f3" sourceRef="charge" targetRef="done"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	approve := nodeIndexByBpmnId(t, cp, "approve")
	ut := cp.UserTask(cp.Node(approve).Detail)
	if got := cp.Intern(ut.FormId); got != "approval-form" {
		t.Errorf("user task's work form = %q, want %q", got, "approval-form")
	}
	// The user task's own form is the form it shows a person to *do* the task. It is not
	// a repair form, and must not be offered as one.
	if got := cp.RepairForm(approve); got != "" {
		t.Errorf("user task reports repair form %q; its work form is not a repair form", got)
	}
	if got := cp.RepairForm(nodeIndexByBpmnId(t, cp, "charge")); got != "fix-payment" {
		t.Errorf("service task's repair form = %q, want %q", got, "fix-payment")
	}
}

// TestRepairFormOnAConnectorTask covers the case the record was actually written for: a
// connector task is the one that parks with an incident most often, and it compiles
// down a different arm of the task walk than a plain service task does. Both arms must
// record the form, or the feature works only for the tasks least likely to need it.
func TestRepairFormOnAConnectorTask(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas.dev/schema/1.0">
  <process id="mailing" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="notify">
      <extensionElements>
        <atlas:mailConnector connector="Postbox" to="=recipient" subject="Hi" body="There"/>
        <zeebe:formDefinition formId="fix-recipient"/>
      </extensionElements>
    </serviceTask>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="notify"/>
    <sequenceFlow id="f2" sourceRef="notify" targetRef="done"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	notify := nodeIndexByBpmnId(t, cp, "notify")
	if cp.Node(notify).Type != TypeConnectorTask {
		t.Fatalf("notify compiled as %s, want a connector task", cp.Node(notify).Type)
	}
	if got := cp.RepairForm(notify); got != "fix-recipient" {
		t.Errorf("connector task's repair form = %q, want %q", got, "fix-recipient")
	}
}

// TestSetRepairFormIgnoresUnknownNode mirrors the other node-metadata setters: an id
// that is not a node is dropped rather than panicking or growing the slice.
func TestSetRepairFormIgnoresUnknownNode(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	task := b.AddServiceTask("work", 3)
	b.SetRepairForm(task, "kept")
	b.SetRepairForm(-1, "dropped")
	b.SetRepairForm(99, "dropped")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := cp.RepairForm(task); got != "kept" {
		t.Errorf("repair form = %q, want %q", got, "kept")
	}
	// Out-of-range reads answer "none" rather than panicking — the accessor is called
	// with an element index that came off a durable record, which may name a node this
	// version of the model no longer has.
	for _, id := range []int32{-1, 99} {
		if got := cp.RepairForm(id); got != "" {
			t.Errorf("RepairForm(%d) = %q, want empty", id, got)
		}
	}
}
