package api

import (
	"strings"
	"testing"
)

const semanticOnly = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                      xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="Start"/>
    <serviceTask id="Charge"><extensionElements><zeebe:taskDefinition type="pay"/></extensionElements></serviceTask>
    <endEvent id="Done"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Charge"/>
    <sequenceFlow id="f2" sourceRef="Charge" targetRef="Done"/>
  </process>
</definitions>`

func TestEnsureDiagramLayoutInjects(t *testing.T) {
	out := string(ensureDiagramLayout([]byte(semanticOnly)))
	for _, want := range []string{
		"<bpmndi:BPMNDiagram",
		`bpmnElement="order"`,
		`<bpmndi:BPMNShape id="Start_di" bpmnElement="Start">`,
		`<bpmndi:BPMNShape id="Charge_di" bpmnElement="Charge">`,
		`<bpmndi:BPMNEdge id="f1_di" bpmnElement="f1">`,
		"omgdc:Bounds",
		"omgdi:waypoint",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated layout missing %q\n---\n%s", want, out)
		}
	}
	// The injected diagram must sit inside <definitions>.
	if i, j := strings.Index(out, "<bpmndi:BPMNDiagram"), strings.LastIndex(out, "</definitions>"); i < 0 || j < 0 || i > j {
		t.Fatalf("BPMNDiagram not placed before </definitions> (i=%d j=%d)", i, j)
	}
}

func TestEnsureDiagramLayoutPreservesExisting(t *testing.T) {
	withDI := semanticOnly[:len(semanticOnly)-len("</definitions>")] +
		`<bpmndi:BPMNDiagram id="x"/></definitions>`
	out := ensureDiagramLayout([]byte(withDI))
	if string(out) != withDI {
		t.Fatalf("existing DI must be left untouched")
	}
	// And it must not have added a second diagram (the existing one is self-closing).
	if n := strings.Count(string(out), "BPMNDiagram"); n != 1 {
		t.Fatalf("expected the single existing BPMNDiagram to be preserved, got %d occurrences", n)
	}
}

func TestEnsureDiagramLayoutBestEffort(t *testing.T) {
	// Not BPMN at all → returned unchanged rather than mangled.
	junk := []byte(`<html><body>nope</body></html>`)
	if string(ensureDiagramLayout(junk)) != string(junk) {
		t.Fatalf("non-BPMN input should be returned unchanged")
	}
}
