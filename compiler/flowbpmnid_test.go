package compiler

import (
	"strings"
	"testing"
)

// TestFlowBpmnId covers the second half of mapping the engine back onto a diagram.
// Elements always kept their BPMN id; sequence flows did not, because nothing at
// runtime needed it. The Operations heatmap does: the engine counts which flow a token
// took by its compiled index, and the browser can only colour an arrow it can name
// (ADR-draft-operations-heatmap). Every authored flow — at the root and inside a
// subprocess — must report the id it was written with, and the flow the compiler makes
// up for a link event (ADR-0133) has no arrow on the diagram, so it reports none.
func TestFlowBpmnId(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <endEvent id="se"/>
	      <sequenceFlow id="inner" sourceRef="ss" targetRef="se"/>
	    </subProcess>
	    <intermediateThrowEvent id="thr"><linkEventDefinition name="A"/></intermediateThrowEvent>
	    <intermediateCatchEvent id="cat"><linkEventDefinition name="A"/></intermediateCatchEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="toSub" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="toThrow" sourceRef="sub" targetRef="thr"/>
	    <sequenceFlow id="toEnd" sourceRef="cat" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := map[string]bool{}
	synthetic := 0
	for i := 0; i < cp.FlowCount(); i++ {
		id := cp.FlowBpmnId(int32(i))
		if id == "" {
			synthetic++
			f := cp.Flow(int32(i))
			if cp.ElementBpmnId(f.Source) != "thr" || cp.ElementBpmnId(f.Target) != "cat" {
				t.Errorf("flow %d has no BPMN id but runs %s → %s; only the link goto may be unnamed",
					i, cp.ElementBpmnId(f.Source), cp.ElementBpmnId(f.Target))
			}
			continue
		}
		got[id] = true
	}
	for _, want := range []string{"toSub", "toThrow", "toEnd", "inner"} {
		if !got[want] {
			t.Errorf("no compiled flow reports BPMN id %q (got %v)", want, got)
		}
	}
	if synthetic != 1 {
		t.Errorf("%d flows without a BPMN id, want exactly 1 (the link goto)", synthetic)
	}
	for _, out := range []int32{-1, int32(cp.FlowCount())} {
		if id := cp.FlowBpmnId(out); id != "" {
			t.Errorf("FlowBpmnId(%d) = %q for an index out of range, want \"\"", out, id)
		}
	}
}

// TestBuilderFlowWithoutBpmnId: a process assembled in code names no flows, and that
// must read as "no id" rather than as some other flow's — the default, not an accident
// of a zero value pointing at the intern table's first string.
func TestBuilderFlowWithoutBpmnId(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	s := b.AddStartEvent()
	e := b.AddEndEvent()
	named := b.Connect(s, e)
	b.SetFlowBpmnId(named, "Flow_1")
	unnamed := b.Connect(s, e)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.FlowBpmnId(named); got != "Flow_1" {
		t.Errorf("named flow = %q, want Flow_1", got)
	}
	if got := cp.FlowBpmnId(unnamed); got != "" {
		t.Errorf("unnamed flow = %q, want \"\"", got)
	}
}
