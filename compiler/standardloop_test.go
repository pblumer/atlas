package compiler

import (
	"strings"
	"testing"
)

// standardLoopXML is a service task marked with a BPMN standard loop (ADR-0130):
// it repeats while <loopCondition> holds, checked before the first iteration
// (testBefore) and capped by loopMaximum.
const standardLoopXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="work">
      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
      <standardLoopCharacteristics testBefore="true" loopMaximum="5">
        <loopCondition>=not(approved)</loopCondition>
      </standardLoopCharacteristics>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
  </process>
</definitions>`

// TestParseStandardLoop checks that <standardLoopCharacteristics> compiles into a
// MultiInstanceDetail marked Standard — the same loop table a multi-instance
// activity uses, so the engine's body/iteration machinery runs it (ADR-0130) —
// while the node keeps its real activity type.
func TestParseStandardLoop(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(standardLoopXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := nodeByBpmnId(t, cp, "work")
	if node.Type != TypeServiceTask {
		t.Fatalf("node %q type = %v, want ServiceTask (the loop keeps the real type)", "work", node.Type)
	}
	if node.MultiInstance < 0 {
		t.Fatalf("node %q MultiInstance = %d, want a loop detail index (>= 0)", "work", node.MultiInstance)
	}
	d := cp.MultiInstance(node.MultiInstance)
	if !d.Standard {
		t.Errorf("Standard = false, want true (a standard loop, not a multi-instance)")
	}
	if !d.Sequential {
		t.Errorf("Sequential = false, want true (a standard loop runs one iteration at a time)")
	}
	if !d.TestBefore {
		t.Errorf("TestBefore = false, want true")
	}
	if d.LoopMaximum != 5 {
		t.Errorf("LoopMaximum = %d, want 5", d.LoopMaximum)
	}
	if d.LoopCondition == nil {
		t.Errorf("LoopCondition = nil, want a compiled expression")
	}
	if d.InputCollection != nil || d.Cardinality != nil {
		t.Errorf("InputCollection/Cardinality = non-nil, want nil (a standard loop iterates on a condition)")
	}
	if d.InputElement != -1 || d.OutputCollection != -1 {
		t.Errorf("InputElement/OutputCollection = %d/%d, want -1/-1 (no per-item bindings)", d.InputElement, d.OutputCollection)
	}
}

// TestParseStandardLoopDefaults checks the BPMN defaults: testBefore is absent
// (a repeat-until that always runs at least once) and no loopMaximum means the
// condition alone bounds the loop.
func TestParseStandardLoopDefaults(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work">
	      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
	      <standardLoopCharacteristics>
	        <loopCondition>=retry</loopCondition>
	      </standardLoopCharacteristics>
	    </serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.MultiInstance(nodeByBpmnId(t, cp, "work").MultiInstance)
	if d.TestBefore {
		t.Errorf("TestBefore = true, want false (BPMN's default: check after each iteration)")
	}
	if d.LoopMaximum != 0 {
		t.Errorf("LoopMaximum = %d, want 0 (no cap)", d.LoopMaximum)
	}
	if d.LoopCondition == nil {
		t.Errorf("LoopCondition = nil, want a compiled expression")
	}
}

// TestParseStandardLoopMaximumOnly checks a loop bounded only by loopMaximum: no
// condition means "repeat until the cap", the fixed-count form of a standard loop.
func TestParseStandardLoopMaximumOnly(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work">
	      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
	      <standardLoopCharacteristics loopMaximum="3"/>
	    </serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.MultiInstance(nodeByBpmnId(t, cp, "work").MultiInstance)
	if d.LoopCondition != nil {
		t.Errorf("LoopCondition = non-nil, want nil (bounded by loopMaximum alone)")
	}
	if d.LoopMaximum != 3 {
		t.Errorf("LoopMaximum = %d, want 3", d.LoopMaximum)
	}
}

// TestParseStandardLoopScopes checks that the marker is wired for every activity
// kind the engine can loop — including one nested in a subprocess, so the
// recursive scope walk covers it — and on subprocesses and call activities.
func TestParseStandardLoopScopes(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <callActivity id="call">
	      <extensionElements><zeebe:calledElement processId="child"/></extensionElements>
	      <standardLoopCharacteristics loopMaximum="2"/>
	    </callActivity>
	    <subProcess id="sub">
	      <standardLoopCharacteristics><loopCondition>=again</loopCondition></standardLoopCharacteristics>
	      <startEvent id="ss"/>
	      <userTask id="inner">
	        <standardLoopCharacteristics><loopCondition>=again</loopCondition></standardLoopCharacteristics>
	      </userTask>
	      <endEvent id="se"/>
	      <sequenceFlow id="sf1" sourceRef="ss" targetRef="inner"/>
	      <sequenceFlow id="sf2" sourceRef="inner" targetRef="se"/>
	    </subProcess>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="call"/>
	    <sequenceFlow id="f2" sourceRef="call" targetRef="sub"/>
	    <sequenceFlow id="f3" sourceRef="sub" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, id := range []string{"call", "sub", "inner"} {
		node := nodeByBpmnId(t, cp, id)
		if node.MultiInstance < 0 {
			t.Fatalf("node %q MultiInstance = %d, want a loop detail index (>= 0)", id, node.MultiInstance)
		}
		if !cp.MultiInstance(node.MultiInstance).Standard {
			t.Errorf("node %q Standard = false, want true", id)
		}
	}
	if got := nodeByBpmnId(t, cp, "sub").Type; got != TypeSubProcess {
		t.Errorf("looping subprocess type = %v, want SubProcess (the loop keeps the real type)", got)
	}
}

// TestParseStandardLoopRefusals checks the deploy-time refusals: a loop with no
// bound at all (neither a condition nor a maximum) would never terminate, a
// non-positive or unparseable maximum is a modeling error, an uncompilable
// condition must fail at deploy (I5), and an activity cannot carry both loop
// markers at once.
func TestParseStandardLoopRefusals(t *testing.T) {
	cases := map[string]string{
		"no condition and no maximum": `<standardLoopCharacteristics/>`,
		"bad loopCondition":           `<standardLoopCharacteristics><loopCondition>=)(</loopCondition></standardLoopCharacteristics>`,
		"non-numeric loopMaximum":     `<standardLoopCharacteristics loopMaximum="lots"><loopCondition>=again</loopCondition></standardLoopCharacteristics>`,
		"zero loopMaximum":            `<standardLoopCharacteristics loopMaximum="0"><loopCondition>=again</loopCondition></standardLoopCharacteristics>`,
		"negative loopMaximum":        `<standardLoopCharacteristics loopMaximum="-1"><loopCondition>=again</loopCondition></standardLoopCharacteristics>`,
		"both loop markers": `<standardLoopCharacteristics><loopCondition>=again</loopCondition></standardLoopCharacteristics>
			<multiInstanceLoopCharacteristics><loopCardinality>3</loopCardinality></multiInstanceLoopCharacteristics>`,
	}
	for name, marker := range cases {
		t.Run(name, func(t *testing.T) {
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
			        xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
			  <process id="p" isExecutable="true">
			    <startEvent id="s"/>
			    <serviceTask id="work">
			      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
			      ` + marker + `
			    </serviceTask>
			    <endEvent id="e"/>
			    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
			    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
			  </process>
			</definitions>`
			if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
				t.Fatalf("Parse: err = nil, want a refusal for %s", name)
			}
		})
	}
}

// TestSetStandardLoopInvalidNode: marking an out-of-range node a standard loop is a
// silent no-op (validNode guards it), matching SetMultiInstance.
func TestSetStandardLoopInvalidNode(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	b.SetStandardLoop(999, false, 2, nil) // must not panic
	start := b.AddStartEvent()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.Node(start).MultiInstance; got != -1 {
		t.Errorf("Node(start).MultiInstance = %d, want -1 (nothing was marked)", got)
	}
}
