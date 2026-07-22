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
