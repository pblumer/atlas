package dmn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A decision service is DMN's published interface over part of a DRD: the caller
// names the service, supplies its input data and the results of its input
// decisions, and the rest stays behind the interface. temis compiles and
// evaluates one completely; Atlas could not reach it, because a business rule
// task's decisionId resolved against decisions only (#998).
//
// These tests drive the registry and the validator — the seams a task and the
// deploy gate actually pass through.

// serviceXML is shaped like the demo tariff model: the output decision is a boxed
// context, one decision is encapsulated, and one is an input decision — a
// boundary the caller supplies rather than the service computing it.
const serviceXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="svc" name="Tarif" namespace="http://atlas/dmn">
  <inputData id="in_km" name="jahreskilometer"><variable name="jahreskilometer" typeRef="number"/></inputData>
  <decision id="d_basis" name="basispraemie">
    <variable name="basispraemie" typeRef="number"/>
    <literalExpression id="le_basis"><text>500</text></literalExpression>
  </decision>
  <decision id="d_zuschlag" name="zuschlaege">
    <variable name="zuschlaege" typeRef="number"/>
    <informationRequirement id="ir_z"><requiredInput href="#in_km"/></informationRequirement>
    <literalExpression id="le_z"><text>if jahreskilometer &gt; 20000 then 120 else 0</text></literalExpression>
  </decision>
  <decision id="d_praemie" name="praemie">
    <variable name="praemie" typeRef="number"/>
    <informationRequirement id="ir_p1"><requiredDecision href="#d_basis"/></informationRequirement>
    <informationRequirement id="ir_p2"><requiredDecision href="#d_zuschlag"/></informationRequirement>
    <context id="ctx_p">
      <contextEntry>
        <variable id="cv_b" name="brutto" typeRef="number"/>
        <literalExpression id="le_b"><text>basispraemie + zuschlaege</text></literalExpression>
      </contextEntry>
      <contextEntry>
        <literalExpression id="le_t"><text>brutto - 50</text></literalExpression>
      </contextEntry>
    </context>
  </decision>
  <decisionService id="svc_praemie" name="Praemienrechnung">
    <variable name="Praemienrechnung"/>
    <outputDecision href="#d_praemie"/>
    <encapsulatedDecision href="#d_zuschlag"/>
    <inputDecision href="#d_basis"/>
    <inputData href="#in_km"/>
  </decisionService>
</definitions>`

func serviceInputs() map[string]any {
	// The input data, plus the input decision's result: the boundary the service
	// does not compute.
	return map[string]any{"jahreskilometer": 22000, "basispraemie": 500}
}

func TestABusinessRuleTaskCanCallADecisionService(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(5, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	// The deploy-time selector a latest-bound task is pinned against has to know the
	// service, or the task's deployment is refused before anything evaluates.
	if key, ok := r.LatestDecisionKey("Praemienrechnung"); !ok || key != 5 {
		t.Fatalf("LatestDecisionKey(service) = %d,%v, want 5,true", key, ok)
	}
	for _, c := range []struct {
		how string
		run func() (map[string]any, error)
	}{
		{"bound to the deployment", func() (map[string]any, error) {
			return r.Evaluate(context.Background(), 5, "Praemienrechnung", serviceInputs())
		}},
		{"bound to the latest", func() (map[string]any, error) {
			return r.EvaluateLatest(context.Background(), "Praemienrechnung", serviceInputs())
		}},
	} {
		t.Run(c.how, func(t *testing.T) {
			out, err := c.run()
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			// Keyed by the output decision's name, which is what DMN returns — 500 + 120,
			// less the 50 the context's result cell subtracts.
			got, ok := out["praemie"]
			if !ok {
				t.Fatalf("outputs = %v, want a praemie", out)
			}
			if n, isNum := got.(json.Number); !isNum || n.String() != "570" {
				t.Errorf("praemie = %#v, want the number 570", got)
			}
		})
	}
}

func TestTheCallerSuppliesAnInputDecisionRatherThanTheServiceComputingIt(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(6, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	// basispraemie is an input decision: the model's own literal says 500, and the
	// service must use what the caller gives it instead.
	in := serviceInputs()
	in["basispraemie"] = 1000
	out, err := r.Evaluate(context.Background(), 6, "Praemienrechnung", in)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if n, ok := out["praemie"].(json.Number); !ok || n.String() != "1070" {
		t.Errorf("praemie = %#v, want the number 1070 — the boundary the caller supplied", out["praemie"])
	}
}

func TestADecisionInsideAServiceStaysCallableOnItsOwn(t *testing.T) {
	// The workaround #998 describes — call the output decision directly — must keep
	// working, because every task deployed before this named a decision.
	r := NewRegistry()
	if err := r.DeployDecision(7, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	out, err := r.Evaluate(context.Background(), 7, "zuschlaege", map[string]any{"jahreskilometer": 22000})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if n, ok := out["zuschlaege"].(json.Number); !ok || n.String() != "120" {
		t.Errorf("zuschlaege = %#v, want the number 120", out["zuschlaege"])
	}
}

func TestAModelPublishesItsServicesBesideItsDecisions(t *testing.T) {
	res := NewValidator(nil).ValidateXML(context.Background(), []byte(serviceXML))
	if !res.Valid {
		t.Fatalf("validate: %s", res.Message)
	}
	if len(res.Services) != 1 || res.Services[0] != "Praemienrechnung" {
		t.Errorf("Services = %v, want [Praemienrechnung]", res.Services)
	}
	// The decisions are unchanged by the service being there.
	if len(res.Decisions) != 3 {
		t.Errorf("Decisions = %v, want the model's three decisions", res.Decisions)
	}
}

func TestAServiceIsOfferedForTryingWithTheInputsItRequires(t *testing.T) {
	trial := NewValidator(nil).Try(context.Background(), []byte(serviceXML), "Praemienrechnung", serviceInputs())
	if !trial.OK {
		t.Fatalf("try: %s", trial.Message)
	}
	var svc *DecisionInfo
	for i := range trial.Decisions {
		if trial.Decisions[i].ID == "Praemienrechnung" {
			svc = &trial.Decisions[i]
		}
	}
	if svc == nil {
		t.Fatalf("the service is not offered: %+v", trial.Decisions)
	}
	if !svc.Service {
		t.Error("the service is offered without being marked as one")
	}
	// Its input data and its input decision, in the order temis binds them as the
	// service's parameters — not the whole graph's inputs.
	if len(svc.Inputs) != 2 || svc.Inputs[0].Name != "jahreskilometer" || svc.Inputs[1].Name != "basispraemie" {
		t.Errorf("inputs = %+v, want [jahreskilometer basispraemie]", svc.Inputs)
	}
	if svc.Output.Name != "praemie" || svc.Output.Type != "number" {
		t.Errorf("output = %+v, want praemie/number", svc.Output)
	}
	if n, ok := trial.Outputs["praemie"].(json.Number); !ok || n.String() != "570" {
		t.Errorf("praemie = %#v, want the number 570", trial.Outputs["praemie"])
	}
}

func TestAModelNamingADecisionAndAServiceAlikeIsRefused(t *testing.T) {
	const collidingXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="coll" name="coll" namespace="http://atlas/dmn">
  <decision id="d_one" name="Freigabe">
    <variable name="Freigabe" typeRef="number"/>
    <literalExpression id="le_one"><text>1</text></literalExpression>
  </decision>
  <decisionService id="svc_one" name="Freigabe">
    <outputDecision href="#d_one"/>
  </decisionService>
</definitions>`
	res := NewValidator(nil).ValidateXML(context.Background(), []byte(collidingXML))
	if res.Valid {
		t.Fatal("a model in which one name means two things was accepted")
	}
	if !strings.Contains(res.Message, "Freigabe") {
		t.Errorf("message = %q, want it to name the colliding name", res.Message)
	}
}

func TestAServiceTheDocumentDoesNotDeclareIsNotOffered(t *testing.T) {
	// Nothing to read: unparseable bytes, and a model with no service at all. Both
	// have to answer "none" rather than guessing.
	defs, diags, err := NewValidator(nil).engine.Compile(context.Background(), []byte(serviceXML))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", err, diags)
	}
	if got := describeServices(defs, []byte("<not-a-document")); got != nil {
		t.Errorf("describeServices(unparseable) = %v, want none", got)
	}
	if got := serviceNames(describeServices(defs, []byte("<not-a-document"))); got != nil {
		t.Errorf("serviceNames(nothing described) = %v, want none", got)
	}
	res := NewValidator(nil).ValidateXML(context.Background(), []byte(labelledXML))
	if len(res.Services) != 0 {
		t.Errorf("Services = %v, want none for a model that declares none", res.Services)
	}
}

// awkwardServicesXML holds the shapes a document can carry that the description
// has to survive: a service with no name and no id, a reference into another
// document, and a service with two output decisions, whose result is a context
// this shape cannot name.
const awkwardServicesXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="awk" name="awk" namespace="http://atlas/dmn">
  <inputData id="in_a" name="a"><variable name="a" typeRef="number"/></inputData>
  <decision id="d_one" name="one">
    <variable name="one" typeRef="number"/>
    <informationRequirement id="ir1"><requiredInput href="#in_a"/></informationRequirement>
    <literalExpression id="le1"><text>a + 1</text></literalExpression>
  </decision>
  <decision id="d_two" name="two">
    <variable name="two" typeRef="number"/>
    <informationRequirement id="ir2"><requiredInput href="#in_a"/></informationRequirement>
    <literalExpression id="le2"><text>a + 2</text></literalExpression>
  </decision>
  <decisionService><outputDecision href="#d_one"/></decisionService>
  <decisionService id="svc_x" name="Both">
    <outputDecision href="#d_one"/>
    <outputDecision href="#d_two"/>
    <inputData href="#in_a"/>
    <inputData href="elsewhere.dmn#in_zzz"/>
  </decisionService>
</definitions>`

func TestTheAwkwardShapesOfADocumentAreSurvived(t *testing.T) {
	res := NewValidator(nil).ValidateXML(context.Background(), []byte(awkwardServicesXML))
	if !res.Valid {
		t.Fatalf("validate: %s", res.Message)
	}
	// The nameless service cannot be addressed and is not offered.
	if len(res.Services) != 1 || res.Services[0] != "Both" {
		t.Fatalf("Services = %v, want [Both]", res.Services)
	}
	trial := NewValidator(nil).Try(context.Background(), []byte(awkwardServicesXML), "Both", map[string]any{"a": 4})
	if !trial.OK {
		t.Fatalf("try: %s", trial.Message)
	}
	var svc *DecisionInfo
	for i := range trial.Decisions {
		if trial.Decisions[i].Service {
			svc = &trial.Decisions[i]
		}
	}
	if svc == nil {
		t.Fatalf("no service offered: %+v", trial.Decisions)
	}
	// The reference into another document names nothing here and is left out.
	if len(svc.Inputs) != 1 || svc.Inputs[0].Name != "a" {
		t.Errorf("inputs = %+v, want only the input this document declares", svc.Inputs)
	}
	// Two output decisions: the result is a context, which one field cannot name, so
	// the service's own name stands there with no type.
	if svc.Output.Name != "Both" || svc.Output.Type != "" {
		t.Errorf("output = %+v, want the service's own name and no type", svc.Output)
	}
	// And it evaluates: both output decisions, keyed by their names.
	if len(trial.Outputs) != 2 {
		t.Fatalf("outputs = %v, want both output decisions", trial.Outputs)
	}
	for name, want := range map[string]string{"one": "5", "two": "6"} {
		if n, ok := trial.Outputs[name].(json.Number); !ok || n.String() != want {
			t.Errorf("%s = %#v, want the number %s", name, trial.Outputs[name], want)
		}
	}
}

func TestTwoServicesUnderOneNameAreRefused(t *testing.T) {
	// The same ambiguity as a decision and a service sharing a name, and refused for
	// the same reason: a decisionId is one string.
	const twiceXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="twice" name="twice" namespace="http://atlas/dmn">
  <decision id="d_one" name="one">
    <variable name="one" typeRef="number"/>
    <literalExpression id="le1"><text>1</text></literalExpression>
  </decision>
  <decision id="d_two" name="two">
    <variable name="two" typeRef="number"/>
    <literalExpression id="le2"><text>2</text></literalExpression>
  </decision>
  <decisionService id="svc_a" name="Both"><outputDecision href="#d_one"/></decisionService>
  <decisionService id="svc_b" name="Both"><outputDecision href="#d_two"/></decisionService>
</definitions>`
	res := NewValidator(nil).ValidateXML(context.Background(), []byte(twiceXML))
	if res.Valid {
		t.Fatal("a model declaring two services under one name was accepted")
	}
	if !strings.Contains(res.Message, "Both") {
		t.Errorf("message = %q, want it to name the repeated name", res.Message)
	}
	// And the same answer before deploying, from the try-a-decision path.
	trial := NewValidator(nil).Try(context.Background(), []byte(twiceXML), "Both", nil)
	if trial.OK || !strings.Contains(trial.Message, "Both") {
		t.Errorf("try = %+v, want the same refusal", trial)
	}
}

func TestAServiceThatCannotEvaluateFailsTheJobRatherThanTheRegistry(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(8, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	// A value no FEEL type can hold. The evaluation says which service and where,
	// the same shape a failing decision answers with, so the worker's job error
	// names the model rather than the engine's internals.
	_, err := r.Evaluate(context.Background(), 8, "Praemienrechnung", map[string]any{"jahreskilometer": make(chan int)})
	if err == nil {
		t.Fatal("an unconvertible input evaluated")
	}
	if !strings.Contains(err.Error(), `service "Praemienrechnung"`) || !strings.Contains(err.Error(), "def 8") {
		t.Errorf("error = %v, want it to name the service and the deployment", err)
	}
}

func TestAModelWithTwoServicesAlreadyOnDiskStillComesBack(t *testing.T) {
	// Before a service could be called, a model declaring two of them under one name
	// deployed without anyone noticing — Atlas did not look at services at all. The
	// deploy gate refuses that model now, but a reload does not re-apply the gate
	// (ADR-0177): it must come back, publish the name once, and evaluate.
	const twiceXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="twice" name="twice" namespace="http://atlas/dmn">
  <decision id="d_one" name="one">
    <variable name="one" typeRef="number"/>
    <literalExpression id="le1"><text>1</text></literalExpression>
  </decision>
  <decisionService id="svc_a" name="Both"><outputDecision href="#d_one"/></decisionService>
  <decisionService id="svc_b" name="Both"><outputDecision href="#d_one"/></decisionService>
</definitions>`
	r := NewRegistry()
	diag, err := r.ReloadDecision(41, []byte(twiceXML))
	if err != nil || diag != "" {
		t.Fatalf("reload: err=%v diag=%q", err, diag)
	}
	if _, ok := r.LatestDecisionKey("Both"); !ok {
		t.Fatal("the service did not come back addressable")
	}
	out, err := r.Evaluate(context.Background(), 41, "Both", nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if n, ok := out["one"].(json.Number); !ok || n.String() != "1" {
		t.Errorf("one = %#v, want the number 1", out["one"])
	}
}
