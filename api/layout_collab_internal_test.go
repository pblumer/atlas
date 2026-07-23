package api

import (
	"strings"
	"testing"
)

// TestGenerateCollaborationDIBlackBoxPool covers the black-box-pool branch: a
// participant with no resolvable process still gets an (empty) pool band, while a
// participant with a process is laid out inside its band.
func TestGenerateCollaborationDIBlackBoxPool(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <collaboration id="C">
    <participant id="p_real" name="Real" processRef="proc"/>
    <participant id="p_box" name="External"/>
  </collaboration>
  <process id="proc">
    <startEvent id="s"/>
    <serviceTask id="t"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`)
	out, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok for a collaboration with a black-box pool")
	}
	for _, want := range []string{`bpmnElement="C"`, `bpmnElement="p_real"`, `bpmnElement="p_box"`, `width="600"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestGenerateCollaborationDINothingUsable covers the any==false return: a
// collaboration whose only participant has no id is skipped entirely, so no
// diagram is produced and ensureDiagramLayout leaves the source unchanged.
func TestGenerateCollaborationDINothingUsable(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <collaboration id="C"><participant name="anonymous"/></collaboration>
</definitions>`)
	if _, ok := generateDI(src); ok {
		t.Fatal("generateDI: want not-ok when every participant is skipped")
	}
	if got := ensureDiagramLayout(src); strings.Contains(string(got), "BPMNDiagram") {
		t.Fatal("ensureDiagramLayout injected a diagram it couldn't build")
	}
}

// TestGenerateDIScriptAndIntermediateEvents confirms that scriptTask,
// intermediateCatchEvent, and intermediateThrowEvent get BPMNShapes in the
// generated DI (they were previously missing from the layout parser).
func TestGenerateDIScriptAndIntermediateEvents(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	               xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <scriptTask id="stamp">
	      <extensionElements><zeebe:script expression="= 1" resultVariable="x"/></extensionElements>
	    </scriptTask>
	    <intermediateThrowEvent id="throw"><messageEventDefinition messageRef="M"/></intermediateThrowEvent>
	    <intermediateCatchEvent id="catch"><messageEventDefinition messageRef="M"/></intermediateCatchEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="stamp"/>
	    <sequenceFlow id="f2" sourceRef="stamp" targetRef="throw"/>
	    <sequenceFlow id="f3" sourceRef="throw" targetRef="catch"/>
	    <sequenceFlow id="f4" sourceRef="catch" targetRef="e"/>
	  </process>
	</definitions>`)
	di, ok := generateDI([]byte(src))
	if !ok {
		t.Fatal("generateDI: want ok for a process with script/intermediate events")
	}
	for _, want := range []string{
		`bpmnElement="stamp"`,
		`bpmnElement="throw"`,
		`bpmnElement="catch"`,
		`bpmnElement="f2"`,
		`bpmnElement="f3"`,
	} {
		if !strings.Contains(di, want) {
			t.Errorf("output missing %q:\n%s", want, di)
		}
	}
}

// TestGenerateCollaborationDIMessageFlows confirms that <messageFlow> elements
// in a collaboration produce BPMNEdge elements connecting elements across pools.
func TestGenerateCollaborationDIMessageFlows(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	               xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <collaboration id="C">
	    <participant id="P_a" name="A" processRef="a"/>
	    <participant id="P_b" name="B" processRef="b"/>
	    <messageFlow id="mf1" sourceRef="a_throw" targetRef="b_start"/>
	  </collaboration>
	  <message id="M" name="msg">
	    <extensionElements><zeebe:subscription correlationKey="= k"/></extensionElements>
	  </message>
	  <process id="a" isExecutable="true">
	    <startEvent id="a_start"/>
	    <intermediateThrowEvent id="a_throw"><messageEventDefinition messageRef="M"/></intermediateThrowEvent>
	    <endEvent id="a_end"/>
	    <sequenceFlow id="af1" sourceRef="a_start" targetRef="a_throw"/>
	    <sequenceFlow id="af2" sourceRef="a_throw" targetRef="a_end"/>
	  </process>
	  <process id="b" isExecutable="true">
	    <startEvent id="b_start"><messageEventDefinition messageRef="M"/></startEvent>
	    <endEvent id="b_end"/>
	    <sequenceFlow id="bf1" sourceRef="b_start" targetRef="b_end"/>
	  </process>
	</definitions>`)
	di, ok := generateDI([]byte(src))
	if !ok {
		t.Fatal("generateDI: want ok for a collaboration with message flows")
	}
	if !strings.Contains(di, `bpmnElement="mf1"`) {
		t.Errorf("output missing message flow edge:\n%s", di)
	}
	if !strings.Contains(di, `bpmnElement="a_throw"`) {
		t.Errorf("output missing intermediateThrowEvent shape:\n%s", di)
	}
}

// TestGenerateCollaborationDIStacksNodes exercises nodeExtents across nodes on
// different rows (two start events stack vertically), so the band height reflects
// the taller content.
func TestGenerateCollaborationDIStacksNodes(t *testing.T) {
	src := []byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <collaboration id="C"><participant id="p" name="P" processRef="proc"/></collaboration>
  <process id="proc">
    <startEvent id="s1"/>
    <startEvent id="s2"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s1" targetRef="e"/>
    <sequenceFlow id="f2" sourceRef="s2" targetRef="e"/>
  </process>
</definitions>`)
	out, ok := generateDI(src)
	if !ok {
		t.Fatal("generateDI: want ok")
	}
	if !strings.Contains(out, `bpmnElement="s1"`) || !strings.Contains(out, `bpmnElement="s2"`) {
		t.Errorf("output missing stacked start events:\n%s", out)
	}
}
