package playground_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// A business rule task in a sandbox is answered by its decision, evaluated by the same
// handler production uses, against the decisions the server has deployed — not by the
// stub policy (ADR-0501). These tests pin that down, and pin the
// one escape an author still has: a stub named for the task itself.

const dishDecision = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="dish" namespace="http://atlas/dmn">
  <inputData id="id_season" name="Season"/>
  <decision id="Dish" name="Dish">
    <informationRequirement><requiredInput href="#id_season"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in1" label="Season"><inputExpression id="ie1" typeRef="string"><text>Season</text></inputExpression></input>
      <output id="out1" label="Dish" name="Dish" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>"Winter"</text></inputEntry><outputEntry id="o1"><text>"Roastbeef"</text></outputEntry></rule>
      <rule id="r2"><inputEntry id="e2"><text>"Summer"</text></inputEntry><outputEntry id="o2"><text>"Salad"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

// dishDecisionV2 is a second version of the same decision that answers winter
// differently, so a test can tell which version ran.
var dishDecisionV2 = strings.Replace(dishDecision, `"Roastbeef"`, `"Stew"`, 1)

// dishProcess is start → business rule task "brt" calling Dish with the case's season →
// end. extra is spliced into the task's calledDecision attributes, and ext into its
// extension elements.
func dishProcess(extra, ext string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0" id="d" targetNamespace="t">
  <process id="dinner" isExecutable="true">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="brt"/>
    <businessRuleTask id="brt">
      <extensionElements>
        <zeebe:calledDecision decisionId="Dish" resultVariable="dish"` + extra + `/>
        <zeebe:ioMapping><zeebe:input source="=season" target="Season"/></zeebe:ioMapping>` + ext + `
      </extensionElements>
    </businessRuleTask>
    <sequenceFlow id="f2" sourceRef="brt" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>`)
}

// runDinner opens a sandbox on the model, starts one case in the given season, runs it
// to rest and returns the case.
func runDinner(t *testing.T, opts playground.Options, season string) playground.CaseResult {
	t.Helper()
	opts.BaseDir, opts.StartTime = t.TempDir(), simStart
	sb, err := playground.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close() })
	key, err := sb.StartCase(model.VariableValue{Name: "season", Kind: model.VarString, Text: season})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	c, err := sb.Case(key)
	if err != nil {
		t.Fatalf("case: %v", err)
	}
	return c
}

func dishRegistry(t *testing.T, models ...string) *dmn.Registry {
	t.Helper()
	reg := dmn.NewRegistry()
	for i, m := range models {
		if err := reg.DeployDecision(uint64(101+i), []byte(m)); err != nil {
			t.Fatalf("deploy decision %d: %v", i+1, err)
		}
	}
	return reg
}

func TestABusinessRuleTaskIsAnsweredByItsDecisionNotByTheDefaultStub(t *testing.T) {
	c := runDinner(t, playground.Options{
		ModelXML:  dishProcess("", ""),
		Stubs:     playground.DefaultStubs(),
		Decisions: dishRegistry(t, dishDecision),
	}, "Winter")
	if c.State != model.PICompleted {
		t.Fatalf("case state = %v, want completed (path %v, incidents %d)", c.State, c.Path, c.Incidents)
	}
	if !strings.Contains(c.Variables["dish"], "Roastbeef") {
		t.Fatalf("dish = %q, want the decision's answer for winter (Roastbeef); variables %v", c.Variables["dish"], c.Variables)
	}
}

func TestTheDecisionReadsTheCasesOwnInputs(t *testing.T) {
	c := runDinner(t, playground.Options{
		ModelXML: dishProcess("", ""), Stubs: playground.DefaultStubs(), Decisions: dishRegistry(t, dishDecision),
	}, "Summer")
	if !strings.Contains(c.Variables["dish"], "Salad") {
		t.Fatalf("dish = %q, want Salad for summer", c.Variables["dish"])
	}
}

func TestAStubNamedForTheDecisionTaskStillAnswersIt(t *testing.T) {
	c := runDinner(t, playground.Options{
		ModelXML: dishProcess("", ""),
		Stubs: playground.StubSet{ByElement: map[string]playground.Stub{"brt": {
			Outputs: []model.VariableValue{{Name: "dish", Kind: model.VarString, Text: "stubbed"}},
		}}},
		Decisions: dishRegistry(t, dishDecision),
	}, "Winter")
	if c.Variables["dish"] != "stubbed" {
		t.Fatalf("dish = %q, want the author's stub to win over the decision", c.Variables["dish"])
	}
}

func TestAnUndeployedDecisionRaisesAnIncidentRatherThanPassing(t *testing.T) {
	c := runDinner(t, playground.Options{
		ModelXML: dishProcess("", ""), Stubs: playground.DefaultStubs(), Decisions: dmn.NewRegistry(),
	}, "Winter")
	if c.Incidents != 1 || c.State == model.PICompleted {
		t.Fatalf("case state %v with %d incidents, want it parked behind one incident", c.State, c.Incidents)
	}
	if _, ok := c.Variables["dish"]; ok {
		t.Fatalf("dish = %q, want no answer from a decision that does not exist", c.Variables["dish"])
	}
}

func TestAVersionBoundTaskEvaluatesThePinnedVersion(t *testing.T) {
	reg := dishRegistry(t, dishDecision, dishDecisionV2) // version 1 is key 101, version 2 is 102
	pins := func(cp *compiler.CompiledProcess) (map[compiler.DecisionVersionRef]uint64, error) {
		out := map[compiler.DecisionVersionRef]uint64{}
		for _, ref := range cp.VersionBoundDecisions() {
			out[ref] = uint64(100 + ref.Version)
		}
		return out, nil
	}
	for _, tc := range []struct {
		extra, want string
	}{
		{` atlas:version="1"`, "Roastbeef"},
		{` atlas:version="2"`, "Stew"},
		{``, "Stew"}, // latest
	} {
		c := runDinner(t, playground.Options{
			ModelXML: dishProcess(tc.extra, ""), Stubs: playground.DefaultStubs(), Decisions: reg, DecisionPins: pins,
		}, "Winter")
		if !strings.Contains(c.Variables["dish"], tc.want) {
			t.Errorf("binding %q: dish = %q, want %s", tc.extra, c.Variables["dish"], tc.want)
		}
	}
}

func TestADeploymentBoundTaskEvaluatesTheModelBundledWithTheDeployedVersion(t *testing.T) {
	reg := dishRegistry(t, dishDecision) // the latest decision deployment says Roastbeef
	if err := reg.Deploy(7, []byte(dishDecisionV2)); err != nil {
		t.Fatalf("deploy bundled model: %v", err) // the model bundled with deployment 7 says Stew
	}
	for _, tc := range []struct {
		name    string
		bundled uint64
		want    string
	}{
		{"a deployed version runs its own snapshot", 7, "Stew"},
		{"a draft has none and runs the latest", 0, "Roastbeef"},
	} {
		c := runDinner(t, playground.Options{
			ModelXML: dishProcess(` bindingType="deployment"`, ""), Stubs: playground.DefaultStubs(),
			Decisions: reg, BundledDecisions: tc.bundled,
		}, "Winter")
		if !strings.Contains(c.Variables["dish"], tc.want) {
			t.Errorf("%s: dish = %q, want %s", tc.name, c.Variables["dish"], tc.want)
		}
	}
}

func TestAModelNamingAVersionThatIsNotDeployedIsRefusedAtOpen(t *testing.T) {
	refused := errors.New(`process "dinner" binds decision "Dish" to version 3, which is not deployed`)
	_, err := playground.Open(playground.Options{
		ModelXML: dishProcess(` atlas:version="3"`, ""), BaseDir: t.TempDir(), StartTime: simStart,
		Decisions:    dishRegistry(t, dishDecision),
		DecisionPins: func(*compiler.CompiledProcess) (map[compiler.DecisionVersionRef]uint64, error) { return nil, refused },
	})
	if err == nil || !strings.Contains(err.Error(), "version 3") {
		t.Fatalf("open = %v, want the deploy's own refusal of an undeployed version", err)
	}
}

func TestACentrallyEvaluatedDecisionIsEvaluatedHereAndDialsNothing(t *testing.T) {
	// The task names a temis Worker (ADR-0050). A sandbox has none to dial, so it
	// evaluates the decision itself from the server's registry.
	c := runDinner(t, playground.Options{
		ModelXML:  dishProcess("", `<atlas:temisConnector connector="central"/>`),
		Stubs:     playground.DefaultStubs(),
		Decisions: dishRegistry(t, dishDecision),
	}, "Winter")
	if !strings.Contains(c.Variables["dish"], "Roastbeef") {
		t.Fatalf("dish = %q, want the decision evaluated locally; variables %v", c.Variables["dish"], c.Variables)
	}
}

func TestWithoutDecisionsTheStubPolicyAnswersAsBefore(t *testing.T) {
	// A sandbox given no decisions keeps the behaviour it had: the task is a job like
	// any other. This is the contract for a caller that has no registry to offer.
	c := runDinner(t, playground.Options{ModelXML: dishProcess("", ""), Stubs: playground.DefaultStubs()}, "Winter")
	if c.State != model.PICompleted {
		t.Fatalf("case state = %v, want the default stub to have answered", c.State)
	}
	if _, ok := c.Variables["dish"]; ok {
		t.Fatalf("dish = %q, want nothing written by a stub with no outputs", c.Variables["dish"])
	}
}
