package compiler

import (
	"errors"
	"strings"
	"testing"
)

// A plain embedded subprocess: start → subProcess{ innerStart → check → innerEnd }
// → end. Phase 1 (ADR-0074) compiles it — the container is a real node and its
// children carry the subprocess as their flow scope — even though the runtime
// behavior that enters the scope lands in a later phase.
const subProcessXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <subProcess id="sub">
      <startEvent id="innerStart"/>
      <scriptTask id="check">
        <extensionElements><zeebe:script expression="= 1" resultVariable="r"/></extensionElements>
      </scriptTask>
      <endEvent id="innerEnd"/>
      <sequenceFlow id="if1" sourceRef="innerStart" targetRef="check"/>
      <sequenceFlow id="if2" sourceRef="check" targetRef="innerEnd"/>
    </subProcess>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
    <sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>
  </process>
</definitions>`

// TestParseSubProcess checks the Phase-1 invariant: the <subProcess> compiles to a
// TypeSubProcess node at the process root, and its children carry that node's
// ElementId as their FlowScope, while root-level nodes stay at scope -1.
func TestParseSubProcess(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(subProcessXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	sub := nodeByBpmnId(t, cp, "sub")
	if sub.Type != TypeSubProcess {
		t.Fatalf("node %q type = %v, want SubProcess", "sub", sub.Type)
	}
	if got := sub.Type.String(); got != "SubProcess" {
		t.Errorf("SubProcess.String() = %q, want SubProcess", got)
	}
	if sub.FlowScope != -1 {
		t.Errorf("subProcess FlowScope = %d, want -1 (process root)", sub.FlowScope)
	}

	// Children of the subprocess carry its ElementId as their flow scope.
	for _, id := range []string{"innerStart", "check", "innerEnd"} {
		if got := nodeByBpmnId(t, cp, id).FlowScope; got != sub.ElementId {
			t.Errorf("node %q FlowScope = %d, want %d (the subprocess)", id, got, sub.ElementId)
		}
	}
	// The subprocess exposes its inner start as its scope entry point (what the
	// runtime seeds on activation); a non-subprocess node exposes none.
	starts := cp.ScopeStartEvents(sub.ElementId)
	if len(starts) != 1 || starts[0] != nodeByBpmnId(t, cp, "innerStart").ElementId {
		t.Errorf("ScopeStartEvents(sub) = %v, want [%d]", starts, nodeByBpmnId(t, cp, "innerStart").ElementId)
	}
	if got := cp.ScopeStartEvents(nodeByBpmnId(t, cp, "check").ElementId); len(got) != 0 {
		t.Errorf("ScopeStartEvents(check) = %v, want empty (not a subprocess)", got)
	}
	// Root-level nodes stay at the process root.
	for _, id := range []string{"s", "e"} {
		if got := nodeByBpmnId(t, cp, id).FlowScope; got != -1 {
			t.Errorf("node %q FlowScope = %d, want -1", id, got)
		}
	}
}

// TestParseSubProcessIOMapping checks that a zeebe:ioMapping on a <subProcess> is
// parsed and wired onto the container node — the compiler half of subprocess-level
// variable passing; the engine applies these generically (ADR-0074 Phase 4).
func TestParseSubProcessIOMapping(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <extensionElements>
	        <zeebe:ioMapping>
	          <zeebe:input source="= outerVal + 1" target="innerVal"/>
	          <zeebe:output source="= innerVal * 100" target="promoted"/>
	        </zeebe:ioMapping>
	      </extensionElements>
	      <startEvent id="iStart"/>
	      <scriptTask id="echo">
	        <extensionElements><zeebe:script expression="= innerVal" resultVariable="innerEcho"/></extensionElements>
	      </scriptTask>
	      <endEvent id="iEnd"/>
	      <sequenceFlow id="if1" sourceRef="iStart" targetRef="echo"/>
	      <sequenceFlow id="if2" sourceRef="echo" targetRef="iEnd"/>
	    </subProcess>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sub := nodeByBpmnId(t, cp, "sub")
	if sub.IOInCount != 1 || sub.IOOutCount != 1 {
		t.Fatalf("subprocess io mapping counts = in %d out %d, want 1 and 1", sub.IOInCount, sub.IOOutCount)
	}
}

// TestParseSubProcessCrossScopeFlowRejected checks that a sequence flow crossing a
// scope boundary (an inner node wired directly to a root node) fails deploy with a
// cross-scope validation error — the dormant checkScopes rule becomes load-bearing
// once real scopes exist.
func TestParseSubProcessCrossScopeFlowRejected(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="innerStart"/>
	      <endEvent id="innerEnd"/>
	      <sequenceFlow id="if1" sourceRef="innerStart" targetRef="innerEnd"/>
	      <sequenceFlow id="bad" sourceRef="innerStart" targetRef="e"/>
	    </subProcess>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>
	  </process>
	</definitions>`

	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want a cross-scope validation error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Parse error = %v (%T), want *ValidationError", err, err)
	}
	found := false
	for _, p := range ve.Problems {
		if p.Rule == RuleFlowCrossScope {
			found = true
		}
	}
	if !found {
		t.Errorf("validation problems %v do not include %s", ve.Problems, RuleFlowCrossScope)
	}
}
