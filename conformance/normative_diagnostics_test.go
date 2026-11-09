package conformance

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// CON2 negative diagnostics for registry row FLOW-14 (model validation). The
// older TestNegativeModels only checks that the compiler refuses; these tests pin
// *which* diagnostic it gives, so a refusal for the wrong reason, or a lost
// location, is a failure rather than a pass. A rejection is diagnostic evidence
// only, never execution-support credit.
//
// Three groups, kept apart on purpose:
//   - located diagnostics: graph-wide (stage 5) rules that carry a stable rule
//     slug and the offending element id;
//   - coarse diagnostics: element-local (stages 1-4) faults that are refused but
//     reported under the generic "compile" rule with no element field, the id
//     appearing only in the message text — a known gap in location reporting;
//   - accepted violations: models the specification forbids that atlas deploys
//     without an error today. They are characterised here so the gap stays
//     visible; closing one is a deploy-behaviour change for an ADR-gated slice.

// con2Problems returns the modeler-validation problems for a model, failing the
// test if validation itself errors.
func con2Problems(t *testing.T, xml []byte) []compiler.Problem {
	t.Helper()
	problems, err := compiler.ValidateModel(bytes.NewReader(xml))
	if err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	return problems
}

// TestCON2InvalidModelsHaveLocatedDiagnostics asserts rule, element and error
// severity both on the deploy path (Parse, as a ValidationError) and on the
// modeler path (ValidateModel).
func TestCON2InvalidModelsHaveLocatedDiagnostics(t *testing.T) {
	S, F, W := con2Script, con2Flow, con2Wait
	cases := []struct {
		name, rule, element string
		xml                 []byte
	}{
		{"flow-crosses-scope", compiler.RuleFlowCrossScope, "inner", con2Defs("",
			`<startEvent id="s"/><subProcess id="sub"><startEvent id="ss"/>`+S("inner")+F("i1", "ss", "inner")+
				`</subProcess><endEvent id="e"/>`+F("f1", "s", "sub")+F("f2", "inner", "e"))},
		{"gateway-no-outgoing", compiler.RuleGatewayNoOutgoing, "gw", con2Defs("",
			`<startEvent id="s"/><exclusiveGateway id="gw"/>`+F("f1", "s", "gw"))},
		{"gateway-no-incoming", compiler.RuleGatewayNoIncoming, "gw", con2Defs("",
			`<startEvent id="s"/><endEvent id="e"/><parallelGateway id="gw"/><endEvent id="e2"/>`+
				F("f1", "s", "e")+F("f2", "gw", "e2"))},
		{"boundary-with-incoming-flow", compiler.RuleBoundaryIncomingFlow, "b", con2Defs("",
			`<startEvent id="s"/>`+W("t")+`<boundaryEvent id="b" attachedToRef="t"><timerEventDefinition>`+
				`<timeDuration>PT1M</timeDuration></timerEventDefinition></boundaryEvent><endEvent id="e"/><endEvent id="e2"/>`+
				F("f1", "s", "t")+F("f2", "t", "e")+F("f3", "b", "e2")+F("f4", "s", "b"))},
		{"cancel-end-outside-transaction", compiler.RuleCancelEndOutsideTransaction, "c", con2Defs("",
			`<startEvent id="s"/><endEvent id="c"><cancelEventDefinition/></endEvent>`+F("f1", "s", "c"))},
		{"cancel-boundary-on-non-transaction", compiler.RuleCancelBoundaryInvalidHost, "b", con2Defs("",
			`<startEvent id="s"/>`+W("t")+`<boundaryEvent id="b" attachedToRef="t"><cancelEventDefinition/></boundaryEvent>`+
				`<endEvent id="e"/><endEvent id="e2"/>`+F("f1", "s", "t")+F("f2", "t", "e")+F("f3", "b", "e2"))},
		{"event-gateway-targets-task", compiler.RuleEventGatewayTarget, "gw", con2Defs("",
			`<startEvent id="s"/><eventBasedGateway id="gw"/>`+S("task")+`<endEvent id="e"/>`+
				F("f1", "s", "gw")+F("f2", "gw", "task")+F("f3", "task", "e"))},
		{"timer-start-schedule-unresolvable", compiler.RuleTimerStartSchedule, "s", con2Defs("",
			`<startEvent id="s"><timerEventDefinition><timeDuration>= "nonsense"</timeDuration></timerEventDefinition>`+
				`</startEvent><endEvent id="e"/>`+F("f1", "s", "e"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compiler.Parse(defKey, 1, bytes.NewReader(tc.xml))
			var diagnostics *compiler.ValidationError
			if !errors.As(err, &diagnostics) {
				t.Fatalf("Parse: want ValidationError carrying %s, got %v", tc.rule, err)
			}
			if !con2HasProblem(diagnostics.Problems, tc.rule, tc.element, compiler.SeverityError) {
				t.Fatalf("deploy diagnostics %+v lack error %s on %q", diagnostics.Problems, tc.rule, tc.element)
			}
			if problems := con2Problems(t, tc.xml); !con2HasProblem(problems, tc.rule, tc.element, compiler.SeverityError) {
				t.Fatalf("modeler diagnostics %+v lack error %s on %q", problems, tc.rule, tc.element)
			}
		})
	}
}

func con2HasProblem(problems []compiler.Problem, rule, element string, severity compiler.Severity) bool {
	for _, p := range problems {
		if p.Rule == rule && p.Element == element && p.Severity == severity && strings.TrimSpace(p.Message) != "" {
			return true
		}
	}
	return false
}

// TestCON2InvalidModelsHaveCoarseDiagnostics covers element-local faults: the
// deploy is refused, but the modeler problem carries only the generic "compile"
// rule and no element field; the offending id is in the message text alone. The
// assertion on the empty element field is a tripwire: when location reporting
// improves, this test fails and the case moves to the located table above, and
// registry row FLOW-14 is updated with it.
func TestCON2InvalidModelsHaveCoarseDiagnostics(t *testing.T) {
	F := con2Flow
	cases := []struct {
		name, mentions string
		xml            []byte
	}{
		{"duplicate-id", `"s"`, con2Defs("", `<startEvent id="s"/><endEvent id="s"/>`+F("f1", "s", "s"))},
		{"unknown-error-ref", `"e"`, con2Defs("",
			`<startEvent id="s"/><endEvent id="e"><errorEventDefinition errorRef="Nope"/></endEvent>`+F("f1", "s", "e"))},
		{"unknown-escalation-ref", `"e"`, con2Defs("",
			`<startEvent id="s"/><endEvent id="e"><escalationEventDefinition escalationRef="Nope"/></endEvent>`+F("f1", "s", "e"))},
		{"unknown-signal-ref", `"c"`, con2Defs("",
			`<startEvent id="s"/><intermediateCatchEvent id="c"><signalEventDefinition signalRef="Nope"/></intermediateCatchEvent>`+
				`<endEvent id="e"/>`+F("f1", "s", "c")+F("f2", "c", "e"))},
		{"link-throw-without-catch", `"l"`, con2Defs("",
			`<startEvent id="s"/><intermediateThrowEvent id="l"><linkEventDefinition name="X"/></intermediateThrowEvent>`+F("f1", "s", "l"))},
		{"unparsable-flow-condition", `"c"`, con2Defs("",
			`<startEvent id="s"/><exclusiveGateway id="gw" default="d"/><endEvent id="e"/><endEvent id="e2"/>`+
				F("f1", "s", "gw")+F("d", "gw", "e2")+
				`<sequenceFlow id="c" sourceRef="gw" targetRef="e"><conditionExpression>= (((</conditionExpression></sequenceFlow>`)},
	}
	// The registered negative fixtures belong to the same group.
	mentions := map[string]string{
		"neg-dangling-flow":     `"f1"`,
		"neg-boundary-bad-host": `"b"`,
		"neg-unknown-message":   `"await"`,
		"neg-loop-unbounded":    `"step"`,
	}
	for _, nm := range NegativeModels {
		xml, err := nm.load()
		if err != nil {
			t.Fatalf("load %s: %v", nm.Name, err)
		}
		id, ok := mentions[nm.Name]
		if !ok {
			t.Fatalf("negative model %s has no expected diagnostic here; add one", nm.Name)
		}
		cases = append(cases, struct {
			name, mentions string
			xml            []byte
		}{nm.Name, id, xml})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compiler.Parse(defKey, 1, bytes.NewReader(tc.xml)); err == nil {
				t.Fatal("model compiled but must be refused")
			}
			problems := con2Problems(t, tc.xml)
			if len(problems) != 1 {
				t.Fatalf("want exactly one modeler problem, got %+v", problems)
			}
			p := problems[0]
			if p.Rule != compiler.RuleCompile || p.Severity != compiler.SeverityError {
				t.Fatalf("problem %+v: want error under rule %q", p, compiler.RuleCompile)
			}
			if !strings.Contains(p.Message, tc.mentions) {
				t.Fatalf("message %q does not name %s", p.Message, tc.mentions)
			}
			if p.Element != "" {
				t.Fatalf("diagnostic now carries element %q: move %s to the located table and update FLOW-14",
					p.Element, tc.name)
			}
		})
	}
}

// TestCON2ModelingSmellsAreWarningsNotRefusals pins two diagnostics that must not
// block a deploy: unreachable elements and an exclusive gateway whose outgoing
// flows are all conditional.
func TestCON2ModelingSmellsAreWarningsNotRefusals(t *testing.T) {
	S, F := con2Script, con2Flow
	cases := []struct {
		name, rule, element string
		xml                 []byte
	}{
		{"unreachable-activity", compiler.RuleReachability, "orphan", con2Defs("",
			`<startEvent id="s"/><endEvent id="e"/>`+S("orphan")+F("f1", "s", "e"))},
		{"exclusive-without-default", compiler.RuleGatewayMissingDefault, "gw", con2Defs("",
			`<startEvent id="s"/><exclusiveGateway id="gw"/><endEvent id="e"/>`+F("f1", "s", "gw")+
				`<sequenceFlow id="f2" sourceRef="gw" targetRef="e"><conditionExpression>= true</conditionExpression></sequenceFlow>`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compiler.Parse(defKey, 1, bytes.NewReader(tc.xml)); err != nil {
				t.Fatalf("a warning must not refuse the deploy: %v", err)
			}
			if problems := con2Problems(t, tc.xml); !con2HasProblem(problems, tc.rule, tc.element, compiler.SeverityWarning) {
				t.Fatalf("modeler diagnostics %+v lack warning %s on %q", problems, tc.rule, tc.element)
			}
		})
	}
}

// TestCON2KnownAcceptedSpecViolations characterises models that OMG BPMN 2.0.2
// forbids but atlas deploys without an error today. Clause wording is provisional
// pending the CON0 per-clause review:
//   - a start event must not be the target of a sequence flow;
//   - an end event must not be the source of a sequence flow;
//   - a gateway's default must be one of its own outgoing sequence flows.
//
// The runtime consequences are asserted too, because they are what makes each gap
// matter: the outgoing flow of an end event is silently never taken, and a default
// that names a non-outgoing flow is ignored, so the gateway parks with an incident
// while the modeler reported only the generic missing-default warning. A start
// event with an incoming flow is only compile-checked here; executed, the loop it
// builds runs until the engine's per-token step budget raises an incident.
//
// When the compiler starts refusing one of these, this test fails by design: move
// the case to TestCON2InvalidModelsHaveLocatedDiagnostics and update FLOW-14.
func TestCON2KnownAcceptedSpecViolations(t *testing.T) {
	S, F := con2Script, con2Flow
	startWithIncoming := con2Defs("", `<startEvent id="s"/>`+S("a")+`<endEvent id="e"/>`+
		F("f1", "s", "a")+F("f2", "a", "e")+F("f3", "a", "s"))
	endWithOutgoing := con2Defs("", `<startEvent id="s"/><endEvent id="e"/>`+S("a")+`<endEvent id="e2"/>`+
		F("f1", "s", "e")+F("f2", "e", "a")+F("f3", "a", "e2"))
	defaultNotOutgoing := con2Defs("", `<startEvent id="s"/><exclusiveGateway id="gw" default="f1"/><endEvent id="e"/>`+
		F("f1", "s", "gw")+`<sequenceFlow id="f2" sourceRef="gw" targetRef="e"><conditionExpression>= false</conditionExpression></sequenceFlow>`)

	for _, tc := range []struct {
		name string
		xml  []byte
	}{
		{"start-event-with-incoming-flow", startWithIncoming},
		{"end-event-with-outgoing-flow", endWithOutgoing},
		{"default-flow-not-outgoing", defaultNotOutgoing},
	} {
		xml := tc.xml
		t.Run(tc.name+"/deploys", func(t *testing.T) {
			if _, err := compiler.Parse(defKey, 1, bytes.NewReader(xml)); err != nil {
				t.Fatalf("compiler now refuses this violation (%v): move it to the located table and update FLOW-14", err)
			}
			for _, p := range con2Problems(t, xml) {
				if p.Severity == compiler.SeverityError {
					t.Fatalf("modeler now reports error %+v: move the case and update FLOW-14", p)
				}
			}
		})
	}

	t.Run("end-event-with-outgoing-flow/flow-never-taken", func(t *testing.T) {
		got := con2Run(t, endWithOutgoing)
		if got.State != model.PICompleted {
			t.Fatalf("state %v, want completed", got.State)
		}
		con2Expect(t, "FLOW-14", got, map[string]int{"e": 1, "a": 0, "e2": 0})
	})

	t.Run("default-flow-not-outgoing/gateway-parks", func(t *testing.T) {
		// The only diagnostic is the generic warning, which does not say that the
		// declared default is not one of the gateway's outgoing flows.
		if problems := con2Problems(t, defaultNotOutgoing); !con2HasProblem(problems,
			compiler.RuleGatewayMissingDefault, "gw", compiler.SeverityWarning) {
			t.Fatalf("modeler diagnostics %+v lack the missing-default warning on gw", problems)
		}
		got := con2Run(t, defaultNotOutgoing)
		if got.State != model.PIActive {
			t.Fatalf("state %v, want active", got.State)
		}
		con2Inspect(t, defaultNotOutgoing, nil, func(store *state.Store, cp *compiler.CompiledProcess) {
			var on []string
			if err := store.Incidents(func(_ uint64, incident *model.IncidentValue) error {
				on = append(on, cp.ElementBpmnId(incident.ElementId))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(on) != 1 || on[0] != "gw" {
				t.Fatalf("incidents on %v, want exactly one on gw", on)
			}
		})
	})
}
