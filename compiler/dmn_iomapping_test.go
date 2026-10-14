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
		if _, err := decisionInputMappings(strictFEEL, "decide", []xmlZeebeIOMapInput{{Source: "= x"}}); err == nil {
			t.Fatal("input mapping with empty target: got nil error, want an error")
		}
	})
	t.Run("empty source", func(t *testing.T) {
		if _, err := decisionInputMappings(strictFEEL, "decide", []xmlZeebeIOMapInput{{Target: "Season", Source: " = "}}); err == nil {
			t.Fatal("input mapping with empty source: got nil error, want an error")
		}
	})
	t.Run("uncompilable source", func(t *testing.T) {
		if _, err := decisionInputMappings(strictFEEL, "decide", []xmlZeebeIOMapInput{{Target: "Season", Source: "= 1 +"}}); err == nil {
			t.Fatal("input mapping with a bad source: got nil error, want an error")
		}
	})
	t.Run("no mappings yields nil", func(t *testing.T) {
		m, err := decisionInputMappings(strictFEEL, "decide", nil)
		if err != nil || m != nil {
			t.Fatalf("decisionInputMappings(strictFEEL, nil) = %v, %v, want nil, nil", m, err)
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

// TestBusinessRuleTaskBinding proves the compiler reads zeebe:calledDecision
// bindingType onto the detail (ADR-0063): "deployment" pins, anything else
// (including the default) is latest.
func TestBusinessRuleTaskBinding(t *testing.T) {
	brtBinding := func(bindingAttr string) DecisionBinding {
		bpmn := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
		  <process id="p" isExecutable="true"><startEvent id="s"/>
		  <businessRuleTask id="d"><extensionElements><calledDecision decisionId="Dish"` + bindingAttr + `/></extensionElements></businessRuleTask>
		  <endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="d"/><sequenceFlow id="f2" sourceRef="d" targetRef="e"/></process></definitions>`
		cp, err := Parse(1, 1, strings.NewReader(bpmn))
		if err != nil {
			t.Fatalf("Parse(%q): %v", bindingAttr, err)
		}
		for i := 0; i < len(cp.nodes); i++ {
			if cp.nodes[i].Type == TypeBusinessRuleTask {
				return cp.BusinessRuleTask(cp.nodes[i].Detail).Binding
			}
		}
		t.Fatal("no business rule task compiled")
		return BindingLatest
	}
	if b := brtBinding(``); b != BindingLatest {
		t.Errorf("default binding = %d, want BindingLatest", b)
	}
	if b := brtBinding(` bindingType="latest"`); b != BindingLatest {
		t.Errorf("latest binding = %d, want BindingLatest", b)
	}
	if b := brtBinding(` bindingType="deployment"`); b != BindingDeployment {
		t.Errorf("deployment binding = %d, want BindingDeployment", b)
	}
}

// TestBusinessRuleTaskVersionBinding covers the two bindings
// ADR-0423 adds on the compiler side: a
// fixed version (atlas:version), read onto the detail, and Camunda's versionTag, which
// used to be read as latest and is now refused.
func TestBusinessRuleTaskVersionBinding(t *testing.T) {
	parse := func(attrs string) (*CompiledProcess, error) {
		bpmn := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
		  <process id="p" isExecutable="true"><startEvent id="s"/>
		  <businessRuleTask id="d"><extensionElements><calledDecision decisionId="Dish"` + attrs + `/></extensionElements></businessRuleTask>
		  <endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="d"/><sequenceFlow id="f2" sourceRef="d" targetRef="e"/></process></definitions>`
		return Parse(1, 1, strings.NewReader(bpmn))
	}
	detail := func(cp *CompiledProcess) *BusinessRuleTaskDetail {
		for i := 0; i < len(cp.nodes); i++ {
			if cp.nodes[i].Type == TypeBusinessRuleTask {
				return cp.BusinessRuleTask(cp.nodes[i].Detail)
			}
		}
		t.Fatal("no business rule task compiled")
		return nil
	}

	for _, attrs := range []string{` atlas:version="3"`, ` bindingType="latest" atlas:version="3"`} {
		cp, err := parse(attrs)
		if err != nil {
			t.Fatalf("Parse(%s): %v", attrs, err)
		}
		if d := detail(cp); d.Binding != BindingVersion || d.Version != 3 {
			t.Errorf("Parse(%s): binding %v version %d, want version 3", attrs, d.Binding, d.Version)
		}
		if refs := cp.VersionBoundDecisions(); len(refs) != 1 || refs[0] != (DecisionVersionRef{DecisionID: "Dish", Version: 3}) {
			t.Errorf("Parse(%s): VersionBoundDecisions = %+v, want Dish v3", attrs, refs)
		}
		if got := cp.LatestBoundDecisions(); len(got) != 0 {
			t.Errorf("Parse(%s): LatestBoundDecisions = %v, want none — a fixed version is not latest", attrs, got)
		}
		if got := cp.BundleBoundDecisions(); len(got) != 0 {
			t.Errorf("Parse(%s): BundleBoundDecisions = %v, want none — it needs no model bundled", attrs, got)
		}
	}

	for _, bad := range []struct{ attrs, want string }{
		{` atlas:version="0"`, "not a deployed version number"},
		{` atlas:version="v3"`, "not a deployed version number"},
		{` bindingType="deployment" atlas:version="3"`, "cannot be combined"},
		{` bindingType="versionTag" versionTag="2027"`, "versionTag"},
	} {
		if _, err := parse(bad.attrs); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("Parse(%s) = %v, want a refusal mentioning %q", bad.attrs, err, bad.want)
		}
	}
}
