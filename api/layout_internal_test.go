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
