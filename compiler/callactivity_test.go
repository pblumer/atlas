package compiler

import (
	"strings"
	"testing"
)

// A call activity referencing a separate process by id, with a binding, a
// propagation flag, and an input mapping (ADR-0076 Phase 1).
const callActivityXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <callActivity id="call">
      <extensionElements>
        <zeebe:calledElement processId="child-proc" bindingType="deployment" propagateAllParentVariables="false"/>
        <zeebe:ioMapping>
          <zeebe:input source="= order" target="childOrder"/>
        </zeebe:ioMapping>
      </extensionElements>
    </callActivity>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="call"/>
    <sequenceFlow id="f2" sourceRef="call" targetRef="e"/>
  </process>
</definitions>`

// TestParseCallActivity checks the Phase-1 compiler contract: a <callActivity>
// compiles to a TypeCallActivity node whose detail records the called process id,
// binding, propagation flags, and its I/O mappings are wired.
func TestParseCallActivity(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(callActivityXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := nodeByBpmnId(t, cp, "call")
	if node.Type != TypeCallActivity {
		t.Fatalf("node %q type = %v, want CallActivity", "call", node.Type)
	}
	if got := node.Type.String(); got != "CallActivity" {
		t.Errorf("CallActivity.String() = %q, want CallActivity", got)
	}
	d := cp.CallActivity(node.Detail)
	if got := cp.Intern(d.CalledProcessId); got != "child-proc" {
		t.Errorf("called process id = %q, want child-proc", got)
	}
	if d.Binding != BindingDeployment {
		t.Errorf("binding = %v, want deployment", d.Binding)
	}
	if d.PropagateAllParent {
		t.Errorf("propagateAllParent = true, want false (isolated in)")
	}
	if !d.PropagateAllChild {
		t.Errorf("propagateAllChild = false, want true (the default)")
	}
	if node.IOInCount != 1 {
		t.Errorf("call activity io input mappings = %d, want 1 (wired)", node.IOInCount)
	}
}

// TestParseCallActivityDefaults checks the defaults: an omitted bindingType is
// latest, and omitted propagation flags default to true (Zeebe semantics).
func TestParseCallActivityDefaults(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <callActivity id="call">
	      <extensionElements><zeebe:calledElement processId="child"/></extensionElements>
	    </callActivity>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="call"/>
	    <sequenceFlow id="f2" sourceRef="call" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.CallActivity(nodeByBpmnId(t, cp, "call").Detail)
	if d.Binding != BindingLatest || !d.PropagateAllParent || !d.PropagateAllChild {
		t.Errorf("defaults = {binding:%v parent:%v child:%v}, want {latest true true}", d.Binding, d.PropagateAllParent, d.PropagateAllChild)
	}
}

// TestCallActivities checks the static, server-facing enumeration a per-server
// call-activity management view is built on: every call activity in node order,
// each with its element id, called process id, binding, propagation flags, and
// whether it is a multi-instance loop (ADR-0076).
func TestCallActivities(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(callActivityXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	refs := cp.CallActivities()
	if len(refs) != 1 {
		t.Fatalf("CallActivities() = %d refs, want 1", len(refs))
	}
	r := refs[0]
	if r.ElementId != "call" {
		t.Errorf("ElementId = %q, want call", r.ElementId)
	}
	if r.CalledProcessId != "child-proc" {
		t.Errorf("CalledProcessId = %q, want child-proc", r.CalledProcessId)
	}
	if r.Binding != BindingDeployment {
		t.Errorf("Binding = %v, want deployment", r.Binding)
	}
	if got := r.Binding.String(); got != "deployment" {
		t.Errorf("Binding.String() = %q, want deployment", got)
	}
	if r.PropagateAllParent {
		t.Errorf("PropagateAllParent = true, want false (isolated in)")
	}
	if !r.PropagateAllChild {
		t.Errorf("PropagateAllChild = false, want true (default)")
	}
	if r.MultiInstance {
		t.Errorf("MultiInstance = true, want false")
	}
}

// TestCallActivitiesNone returns an empty slice for a process with no call
// activities, and BindingLatest stringifies to "latest".
func TestCallActivitiesNone(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if refs := cp.CallActivities(); len(refs) != 0 {
		t.Errorf("CallActivities() = %d refs, want 0", len(refs))
	}
	if got := BindingLatest.String(); got != "latest" {
		t.Errorf("BindingLatest.String() = %q, want latest", got)
	}
}

// TestCallActivitiesMultiInstance flags a call activity that runs once per
// element of a collection — the server view marks it so an operator sees the
// called process may spawn many children per caller (ADR-0077).
func TestCallActivitiesMultiInstance(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <callActivity id="call">
	      <extensionElements><zeebe:calledElement processId="child"/></extensionElements>
	      <multiInstanceLoopCharacteristics>
	        <extensionElements><zeebe:loopCharacteristics inputCollection="=items"/></extensionElements>
	      </multiInstanceLoopCharacteristics>
	    </callActivity>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="call"/>
	    <sequenceFlow id="f2" sourceRef="call" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	refs := cp.CallActivities()
	if len(refs) != 1 || !refs[0].MultiInstance {
		t.Fatalf("CallActivities() = %+v, want one ref with MultiInstance=true", refs)
	}
}

// TestParseCallActivityRequiresProcessId fails deploy when the called process id
// is missing — a call activity that references nothing cannot run.
func TestParseCallActivityRequiresProcessId(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <callActivity id="call"><extensionElements><zeebe:calledElement/></extensionElements></callActivity>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="call"/>
	    <sequenceFlow id="f2" sourceRef="call" targetRef="e"/>
	  </process>
	</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("Parse: want an error for a call activity with no processId, got nil")
	}
}
