package api

import (
	"strings"
	"testing"
)

// TestGenerateDIEdgeCases covers the non-happy exits of generateDI: unparseable
// XML, a process without an id, and a process with no layout-relevant nodes.
func TestGenerateDIEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"malformed xml", `<definitions><process`},
		{"no process", `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"></definitions>`},
		{
			"process without id",
			`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process><startEvent id="s"/></process></definitions>`,
		},
		{
			"process with no nodes",
			`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><sequenceFlow id="f" sourceRef="a" targetRef="b"/></process></definitions>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if di, ok := generateDI([]byte(tc.src)); ok || di != "" {
				t.Fatalf("generateDI = (%q, %v), want (\"\", false)", di, ok)
			}
		})
	}
}

// TestGenerateDISkipsDanglingFlows exercises renderDI's and positionNodes' skip
// branches: a flow to an unknown node, and a flow with no id, must both be
// dropped while the real edge is still rendered.
func TestGenerateDISkipsDanglingFlows(t *testing.T) {
	src := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p">
	    <startEvent id="s"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="good" sourceRef="s" targetRef="e"/>
	    <sequenceFlow id="dangling" sourceRef="s" targetRef="ghost"/>
	    <sequenceFlow sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	di, ok := generateDI([]byte(src))
	if !ok {
		t.Fatal("generateDI returned ok=false for a valid process")
	}
	if !strings.Contains(di, `bpmnElement="good"`) {
		t.Fatalf("edge for real flow missing:\n%s", di)
	}
	if strings.Contains(di, `bpmnElement="dangling"`) {
		t.Fatalf("edge for dangling flow should have been skipped:\n%s", di)
	}
}

// TestGenerateDICyclicTerminates confirms the layering loop terminates (via its
// iteration cap) even when the sequence flows form a cycle.
func TestGenerateDICyclicTerminates(t *testing.T) {
	src := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p">
	    <task id="a"/>
	    <task id="b"/>
	    <sequenceFlow id="f1" sourceRef="a" targetRef="b"/>
	    <sequenceFlow id="f2" sourceRef="b" targetRef="a"/>
	  </process>
	</definitions>`
	di, ok := generateDI([]byte(src))
	if !ok || !strings.Contains(di, `bpmnElement="a"`) || !strings.Contains(di, `bpmnElement="b"`) {
		t.Fatalf("cyclic model should still lay out both nodes; ok=%v di=%s", ok, di)
	}
}

// TestInjectBeforeDefinitionsCloseNoTag returns src unchanged when there is no
// closing definitions tag to splice before.
func TestInjectBeforeDefinitionsCloseNoTag(t *testing.T) {
	src := []byte(`<something>no closing definitions here</something>`)
	if got := injectBeforeDefinitionsClose(src, "<di/>"); string(got) != string(src) {
		t.Fatalf("expected src returned unchanged, got %q", got)
	}
}

// TestStripDiagramLayout removes both the self-closing and the container forms of
// a BPMNDiagram (regardless of prefix) while leaving the semantic model and any
// inner self-closing shapes' *names* untouched.
func TestStripDiagramLayout(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"self-closing", `<definitions><process id="p"/><bpmndi:BPMNDiagram id="d"/></definitions>`},
		{"container", `<definitions><process id="p"/>` +
			`<bpmndi:BPMNDiagram id="d"><bpmndi:BPMNPlane><bpmndi:BPMNShape id="s_di"/></bpmndi:BPMNPlane></bpmndi:BPMNDiagram>` +
			`</definitions>`},
		{"no prefix", `<definitions><process id="p"/><BPMNDiagram><BPMNPlane/></BPMNDiagram></definitions>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := string(stripDiagramLayout([]byte(tc.src)))
			if strings.Contains(out, "BPMNDiagram") || strings.Contains(out, "BPMNPlane") || strings.Contains(out, "BPMNShape") {
				t.Fatalf("diagram interchange not fully stripped:\n%s", out)
			}
			if !strings.Contains(out, `<process id="p"/>`) {
				t.Fatalf("semantic model must survive stripping:\n%s", out)
			}
		})
	}
}

// TestStripDiagramLayoutTwoDiagrams confirms the non-greedy container match strips
// each of two adjacent diagrams independently rather than swallowing the gap
// between them.
func TestStripDiagramLayoutTwoDiagrams(t *testing.T) {
	src := `<definitions>` +
		`<bpmndi:BPMNDiagram id="a"><bpmndi:BPMNPlane/></bpmndi:BPMNDiagram>` +
		`<keep/>` +
		`<bpmndi:BPMNDiagram id="b"><bpmndi:BPMNPlane/></bpmndi:BPMNDiagram>` +
		`</definitions>`
	out := string(stripDiagramLayout([]byte(src)))
	if strings.Contains(out, "BPMNDiagram") {
		t.Fatalf("both diagrams should be stripped:\n%s", out)
	}
	if !strings.Contains(out, "<keep/>") {
		t.Fatalf("content between diagrams must be preserved:\n%s", out)
	}
}

// TestRelayoutDiagramRegenerates replaces an existing (minimal) layout with a
// freshly generated one: the old diagram is gone and a new one with shapes for
// every node is spliced in before </definitions>.
func TestRelayoutDiagramRegenerates(t *testing.T) {
	// A model whose stale layout places everything at the origin. Auto-layout must
	// discard it and lay the nodes out on the generated left-to-right grid.
	withStaleDI := semanticOnly[:len(semanticOnly)-len("</definitions>")] +
		`<bpmndi:BPMNDiagram id="stale"><bpmndi:BPMNPlane bpmnElement="order">` +
		`<bpmndi:BPMNShape id="Start_di" bpmnElement="Start">` +
		`<omgdc:Bounds x="0" y="0" width="36" height="36"/></bpmndi:BPMNShape>` +
		`</bpmndi:BPMNPlane></bpmndi:BPMNDiagram></definitions>`

	out := string(relayoutDiagram([]byte(withStaleDI)))
	if strings.Contains(out, `id="stale"`) {
		t.Fatalf("stale diagram should have been discarded:\n%s", out)
	}
	if n := strings.Count(out, "<bpmndi:BPMNDiagram"); n != 1 {
		t.Fatalf("expected exactly one regenerated diagram, got %d:\n%s", n, out)
	}
	for _, want := range []string{
		`bpmnElement="order"`,
		`<bpmndi:BPMNShape id="Start_di" bpmnElement="Start">`,
		`<bpmndi:BPMNShape id="Charge_di" bpmnElement="Charge">`,
		`<bpmndi:BPMNShape id="Done_di" bpmnElement="Done">`,
		`<bpmndi:BPMNEdge id="f1_di" bpmnElement="f1">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regenerated layout missing %q\n---\n%s", want, out)
		}
	}
	// The stale Start shape sat at the origin; the regenerated one must not.
	if strings.Contains(out, `<omgdc:Bounds x="0" y="0"`) {
		t.Fatalf("regenerated layout should not leave a node at the origin:\n%s", out)
	}
	if i, j := strings.Index(out, "<bpmndi:BPMNDiagram"), strings.LastIndex(out, "</definitions>"); i < 0 || j < 0 || i > j {
		t.Fatalf("regenerated diagram not placed before </definitions> (i=%d j=%d)", i, j)
	}
}

// TestRelayoutDiagramBestEffort leaves input untouched when no layout can be
// generated (non-BPMN input, or a model without layout-relevant nodes), so the
// button never strips a diagram without producing a replacement.
func TestRelayoutDiagramBestEffort(t *testing.T) {
	cases := []string{
		`<html><body>nope</body></html>`,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><bpmndi:BPMNDiagram id="d"/></process></definitions>`,
	}
	for _, src := range cases {
		if got := string(relayoutDiagram([]byte(src))); got != src {
			t.Fatalf("expected unchanged input for %q, got %q", src, got)
		}
	}
}
