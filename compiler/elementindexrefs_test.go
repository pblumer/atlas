package compiler

import (
	"strings"
	"testing"
)

// The three static reference enumerations — call activities, worker references and
// business-rule decisions — each walk the node table by index and report the node's
// *string* BPMN id. The index itself is discarded, and it is the only thing that can
// join a reference to a maintained per-element counter: ADR-0080's counters are keyed
// by (definition, element index), never by the id a modeller typed.
//
// So these tests pin the index onto the references, and pin it against the index
// found independently from the BPMN id — not merely against itself. That is the one
// property that makes it trustworthy: an index naming a different element than the id
// beside it would be worse than none, because it would attribute one element's traffic
// to another (ADR-0400).

// TestCallActivityRefCarriesElementIndex checks that a call activity reference names
// the node it came from by index as well as by id.
func TestCallActivityRefCarriesElementIndex(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(callActivityXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	refs := cp.CallActivities()
	if len(refs) != 1 {
		t.Fatalf("CallActivities() = %d refs, want 1", len(refs))
	}
	if want := nodeIndexOf(t, cp, refs[0].ElementId); refs[0].ElementIndex != want {
		t.Errorf("ElementIndex = %d, want %d — the index does not name the element the ref does",
			refs[0].ElementIndex, want)
	}
}

// connectorRefIndexXML has two worker references so the test can catch an index that
// is merely *a* valid node rather than the right one: with one element, a hardcoded
// zero would pass.
const connectorRefIndexXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="first">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="alpha" to="a@example.com" subject="a" body="a"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="second">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="beta" to="b@example.com" subject="b" body="b"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="first"/>
    <bpmn:sequenceFlow id="f2" sourceRef="first" targetRef="second"/>
    <bpmn:sequenceFlow id="f3" sourceRef="second" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestConnectorRefCarriesElementIndex checks the same for worker references, over two
// elements so a constant index cannot pass.
func TestConnectorRefCarriesElementIndex(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(connectorRefIndexXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	refs := cp.ConnectorRefs()
	if len(refs) != 2 {
		t.Fatalf("ConnectorRefs() = %d refs, want 2", len(refs))
	}
	seen := map[int32]string{}
	for _, r := range refs {
		if want := nodeIndexOf(t, cp, r.ElementId); r.ElementIndex != want {
			t.Errorf("%s: ElementIndex = %d, want %d", r.ElementId, r.ElementIndex, want)
		}
		if prev, dup := seen[r.ElementIndex]; dup {
			t.Errorf("index %d used by both %q and %q — two references cannot share an element",
				r.ElementIndex, prev, r.ElementId)
		}
		seen[r.ElementIndex] = r.ElementId
	}
}

// decisionRefIndexXML uses one decision from two business rule tasks. That is the case
// BusinessRuleDecisions cannot serve: it de-duplicates by decision id, which is right
// for the deploy-time gate and wrong for a traversal count, because the two tasks are
// two places the decision is reached from and the count is their sum.
const decisionRefIndexXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="check-one">
      <extensionElements><zeebe:calledDecision decisionId="eligibility" resultVariable="r1"/></extensionElements>
    </businessRuleTask>
    <businessRuleTask id="check-two">
      <extensionElements><zeebe:calledDecision decisionId="eligibility" resultVariable="r2"/></extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="check-one"/>
    <sequenceFlow id="f2" sourceRef="check-one" targetRef="check-two"/>
    <sequenceFlow id="f3" sourceRef="check-two" targetRef="e"/>
  </process>
</definitions>`

// TestBusinessRuleDecisionRefsKeepsEveryElement checks that the new enumeration reports
// one reference per business rule task rather than one per decision, and that the old
// de-duplicating enumeration is unchanged — the deploy-time gate reads it.
func TestBusinessRuleDecisionRefsKeepsEveryElement(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(decisionRefIndexXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if ids := cp.BusinessRuleDecisions(); len(ids) != 1 || ids[0] != "eligibility" {
		t.Errorf("BusinessRuleDecisions() = %v, want one deduplicated [eligibility]", ids)
	}

	refs := cp.BusinessRuleDecisionRefs()
	if len(refs) != 2 {
		t.Fatalf("BusinessRuleDecisionRefs() = %d refs, want 2 — one per task, not one per decision", len(refs))
	}
	wantElements := map[string]bool{"check-one": true, "check-two": true}
	for _, r := range refs {
		if r.DecisionId != "eligibility" {
			t.Errorf("DecisionId = %q, want eligibility", r.DecisionId)
		}
		id := cp.ElementBpmnId(r.ElementIndex)
		if !wantElements[id] {
			t.Errorf("ElementIndex %d names %q, want one of the two business rule tasks", r.ElementIndex, id)
		}
		delete(wantElements, id)
	}
	if len(wantElements) != 0 {
		t.Errorf("no reference for %v", wantElements)
	}
}
