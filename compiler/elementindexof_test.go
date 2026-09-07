package compiler

import (
	"strings"
	"testing"
)

// TestElementIndexOf covers the diagram→engine direction: an operator arrives with
// the BPMN id of a shape they clicked, and everything the engine keyed by node index
// is reachable only through this lookup. It must round-trip with ElementBpmnId, and
// it must say "no" rather than guess for an id the model does not hold.
func TestElementIndexOf(t *testing.T) {
	const xml = `<?xml version="1.0"?>
	<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="start"/>
	    <serviceTask id="verbuchen"><extensionElements><zeebe:taskDefinition type="t"/></extensionElements></serviceTask>
	    <endEvent id="done"/>
	    <sequenceFlow id="f1" sourceRef="start" targetRef="verbuchen"/>
	    <sequenceFlow id="f2" sourceRef="verbuchen" targetRef="done"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, id := range []string{"start", "verbuchen", "done"} {
		idx, ok := cp.ElementIndexOf(id)
		if !ok {
			t.Fatalf("ElementIndexOf(%q) reported no such element", id)
		}
		if got := cp.ElementBpmnId(idx); got != id {
			t.Errorf("ElementIndexOf(%q) = %d, which is %q", id, idx, got)
		}
	}
	if _, ok := cp.ElementIndexOf("no-such-element"); ok {
		t.Error("an id the model does not hold reported a node index")
	}
	if _, ok := cp.ElementIndexOf(""); ok {
		t.Error("the empty id reported a node index; every unnamed node would match it")
	}
}
