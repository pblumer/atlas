package compiler

import (
	"strings"
	"testing"
)

// businessRuleWithMappingBPMN references a decision with a result variable and a
// variable-driven input mapping, alongside a constant static input — the full
// shape the DMN worker consumes.
const businessRuleWithMappingBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="dinner" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="decide">
      <extensionElements>
        <calledDecision decisionId="Dish" resultVariable="dish" retries="5"/>
        <decisionInput name="Guests" value="8"/>
        <ioMapping>
          <input source="= order.season" target="Season"/>
        </ioMapping>
      </extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="e"/>
  </process>
</definitions>`

// TestBusinessRuleTaskParsesIOMapping proves the compiler wires a business rule
// task's result variable and input mappings: the result variable is interned, the
// static input survives as a constant base, and the mapping's FEEL source is
// compiled with the variables it reads discovered.
func TestBusinessRuleTaskParsesIOMapping(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(businessRuleWithMappingBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var detail *BusinessRuleTaskDetail
	for i := 0; i < len(cp.nodes); i++ {
		if cp.nodes[i].Type == TypeBusinessRuleTask {
			detail = cp.BusinessRuleTask(cp.nodes[i].Detail)
		}
	}
	if detail == nil {
		t.Fatal("no business rule task compiled")
	}
	if got := cp.Intern(detail.DecisionId); got != "Dish" {
		t.Errorf("decisionId = %q, want Dish", got)
	}
	if got := cp.Intern(detail.ResultVar); got != "dish" {
		t.Errorf("resultVar = %q, want dish", got)
	}
	if detail.Retries != 5 {
		t.Errorf("retries = %d, want 5", detail.Retries)
	}
	if len(detail.InputMappings) != 1 {
		t.Fatalf("input mappings = %d, want 1", len(detail.InputMappings))
	}
	m := detail.InputMappings[0]
	if m.Target != "Season" {
		t.Errorf("mapping target = %q, want Season", m.Target)
	}
	if m.Source == nil {
		t.Fatal("mapping source expression is nil")
	}
	if in := m.Source.Inputs(); len(in) != 1 || in[0] != "order" {
		t.Errorf("mapping source inputs = %v, want [order]", in)
	}
	// The static input remains as a constant base the mapping does not name.
	if detail.Inputs < 0 {
		t.Error("static input JSON was dropped; want the constant Guests base retained")
	}
}

// TestBusinessRuleTaskIOMappingErrors covers the deploy-time rejections: an input
// mapping with no target, and one whose source is not a compilable FEEL
// expression.
func TestBusinessRuleTaskIOMappingErrors(t *testing.T) {
	t.Run("empty target", func(t *testing.T) {
		if _, err := decisionInputMappings("decide", []xmlZeebeIOMapInput{{Source: "= x"}}); err == nil {
			t.Fatal("input mapping with empty target: got nil error, want an error")
		}
	})
	t.Run("empty source", func(t *testing.T) {
		if _, err := decisionInputMappings("decide", []xmlZeebeIOMapInput{{Target: "Season", Source: " = "}}); err == nil {
			t.Fatal("input mapping with empty source: got nil error, want an error")
		}
	})
	t.Run("uncompilable source", func(t *testing.T) {
		if _, err := decisionInputMappings("decide", []xmlZeebeIOMapInput{{Target: "Season", Source: "= 1 +"}}); err == nil {
			t.Fatal("input mapping with a bad source: got nil error, want an error")
		}
	})
	t.Run("no mappings yields nil", func(t *testing.T) {
		m, err := decisionInputMappings("decide", nil)
		if err != nil || m != nil {
			t.Fatalf("decisionInputMappings(nil) = %v, %v, want nil, nil", m, err)
		}
	})
}

// TestParseRejectsBadInputMapping proves a business rule task with an
// uncompilable io-mapping source fails the whole parse (deploy), surfacing the
// error through compileProcess rather than deferring it to runtime.
func TestParseRejectsBadInputMapping(t *testing.T) {
	const bad = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="decide">
      <extensionElements>
        <calledDecision decisionId="Dish"/>
        <ioMapping><input source="= 1 +" target="Season"/></ioMapping>
      </extensionElements>
    </businessRuleTask>
    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
  </process>
</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(bad)); err == nil {
		t.Fatal("Parse with an uncompilable input-mapping source: got nil error, want an error")
	}
}
