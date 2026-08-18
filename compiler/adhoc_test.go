package compiler

import (
	"strings"
	"testing"
)

// TestParseAdHocSubProcess checks the core compile contract for an ad-hoc subprocess (ADR-0138):
// the container compiles as TypeAdHocSubProcess, its two unconnected contained activities are both
// entry activities (nothing sequences them), and the defaults are parallel ordering with
// cancelRemainingInstances true.
func TestParseAdHocSubProcess(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
	    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
	    <adHocSubProcess id="adhoc">
	      <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	      <serviceTask id="b"><extensionElements><zeebe:taskDefinition type="tb"/></extensionElements></serviceTask>
	    </adHocSubProcess>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	adhoc := nodeByBpmnId(t, cp, "adhoc")
	if adhoc.Type != TypeAdHocSubProcess || adhoc.Type.String() != "AdHocSubProcess" {
		t.Fatalf("adhoc type = %v (%q), want AdHocSubProcess", adhoc.Type, adhoc.Type.String())
	}
	// Both contained activities have no incoming flow, so both are entry activities.
	entries := cp.AdHocEntries(adhoc.ElementId)
	if len(entries) != 2 {
		t.Fatalf("entry activities = %v (%d), want 2", entries, len(entries))
	}
	want := map[int32]bool{nodeByBpmnId(t, cp, "a").ElementId: true, nodeByBpmnId(t, cp, "b").ElementId: true}
	for _, id := range entries {
		if !want[id] {
			t.Errorf("unexpected entry activity %d (%q)", id, cp.ElementBpmnId(id))
		}
	}
	d := cp.AdHoc(adhoc.Detail)
	if d.CompletionCondition != nil {
		t.Error("CompletionCondition != nil, want nil (none declared)")
	}
	if !d.CancelRemaining {
		t.Error("CancelRemaining = false, want true (BPMN default)")
	}
	// The contained activities are scoped by the ad-hoc container, like any subprocess scope.
	if fs := cp.Node(nodeByBpmnId(t, cp, "a").ElementId).FlowScope; fs != adhoc.ElementId {
		t.Errorf("contained activity FlowScope = %d, want %d (the ad-hoc container)", fs, adhoc.ElementId)
	}
}

// TestParseAdHocCompletionConditionAndCancel checks the ad-hoc's configuration compiles: the
// <completionCondition> to a boolean FEEL expression that knows the variables it reads, and
// cancelRemainingInstances="false" (ADR-0138).
func TestParseAdHocCompletionConditionAndCancel(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
	    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
	    <adHocSubProcess id="adhoc" cancelRemainingInstances="false">
	      <completionCondition>=done</completionCondition>
	      <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	    </adHocSubProcess>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail)
	if d.CompletionCondition == nil {
		t.Fatal("CompletionCondition = nil, want a compiled FEEL expression")
	}
	if ins := d.CompletionCondition.Inputs(); len(ins) != 1 || ins[0] != "done" {
		t.Errorf("completion condition inputs = %v, want [done]", ins)
	}
	if d.CancelRemaining {
		t.Error("CancelRemaining = true, want false (cancelRemainingInstances=\"false\")")
	}
}

// TestParseAdHocSequentialRejected: sequential ordering runs one contained activity at a time,
// which the engine does not implement yet — it is refused at deploy rather than silently run as
// parallel (which would start every activity at once, not what the model says) (ADR-0138).
func TestParseAdHocSequentialRejected(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
	    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
	    <adHocSubProcess id="adhoc" ordering="Sequential">
	      <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	    </adHocSubProcess>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse succeeded, want an error for ordering=\"Sequential\"")
	}
	for _, want := range []string{"adhoc", "Sequential", "parallel"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

// TestParseAdHocEntryActivities checks which contained nodes count as entry activities
// (ADR-0138): a node targeted by a sequence flow inside the ad-hoc is NOT an entry (it is reached
// by the flow), and a boundary event attached to a contained activity is not an entry either (it
// arms rather than starts).
func TestParseAdHocEntryActivities(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
	    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
	    <adHocSubProcess id="adhoc">
	      <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	      <serviceTask id="b"><extensionElements><zeebe:taskDefinition type="tb"/></extensionElements></serviceTask>
	      <serviceTask id="lone"><extensionElements><zeebe:taskDefinition type="tl"/></extensionElements></serviceTask>
	      <boundaryEvent id="bnd" attachedToRef="a">
	        <timerEventDefinition><timeDuration>PT5M</timeDuration></timerEventDefinition>
	      </boundaryEvent>
	      <endEvent id="bndEnd"/>
	      <sequenceFlow id="inner" sourceRef="a" targetRef="b"/>
	      <sequenceFlow id="bf" sourceRef="bnd" targetRef="bndEnd"/>
	    </adHocSubProcess>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	adhoc := nodeByBpmnId(t, cp, "adhoc")
	got := map[string]bool{}
	for _, id := range cp.AdHocEntries(adhoc.ElementId) {
		got[cp.ElementBpmnId(id)] = true
	}
	// `a` and `lone` have no incoming flow; `b` is targeted by the inner flow; `bnd` is a boundary
	// event (it arms on its host) and `bndEnd` is targeted by the boundary's flow.
	for _, id := range []string{"a", "lone"} {
		if !got[id] {
			t.Errorf("%q should be an entry activity, entries = %v", id, got)
		}
	}
	for _, id := range []string{"b", "bnd", "bndEnd"} {
		if got[id] {
			t.Errorf("%q should NOT be an entry activity, entries = %v", id, got)
		}
	}
}

// TestParseAdHocInvalidCompletionCondition rejects an ad-hoc whose completion condition is not
// valid FEEL (ADR-0138) — like every other compiled expression it is checked at deploy.
func TestParseAdHocInvalidCompletionCondition(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
	    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
	    <adHocSubProcess id="adhoc">
	      <completionCondition>= 1 +</completionCondition>
	      <serviceTask id="a"><extensionElements><zeebe:taskDefinition type="ta"/></extensionElements></serviceTask>
	    </adHocSubProcess>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse succeeded, want an error for a malformed completion condition")
	}
	if !strings.Contains(err.Error(), "adhoc") || !strings.Contains(err.Error(), "completion condition") {
		t.Errorf("error %q should name the ad-hoc and its completion condition", err.Error())
	}
}

// TestParseAdHocNoLongerRejected pins that an <adHocSubProcess> now compiles rather than being
// refused at deploy (ADR-0138) — the diagnostic it used to raise is gone.
func TestParseAdHocNoLongerRejected(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><adHocSubProcess id="Activity_1"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="Activity_1"/>
	    <sequenceFlow id="f2" sourceRef="Activity_1" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v (an ad-hoc subprocess must compile now)", err)
	}
	// An ad-hoc with no contained activity is an empty scope: no entries, and it must still compile.
	if n := nodeByBpmnId(t, cp, "Activity_1"); len(cp.AdHocEntries(n.ElementId)) != 0 {
		t.Errorf("empty ad-hoc entries = %v, want none", cp.AdHocEntries(n.ElementId))
	}
}
