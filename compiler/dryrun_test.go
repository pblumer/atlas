package compiler

import (
	"strings"
	"testing"
)

// TestValidateModelCleanProcess: a well-formed model yields no problems, so the
// Problems panel (ADR-0026) shows an empty list for a correct diagram.
func TestValidateModelCleanProcess(t *testing.T) {
	ps, err := ValidateModel(strings.NewReader(linearBPMN))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if len(ps) != 0 {
		t.Errorf("ValidateModel(clean) = %+v, want no problems", ps)
	}
}

// TestValidateModelSurfacesDeadCodeWarning is the reported scenario: an end event
// with no incoming sequence flow. The deploy path allows it (a warning), but the
// dry run must return that warning so the panel can explain why the "Ende" symbol
// is inert — this is the whole point of the panel.
func TestValidateModelSurfacesDeadCodeWarning(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <task id="live"/>
	    <endEvent id="Ende"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="live"/>
	  </process></definitions>`
	ps, err := ValidateModel(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if !hasProblem(ps, RuleReachability, SeverityWarning, "Ende") {
		t.Errorf("want a reachability warning on \"Ende\", got %+v", ps)
	}
	if HasErrors(ps) {
		t.Errorf("a disconnected end event must not be an error: %+v", ps)
	}
}

// TestValidateModelSurfacesGraphError: an error-severity graph fault (a gateway
// with no outgoing flow) comes back in the list — the dry run reports errors and
// warnings alike, unlike the deploy path which stops at the first error.
func TestValidateModelSurfacesGraphError(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="gw"/>
	  </process></definitions>`
	ps, err := ValidateModel(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if !hasProblem(ps, RuleGatewayNoOutgoing, SeverityError, "gw") {
		t.Errorf("want a no-outgoing error on gw, got %+v", ps)
	}
	if !HasErrors(ps) {
		t.Errorf("a broken gateway must produce an error: %+v", ps)
	}
}

// TestValidateModelMalformedXML: a document that will not parse becomes one
// parse-severity error rather than a transport failure, so the panel renders it
// like any other problem.
func TestValidateModelMalformedXML(t *testing.T) {
	ps, err := ValidateModel(strings.NewReader("<definitions><process"))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if !hasProblem(ps, RuleParse, SeverityError, "") {
		t.Errorf("want a parse error, got %+v", ps)
	}
}

// TestValidateModelNoExecutableProcess: a model with no runnable process (no
// start event anywhere) is itself a parse-level error, matching ParseAll's
// refusal — the panel tells the author nothing will deploy.
func TestValidateModelNoExecutableProcess(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true"><task id="t"/></process></definitions>`
	ps, err := ValidateModel(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if !hasProblem(ps, RuleParse, SeverityError, "") {
		t.Errorf("want a parse error for a start-event-less model, got %+v", ps)
	}
}

// TestValidateModelPerProcessCompileError: a fault an earlier compile stage
// raises (here an unknown flow target) is reported as a compile-severity error
// instead of aborting the whole dry run, so one broken pool does not blind the
// panel to the rest of the model.
func TestValidateModelPerProcessCompileError(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="ghost"/>
	  </process></definitions>`
	ps, err := ValidateModel(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if !hasProblem(ps, RuleCompile, SeverityError, "") {
		t.Errorf("want a compile error for the unknown flow target, got %+v", ps)
	}
}
