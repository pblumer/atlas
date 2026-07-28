package compiler

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// mustCond compiles a FEEL condition for a test flow, failing the test on a bad
// expression (the parser guarantees these compile before validation runs).
func mustCond(t *testing.T, text string) *expr.Compiled {
	t.Helper()
	c, err := expr.CompileAuto(text)
	if err != nil {
		t.Fatalf("compile %q: %v", text, err)
	}
	return c
}

// hasProblem reports whether ps contains a problem with the given rule and
// severity (and, if element != "", that element).
func hasProblem(ps []Problem, rule string, sev Severity, element string) bool {
	for _, p := range ps {
		if p.Rule == rule && p.Severity == sev && (element == "" || p.Element == element) {
			return true
		}
	}
	return false
}

// TestValidateCleanProcess: a well-formed linear process yields no problems, so
// validation does not cry wolf on a correct model.
func TestValidateCleanProcess(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(linearBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ps := Validate(cp); len(ps) != 0 {
		t.Errorf("Validate(clean) = %+v, want no problems", ps)
	}
}

// TestValidateReachabilityWarnsOnDeadCode: an element with no incoming flow is
// unreachable dead code — a warning (it cannot break the reachable process), and
// deploy still succeeds.
func TestValidateReachabilityWarnsOnDeadCode(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <task id="live"/>
	    <task id="orphan"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="live"/>
	    <sequenceFlow id="f2" sourceRef="live" targetRef="e"/>
	  </process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v (a warning must not refuse the deploy)", err)
	}
	ps := Validate(cp)
	if !hasProblem(ps, RuleReachability, SeverityWarning, "orphan") {
		t.Errorf("want a reachability warning on \"orphan\", got %+v", ps)
	}
	// The live path must not be flagged.
	if hasProblem(ps, RuleReachability, SeverityWarning, "live") {
		t.Errorf("\"live\" wrongly flagged unreachable: %+v", ps)
	}
	if HasErrors(ps) {
		t.Errorf("dead code must not produce an error: %+v", ps)
	}
}

// TestValidateReachabilityFollowsBoundaryEvents: a boundary event (and the branch
// leaving it) is reachable whenever its host is, so a boundary-escalation path is
// not falsely flagged as dead code.
func TestValidateReachabilityFollowsBoundaryEvents(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(boundaryTimerBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ps := Validate(cp); len(ps) != 0 {
		t.Errorf("boundary model produced problems: %+v", ps)
	}
}

// TestValidateGatewayNoOutgoing / NoIncoming: a gateway missing a routing side is
// an error that refuses the deploy.
func TestValidateGatewayStructuralErrors(t *testing.T) {
	// No outgoing: start → gw, and gw routes nowhere.
	b := NewBuilder(1, "p", 1)
	s := b.AddStartEvent()
	b.SetElementBpmnId(s, "s")
	gw := b.AddExclusiveGateway()
	b.SetElementBpmnId(gw, "gw")
	b.Connect(s, gw)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ps := Validate(cp)
	if !hasProblem(ps, RuleGatewayNoOutgoing, SeverityError, "gw") {
		t.Errorf("want a no-outgoing error on gw, got %+v", ps)
	}
	if !HasErrors(ps) {
		t.Error("a no-outgoing gateway must be an error")
	}

	// No incoming: gw has an outgoing flow but nothing reaches it.
	b2 := NewBuilder(1, "p", 1)
	s2 := b2.AddStartEvent()
	e2 := b2.AddEndEvent()
	gw2 := b2.AddParallelGateway()
	b2.SetElementBpmnId(gw2, "gw2")
	b2.Connect(s2, e2)
	b2.Connect(gw2, e2)
	cp2, err := b2.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ps := Validate(cp2); !hasProblem(ps, RuleGatewayNoIncoming, SeverityError, "gw2") {
		t.Errorf("want a no-incoming error on gw2, got %+v", ps)
	}
}

// TestValidateGatewayMissingDefault: an exclusive split whose outgoing flows are
// all conditional, with no default, warns (a runtime deadlock is possible but
// data-dependent) — and still deploys.
func TestValidateGatewayMissingDefault(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw"/>
	    <endEvent id="hi"/><endEvent id="lo"/>
	    <sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
	    <sequenceFlow id="f1" sourceRef="gw" targetRef="hi"><conditionExpression>= amount &gt; 100</conditionExpression></sequenceFlow>
	    <sequenceFlow id="f2" sourceRef="gw" targetRef="lo"><conditionExpression>= amount &lt;= 100</conditionExpression></sequenceFlow>
	  </process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v (missing-default is a warning, deploy must succeed)", err)
	}
	ps := Validate(cp)
	if !hasProblem(ps, RuleGatewayMissingDefault, SeverityWarning, "gw") {
		t.Errorf("want a missing-default warning on gw, got %+v", ps)
	}
	if HasErrors(ps) {
		t.Errorf("missing-default must not be an error: %+v", ps)
	}
}

// TestValidateGatewayDefaultSuppressesWarning: adding a default flow removes the
// missing-default warning (the coverage gap is closed).
func TestValidateGatewayDefaultSuppressesWarning(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw" default="f2"/>
	    <endEvent id="hi"/><endEvent id="lo"/>
	    <sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
	    <sequenceFlow id="f1" sourceRef="gw" targetRef="hi"><conditionExpression>= amount &gt; 100</conditionExpression></sequenceFlow>
	    <sequenceFlow id="f2" sourceRef="gw" targetRef="lo"/>
	  </process></definitions>`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ps := Validate(cp); hasProblem(ps, RuleGatewayMissingDefault, SeverityWarning, "gw") {
		t.Errorf("a gateway with a default must not warn: %+v", ps)
	}
}

// TestValidateGatewayMultipleDefaults: two default flows on one gateway is a
// contradiction — an error. Only the Builder can express it (the XML `default`
// attribute is single-valued).
func TestValidateGatewayMultipleDefaults(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	s := b.AddStartEvent()
	gw := b.AddExclusiveGateway()
	b.SetElementBpmnId(gw, "gw")
	a := b.AddTask()
	c := b.AddTask()
	b.Connect(s, gw)
	f1 := b.Connect(gw, a)
	f2 := b.Connect(gw, c)
	b.SetFlowDefault(f1)
	b.SetFlowDefault(f2)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ps := Validate(cp)
	if !hasProblem(ps, RuleGatewayMultipleDefault, SeverityError, "gw") {
		t.Errorf("want a multiple-default error on gw, got %+v", ps)
	}
}

// TestValidateBoundaryIncomingFlow: a boundary event is armed by its host, so an
// incoming sequence flow is structurally invalid — an error.
func TestValidateBoundaryIncomingFlow(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	s := b.AddStartEvent()
	host := b.AddServiceTask("work", 3)
	e := b.AddEndEvent()
	be := b.AddBoundaryTimerEvent(host, true, 1e9)
	b.SetElementBpmnId(be, "be")
	b.Connect(s, host)
	b.Connect(host, e)
	b.Connect(host, be) // illegal: a token flowing into the boundary event
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ps := Validate(cp); !hasProblem(ps, RuleBoundaryIncomingFlow, SeverityError, "be") {
		t.Errorf("want a boundary incoming-flow error on be, got %+v", ps)
	}
}

// TestValidateBoundaryInvalidHost: a boundary event attached to a non-activity (a
// start event) can never fire — an error.
func TestValidateBoundaryInvalidHost(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	s := b.AddStartEvent()
	e := b.AddEndEvent()
	be := b.AddBoundaryTimerEvent(s, true, 1e9) // attached to the start event
	b.SetElementBpmnId(be, "be")
	b.Connect(s, e)
	b.Connect(be, e)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ps := Validate(cp); !hasProblem(ps, RuleBoundaryInvalidHost, SeverityError, "be") {
		t.Errorf("want a boundary invalid-host error on be, got %+v", ps)
	}
}

// TestValidateCrossScopeFlow: a sequence flow whose endpoints live in different
// flow scopes is an error. The flat model can't produce one today, so the test
// constructs it by placing the target in a nested scope after Build (white-box).
func TestValidateCrossScopeFlow(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	s := b.AddStartEvent()
	b.SetElementBpmnId(s, "s")
	e := b.AddEndEvent()
	b.Connect(s, e)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Move the end event into a nested scope: the s→e flow now crosses a boundary.
	cp.nodes[e].FlowScope = 42
	if ps := Validate(cp); !hasProblem(ps, RuleFlowCrossScope, SeverityError, "s") {
		t.Errorf("want a cross-scope error anchored on s, got %+v", ps)
	}
}

// TestCompileRefusesDeployOnError: an error-severity problem propagates out of the
// XML compile path as a fatal ValidationError, so a broken model is refused at
// deploy — preserving the compile-gate contract (invariant I5).
func TestCompileRefusesDeployOnError(t *testing.T) {
	// An exclusive gateway with an incoming flow but no outgoing flow: unroutable.
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw"/>
	    <sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
	  </process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse of an unroutable gateway = nil error, want a fatal validation error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error %v (%T) is not a *ValidationError", err, err)
	}
	if !HasErrors(ve.Problems) {
		t.Errorf("ValidationError carries no error-severity problem: %+v", ve.Problems)
	}
	if !strings.Contains(err.Error(), RuleGatewayNoOutgoing) {
		t.Errorf("error text %q should name the failing rule", err.Error())
	}
}

// TestValidationErrorMessage: Error() renders the error-severity findings and
// skips warnings, so the fatal message names only what actually refuses the deploy.
func TestValidationErrorMessage(t *testing.T) {
	ve := &ValidationError{Problems: []Problem{
		{Severity: SeverityWarning, Rule: RuleReachability, Message: "dead code"},
		{Severity: SeverityError, Rule: RuleGatewayNoOutgoing, Message: "gw has no outgoing sequence flow"},
	}}
	msg := ve.Error()
	if !strings.Contains(msg, RuleGatewayNoOutgoing) || !strings.Contains(msg, "no outgoing") {
		t.Errorf("Error() = %q, want the error-severity finding named", msg)
	}
	if strings.Contains(msg, "dead code") {
		t.Errorf("Error() = %q, must not include the warning", msg)
	}
}

// TestHasErrors: the error/warning discriminator the deploy gate turns on.
func TestHasErrors(t *testing.T) {
	if HasErrors(nil) {
		t.Error("HasErrors(nil) = true, want false")
	}
	if HasErrors([]Problem{{Severity: SeverityWarning}}) {
		t.Error("HasErrors(warnings only) = true, want false")
	}
	if !HasErrors([]Problem{{Severity: SeverityWarning}, {Severity: SeverityError}}) {
		t.Error("HasErrors(with an error) = false, want true")
	}
}
