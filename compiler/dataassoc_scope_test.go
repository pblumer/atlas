package compiler_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// nestedDataAssocModel puts the same activity — a script task reading and writing a
// process-level data object through BPMN data associations — in three places: the
// root scope, an embedded subprocess, and an event subprocess. A data object is
// declared once, at process level, which is where BPMN says it lives and where the
// engine keys it (by process instance).
const nestedDataAssocModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <message id="Msg_go" name="go">
    <extensionElements><zeebe:subscription correlationKey="= id"/></extensionElements>
  </message>
  <process id="p" isExecutable="true">
    <dataObject id="DO_reg" name="reg"/>
    <dataObjectReference id="Ref_root" name="reg" dataObjectRef="DO_reg"/>
    <dataObjectReference id="Ref_sub" name="reg" dataObjectRef="DO_reg"/>
    <dataObjectReference id="Ref_esp" name="reg" dataObjectRef="DO_reg"/>
    <startEvent id="start"/>
    <scriptTask id="root_task">
      <extensionElements><zeebe:script expression="= 1" resultVariable="a"/></extensionElements>
      <dataInputAssociation id="dia_root">
        <sourceRef>Ref_root</sourceRef>
        <assignment><to>cur</to></assignment>
      </dataInputAssociation>
      <dataOutputAssociation id="doa_root">
        <targetRef>Ref_root</targetRef>
        <assignment><from>= 1</from></assignment>
      </dataOutputAssociation>
    </scriptTask>
    <subProcess id="sub">
      <startEvent id="sub_start"/>
      <scriptTask id="sub_task">
        <extensionElements><zeebe:script expression="= 1" resultVariable="b"/></extensionElements>
        <dataInputAssociation id="dia_sub">
          <sourceRef>Ref_sub</sourceRef>
          <assignment><to>cur</to></assignment>
        </dataInputAssociation>
        <dataOutputAssociation id="doa_sub">
          <targetRef>Ref_sub</targetRef>
          <assignment><from>= 2</from></assignment>
        </dataOutputAssociation>
      </scriptTask>
      <endEvent id="sub_end"/>
      <sequenceFlow id="sf1" sourceRef="sub_start" targetRef="sub_task"/>
      <sequenceFlow id="sf2" sourceRef="sub_task" targetRef="sub_end"/>
    </subProcess>
    <subProcess id="esp" triggeredByEvent="true">
      <startEvent id="esp_start" isInterrupting="false">
        <messageEventDefinition messageRef="Msg_go"/>
      </startEvent>
      <scriptTask id="esp_task">
        <extensionElements><zeebe:script expression="= 1" resultVariable="c"/></extensionElements>
        <dataInputAssociation id="dia_esp">
          <sourceRef>Ref_esp</sourceRef>
          <assignment><to>cur</to></assignment>
        </dataInputAssociation>
        <dataOutputAssociation id="doa_esp">
          <targetRef>Ref_esp</targetRef>
          <assignment><from>= 3</from></assignment>
        </dataOutputAssociation>
      </scriptTask>
      <endEvent id="esp_end"/>
      <sequenceFlow id="ef1" sourceRef="esp_start" targetRef="esp_task"/>
      <sequenceFlow id="ef2" sourceRef="esp_task" targetRef="esp_end"/>
    </subProcess>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="root_task"/>
    <sequenceFlow id="f2" sourceRef="root_task" targetRef="sub"/>
    <sequenceFlow id="f3" sourceRef="sub" targetRef="end"/>
  </process>
</definitions>`

// TestDataAssociationsWireInEveryScope is the regression for a silent drop: data
// associations were wired only from the process's own element lists, so an activity
// nested in any subprocess lost them at compile time. The model still deployed and
// still ran — the activity simply read nothing and wrote nothing, with no error at
// deploy and no incident at runtime, which is the worst shape a defect can take.
//
// zeebe:ioMapping and multi-instance were already wired through the whole scope tree
// (wireScopeIO, wireScopeMI); this asserts data associations are too.
func TestDataAssociationsWireInEveryScope(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(nestedDataAssocModel))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, tc := range []struct{ task, scope string }{
		{"root_task", "the root scope"},
		{"sub_task", "an embedded subprocess"},
		{"esp_task", "an event subprocess"},
	} {
		id := nodeByBpmnId(t, cp, tc.task)
		if got := cp.DataInputAssociations(id); len(got) != 1 {
			t.Errorf("%s in %s: %d data input associations, want 1", tc.task, tc.scope, len(got))
		}
		if got := cp.DataOutputAssociations(id); len(got) != 1 {
			t.Errorf("%s in %s: %d data output associations, want 1", tc.task, tc.scope, len(got))
		}
	}
}
