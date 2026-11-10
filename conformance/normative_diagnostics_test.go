package conformance

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
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
//   - element-local diagnostics: faults stages 1-4 raise while the process is being
//     built, reported under the generic "compile" rule and, since ADR-0547 part 2,
//     with the offending element as well;
//   - forbidden flow shapes (ADR-0547): refused at deploy like the located group,
//     and brought back unchanged, with the problem reported, when a stored
//     definition is reloaded.

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
		// ADR-0547: the four shapes BPMN forbids that atlas used to deploy.
		{"start-event-with-incoming-flow", compiler.RuleStartIncomingFlow, "s", con2StartWithIncoming()},
		{"end-event-with-outgoing-flow", compiler.RuleEndOutgoingFlow, "e", con2EndWithOutgoing()},
		{"default-flow-not-outgoing", compiler.RuleGatewayDefaultNotOutgoing, "gw", con2DefaultNotOutgoing()},
		{"default-naming-another-gateways-flow", compiler.RuleGatewayDefaultNotOutgoing, "gw1", con2ForeignDefault()},
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

// TestCON2ElementLocalFaultsCarryTheirElement covers element-local faults: the
// deploy is refused, and the modeler problem carries the generic "compile" rule
// (a stable slug clients may group by, ADR-0547) together with the offending
// element, which the message names as well. Until ADR-0547 part 2 the element
// field was empty and the id was in the message text alone.
func TestCON2ElementLocalFaultsCarryTheirElement(t *testing.T) {
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
			if want := strings.Trim(tc.mentions, `"`); p.Element != want {
				t.Fatalf("problem %+v: element %q, want %q", p, p.Element, want)
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

// The four sequence-flow shapes OMG BPMN 2.0.2 forbids and atlas deployed without an
// error until ADR-0547. Clause wording is provisional pending the CON0 per-clause review.
func con2StartWithIncoming() []byte {
	return con2Defs("", `<startEvent id="s"/>`+con2Script("a")+`<endEvent id="e"/>`+
		con2Flow("f1", "s", "a")+con2Flow("f2", "a", "e")+con2Flow("f3", "a", "s"))
}

func con2EndWithOutgoing() []byte {
	return con2Defs("", `<startEvent id="s"/><endEvent id="e"/>`+con2Script("a")+`<endEvent id="e2"/>`+
		con2Flow("f1", "s", "e")+con2Flow("f2", "e", "a")+con2Flow("f3", "a", "e2"))
}

func con2DefaultNotOutgoing() []byte {
	return con2Defs("", `<startEvent id="s"/><exclusiveGateway id="gw" default="f1"/><endEvent id="e"/>`+
		con2Flow("f1", "s", "gw")+`<sequenceFlow id="f2" sourceRef="gw" targetRef="e"><conditionExpression>= false</conditionExpression></sequenceFlow>`)
}

// con2ForeignDefault: gw1's default names gw2's flow b_no. With x = 1 both of gw2's
// conditions are false, so gw2 must not route; the leaked default sent it to "no".
func con2ForeignDefault() []byte {
	cond := func(id, from, to, c string) string {
		return `<sequenceFlow id="` + id + `" sourceRef="` + from + `" targetRef="` + to +
			`"><conditionExpression>` + c + `</conditionExpression></sequenceFlow>`
	}
	return con2Defs("", `<startEvent id="s"/>`+con2Script("x")+`<exclusiveGateway id="gw1" default="b_no"/>`+
		`<exclusiveGateway id="gw2"/><endEvent id="yes"/><endEvent id="no"/><endEvent id="other"/>`+
		con2Flow("f1", "s", "x")+con2Flow("f2", "x", "gw1")+cond("a1", "gw1", "gw2", "= x = 1")+cond("a2", "gw1", "other", "= x = 5")+
		cond("b_no", "gw2", "no", "= x = 2")+cond("b_yes", "gw2", "yes", "= x = 3"))
}

// con2RunReloaded compiles the model the way a stored deployment is brought back
// (compiler.ReloadNamed, without the deploy gate), runs it live and through WAL
// replay, and returns the result with the problems the reload reported.
func con2RunReloaded(t *testing.T, xml []byte) (RunResult, []compiler.Problem) {
	t.Helper()
	cp, problems, err := compiler.ReloadNamed(defKey, 1, bytes.NewReader(xml), "p")
	if err != nil {
		t.Fatalf("ReloadNamed must bring a stored definition back: %v", err)
	}
	defs := []compiler.Deployable{{Process: cp}}
	dir := t.TempDir()
	live, err := executeLive(filepath.Join(dir, "live"), defs, cp, Start{}, nil)
	if err != nil {
		t.Fatalf("run reloaded definition: %v", err)
	}
	replayed, err := replayLog(filepath.Join(dir, "live"), filepath.Join(dir, "replay"), defs, cp)
	if err != nil {
		t.Fatalf("replay reloaded definition: %v", err)
	}
	if !equalResult(live, replayed) {
		t.Fatalf("replay diverged from live (I4):\n live   = %s\n replay = %s", live.Golden(), replayed.Golden())
	}
	return live, problems
}

// TestCON2ForbiddenFlowShapesReloadUnchanged pins the other half of ADR-0547: a stored
// definition with one of the four shapes, deployed before the rules existed, still
// reloads (ADR-0177, ADR-0393), reports the rule beside the process, and runs exactly as
// it did — including the behaviour that made the shape worth refusing. Repairing a
// stored definition on restart would change what its running instances do.
func TestCON2ForbiddenFlowShapesReloadUnchanged(t *testing.T) {
	cases := []struct {
		name, rule, element string
		xml                 []byte
		state               model.ProcessInstanceState
		visits              map[string]int
	}{
		// The flow after an end event is never taken.
		{"end-event-with-outgoing-flow", compiler.RuleEndOutgoingFlow, "e", con2EndWithOutgoing(),
			model.PICompleted, map[string]int{"e": 1, "a": 0, "e2": 0}},
		// A default naming the gateway's incoming flow is ignored: the gateway parks.
		{"default-flow-not-outgoing", compiler.RuleGatewayDefaultNotOutgoing, "gw", con2DefaultNotOutgoing(),
			model.PIActive, map[string]int{"gw": 1, "e": 0}},
		// A default naming another gateway's flow still mis-routes that gateway.
		{"default-naming-another-gateways-flow", compiler.RuleGatewayDefaultNotOutgoing, "gw1", con2ForeignDefault(),
			model.PICompleted, map[string]int{"gw2": 1, "no": 1, "yes": 0, "other": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, problems := con2RunReloaded(t, tc.xml)
			if !con2HasProblem(problems, tc.rule, tc.element, compiler.SeverityError) {
				t.Fatalf("reload problems %+v lack %s on %q", problems, tc.rule, tc.element)
			}
			if got.State != tc.state {
				t.Fatalf("state %v, want %v; path=%v", got.State, tc.state, got.Path)
			}
			con2Expect(t, "ADR-0547 reload", got, tc.visits)
		})
	}
	// The start-event loop is only reload-checked: run, it cycles until the per-token
	// step budget raises an incident, which is not what this test is about.
	t.Run("start-event-with-incoming-flow", func(t *testing.T) {
		cp, problems, err := compiler.ReloadNamed(defKey, 1, bytes.NewReader(con2StartWithIncoming()), "p")
		if err != nil || cp == nil {
			t.Fatalf("ReloadNamed must bring a stored definition back: %v", err)
		}
		if !con2HasProblem(problems, compiler.RuleStartIncomingFlow, "s", compiler.SeverityError) {
			t.Fatalf("reload problems %+v lack %s on s", problems, compiler.RuleStartIncomingFlow)
		}
	})
}
