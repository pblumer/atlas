package compiler

import (
	"strings"
	"testing"
)

// A sequential multi-instance service task over an input collection, with an input
// element, an output collection/element, and a completion condition (ADR-0077
// Phase 1). Zeebe nests the loop characteristics inside the marker's own
// <extensionElements>.
const multiInstanceXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="work">
      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
      <multiInstanceLoopCharacteristics isSequential="true">
        <extensionElements>
          <zeebe:loopCharacteristics inputCollection="=items" inputElement="item"
            outputCollection="results" outputElement="=item.total"/>
        </extensionElements>
        <completionCondition>=count(results) &gt; 2</completionCondition>
      </multiInstanceLoopCharacteristics>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
  </process>
</definitions>`

// TestParseMultiInstance checks the Phase-1 compiler contract: a
// <multiInstanceLoopCharacteristics> compiles into a MultiInstanceDetail indexed by
// the activity node's MultiInstance field, keeping the node's real activity type.
func TestParseMultiInstance(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(multiInstanceXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := nodeByBpmnId(t, cp, "work")
	if node.Type != TypeServiceTask {
		t.Fatalf("node %q type = %v, want ServiceTask (the loop keeps the real type)", "work", node.Type)
	}
	if node.MultiInstance < 0 {
		t.Fatalf("node %q MultiInstance = %d, want a detail index (>= 0)", "work", node.MultiInstance)
	}
	d := cp.MultiInstance(node.MultiInstance)
	if !d.Sequential {
		t.Errorf("Sequential = false, want true")
	}
	if d.InputCollection == nil {
		t.Errorf("InputCollection = nil, want a compiled expression")
	}
	if d.Cardinality != nil {
		t.Errorf("Cardinality = non-nil, want nil (collection-driven)")
	}
	if got := cp.Intern(d.InputElement); got != "item" {
		t.Errorf("InputElement = %q, want item", got)
	}
	if got := cp.Intern(d.OutputCollection); got != "results" {
		t.Errorf("OutputCollection = %q, want results", got)
	}
	if d.OutputElement == nil {
		t.Errorf("OutputElement = nil, want a compiled expression")
	}
	if d.CompletionCondition == nil {
		t.Errorf("CompletionCondition = nil, want a compiled expression")
	}
}

// TestParseMultiInstanceDefaults checks the parallel default (no isSequential) and
// the optional fields: with no output mapping and no completion condition, they are
// absent, and a collection-only loop has no cardinality.
func TestParseMultiInstanceDefaults(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work">
	      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
	      <multiInstanceLoopCharacteristics>
	        <extensionElements>
	          <zeebe:loopCharacteristics inputCollection="=items" inputElement="item"/>
	        </extensionElements>
	      </multiInstanceLoopCharacteristics>
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
	if d.Sequential {
		t.Errorf("Sequential = true, want false (parallel is the default)")
	}
	if d.OutputCollection != -1 {
		t.Errorf("OutputCollection = %d, want -1 (none)", d.OutputCollection)
	}
	if d.OutputElement != nil || d.CompletionCondition != nil {
		t.Errorf("OutputElement/CompletionCondition = non-nil, want nil (none)")
	}
}

// TestParseMultiInstanceCardinality checks the loop-cardinality form: a fixed count
// with no input collection.
func TestParseMultiInstanceCardinality(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work">
	      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
	      <multiInstanceLoopCharacteristics>
	        <loopCardinality>3</loopCardinality>
	      </multiInstanceLoopCharacteristics>
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
	if d.Cardinality == nil {
		t.Errorf("Cardinality = nil, want a compiled expression")
	}
	if d.InputCollection != nil {
		t.Errorf("InputCollection = non-nil, want nil (cardinality-driven)")
	}
	if d.InputElement != -1 {
		t.Errorf("InputElement = %d, want -1 (none)", d.InputElement)
	}
}

// TestParseMultiInstanceSubProcess checks that the loop is wired generically over the
// scope tree — a multi-instance embedded subprocess keeps its TypeSubProcess type and
// carries the loop detail (proving the recursive wiring, ADR-0077).
func TestParseMultiInstanceSubProcess(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <multiInstanceLoopCharacteristics>
	        <extensionElements>
	          <zeebe:loopCharacteristics inputCollection="=orders" inputElement="order"/>
	        </extensionElements>
	      </multiInstanceLoopCharacteristics>
	      <startEvent id="is"/>
	      <endEvent id="ie"/>
	      <sequenceFlow id="if" sourceRef="is" targetRef="ie"/>
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
	node := nodeByBpmnId(t, cp, "sub")
	if node.Type != TypeSubProcess {
		t.Fatalf("node %q type = %v, want SubProcess", "sub", node.Type)
	}
	if node.MultiInstance < 0 {
		t.Fatalf("multi-instance subprocess has no loop detail (MultiInstance = %d)", node.MultiInstance)
	}
	if got := cp.Intern(cp.MultiInstance(node.MultiInstance).InputElement); got != "order" {
		t.Errorf("InputElement = %q, want order", got)
	}
}

// TestParseMultiInstanceRequiresCollectionOrCardinality fails deploy when a
// multi-instance activity names neither an input collection nor a cardinality — it
// has no way to know how many times to run.
func TestParseMultiInstanceRequiresCollectionOrCardinality(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <serviceTask id="work">
	      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
	      <multiInstanceLoopCharacteristics>
	        <extensionElements>
	          <zeebe:loopCharacteristics inputElement="item"/>
	        </extensionElements>
	      </multiInstanceLoopCharacteristics>
	    </serviceTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	  </process>
	</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("Parse: want an error for a multi-instance activity with no collection or cardinality, got nil")
	}
}

// TestParseMultiInstanceErrors covers the deploy-refusal branches: an uncompilable
// FEEL source in any loop position, and naming both a collection and a cardinality.
func TestParseMultiInstanceErrors(t *testing.T) {
	// mi builds a service task's <multiInstanceLoopCharacteristics> body from the
	// given attributes/children, wrapped in a minimal runnable process.
	wrap := func(mi string) string {
		return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
		         xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
		  <process id="p" isExecutable="true">
		    <startEvent id="s"/>
		    <serviceTask id="work">
		      <extensionElements><zeebe:taskDefinition type="worker"/></extensionElements>
		      ` + mi + `
		    </serviceTask>
		    <endEvent id="e"/>
		    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
		    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
		  </process>
		</definitions>`
	}
	cases := map[string]string{
		"bad inputCollection": `<multiInstanceLoopCharacteristics><extensionElements>
			<zeebe:loopCharacteristics inputCollection="=)("/></extensionElements></multiInstanceLoopCharacteristics>`,
		"bad loopCardinality": `<multiInstanceLoopCharacteristics>
			<loopCardinality>=)(</loopCardinality></multiInstanceLoopCharacteristics>`,
		"bad outputElement": `<multiInstanceLoopCharacteristics><extensionElements>
			<zeebe:loopCharacteristics inputCollection="=items" outputElement="=)("/></extensionElements></multiInstanceLoopCharacteristics>`,
		"bad completionCondition": `<multiInstanceLoopCharacteristics>
			<completionCondition>=)(</completionCondition><extensionElements>
			<zeebe:loopCharacteristics inputCollection="=items"/></extensionElements></multiInstanceLoopCharacteristics>`,
		"both collection and cardinality": `<multiInstanceLoopCharacteristics>
			<loopCardinality>3</loopCardinality><extensionElements>
			<zeebe:loopCharacteristics inputCollection="=items"/></extensionElements></multiInstanceLoopCharacteristics>`,
	}
	for name, mi := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(wrap(mi))); err == nil {
				t.Fatalf("Parse: want an error for %s, got nil", name)
			}
		})
	}
}

// TestSetMultiInstanceInvalidNode covers the builder guard: marking a non-existent
// node is a no-op, not a panic.
func TestSetMultiInstanceInvalidNode(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	b.SetMultiInstance(999, false, "item", "", nil, nil, nil, nil) // out of range → ignored
	if len(b.multiInstances) != 0 {
		t.Errorf("multiInstances = %d, want 0 (invalid node ignored)", len(b.multiInstances))
	}
}
