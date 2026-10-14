package dmn

import (
	"context"
	"encoding/json"
	"testing"
)

// A DMN decision has a label (`name`) and a FEEL identifier (`<variable name>`),
// and they need not be the same string. temis used to publish a required
// decision's result under its label, so a spec-conformant chain — `Decision B`
// reading `alpha`, the variable of `Decision A` — did not compile, and Atlas
// refused a model the DMN specification calls valid (#992).
//
// The temis this repository now pins binds, keys and indexes by the identifier
// and resolves a decision by either name. That fixes the refusal and moves the
// name Atlas reads out of its index — which is where the compatibility question
// lives, because every business rule task deployed so far carries the label.
//
// These tests drive the registry and the validator, not the graph helpers, so
// they fail if either half of that — the chain compiling, the label still
// resolving — stops being true.

// chainXML is the model from #992, unchanged: two decisions whose labels differ
// from their identifiers, the second reading the first by identifier.
const chainXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="chain" name="chain" namespace="http://atlas/dmn">
  <inputData id="in_x" name="x"><variable name="x" typeRef="number"/></inputData>
  <decision id="d_a" name="Decision A">
    <variable name="alpha" typeRef="number"/>
    <informationRequirement id="ir_a"><requiredInput href="#in_x"/></informationRequirement>
    <literalExpression id="le_a"><text>x + 1</text></literalExpression>
  </decision>
  <decision id="d_b" name="Decision B">
    <variable name="beta" typeRef="number"/>
    <informationRequirement id="ir_b"><requiredDecision href="#d_a"/></informationRequirement>
    <literalExpression id="le_b"><text>alpha * 10</text></literalExpression>
  </decision>
</definitions>`

// labelledXML is the shape that was deployable before the bump and must stay
// addressable after it: one leaf decision whose label is not a FEEL identifier at
// all, with an input whose label likewise differs from the name it binds.
const labelledXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="leaf" name="leaf" namespace="http://atlas/dmn">
  <inputData id="in_k" name="Kunde Alter"><variable name="alter" typeRef="number"/></inputData>
  <decision id="d_k" name="Kunden-Risiko">
    <variable name="risiko" typeRef="number"/>
    <informationRequirement id="ir_k"><requiredInput href="#in_k"/></informationRequirement>
    <literalExpression id="le_k"><text>alter * 2</text></literalExpression>
  </decision>
</definitions>`

func TestTheChainFromTheIssueDeploysAndEvaluates(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(7, []byte(chainXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	// Through the deployment-bound path, which finds the model by the decision's
	// name before temis ever sees it.
	out, err := r.Evaluate(context.Background(), 7, "Decision B", map[string]any{"x": 4})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	got, ok := out["beta"]
	if !ok {
		t.Fatalf("outputs = %v, want a beta", out)
	}
	if n, isNum := got.(json.Number); !isNum || n.String() != "50" {
		t.Errorf("beta = %#v, want the number 50", got)
	}
}

func TestADecisionResolvesUnderBothOfItsNames(t *testing.T) {
	for _, addr := range []string{"Kunden-Risiko", "risiko"} {
		t.Run(addr, func(t *testing.T) {
			r := NewRegistry()
			if err := r.DeployDecision(11, []byte(labelledXML)); err != nil {
				t.Fatalf("deploy: %v", err)
			}
			// The deploy-time selector a latest-bound task is pinned against.
			if key, ok := r.LatestDecisionKey(addr); !ok || key != 11 {
				t.Fatalf("LatestDecisionKey(%q) = %d,%v, want 11,true", addr, key, ok)
			}
			if !r.LatestDecisionIDs()[addr] {
				t.Errorf("LatestDecisionIDs() does not carry %q", addr)
			}
			for _, eval := range []struct {
				how string
				run func() (map[string]any, error)
			}{
				{"bound to the deployment", func() (map[string]any, error) {
					return r.Evaluate(context.Background(), 11, addr, map[string]any{"alter": 21})
				}},
				{"bound to the latest", func() (map[string]any, error) {
					return r.EvaluateLatest(context.Background(), addr, map[string]any{"alter": 21})
				}},
			} {
				out, err := eval.run()
				if err != nil {
					t.Fatalf("%s: evaluate %q: %v", eval.how, addr, err)
				}
				if n, ok := out["risiko"].(json.Number); !ok || n.String() != "42" {
					t.Errorf("%s: risiko = %#v, want the number 42", eval.how, out["risiko"])
				}
			}
		})
	}
}

func TestAModelPublishesOneNamePerDecisionAndAcceptsTheOther(t *testing.T) {
	res := NewValidator(nil).ValidateXML(context.Background(), []byte(labelledXML))
	if !res.Valid {
		t.Fatalf("validate: %s", res.Message)
	}
	// What a deployment records, a listing shows and the version counter is kept
	// under: the label, exactly one entry, as before the bump.
	if len(res.Decisions) != 1 || res.Decisions[0] != "Kunden-Risiko" {
		t.Errorf("Decisions = %v, want [Kunden-Risiko]", res.Decisions)
	}
	// What matching additionally accepts, recorded nowhere.
	if len(res.Aliases) != 1 || res.Aliases[0] != "risiko" {
		t.Errorf("Aliases = %v, want [risiko]", res.Aliases)
	}
}

func TestAnInputIsAcceptedUnderTheLabelADeployedTaskRecorded(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(13, []byte(labelledXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	for _, c := range []struct {
		name string
		in   map[string]any
		want string
	}{
		// The identifier temis requires now.
		{"by identifier", map[string]any{"alter": 21}, "42"},
		// The label Atlas offered when the task was deployed, which temis required
		// then and does not now.
		{"by label", map[string]any{"Kunde Alter": 21}, "42"},
		// A supplied identifier is never overwritten by a label.
		{"both, identifier wins", map[string]any{"alter": 21, "Kunde Alter": 1}, "42"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := r.Evaluate(context.Background(), 13, "Kunden-Risiko", c.in)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if n, ok := out["risiko"].(json.Number); !ok || n.String() != c.want {
				t.Errorf("risiko = %#v, want the number %s", out["risiko"], c.want)
			}
		})
	}
}

func TestTheInputsOfferedAreTheNamesTheEvaluationRequires(t *testing.T) {
	trial := NewValidator(nil).Try(context.Background(), []byte(labelledXML), "Kunden-Risiko", map[string]any{"alter": 21})
	if !trial.OK {
		t.Fatalf("try: %s", trial.Message)
	}
	if len(trial.Decisions) != 1 {
		t.Fatalf("decisions = %v, want one", trial.Decisions)
	}
	d := trial.Decisions[0]
	if len(d.Inputs) != 1 || d.Inputs[0].Name != "alter" {
		t.Errorf("inputs = %+v, want the identifier alter the evaluation binds", d.Inputs)
	}
	if d.Output.Name != "risiko" {
		t.Errorf("output = %+v, want risiko", d.Output)
	}
}

func TestOnlyADecisionThatCanRunIsPublished(t *testing.T) {
	// One decision compiles, the other does not. Reload is the path that accepts a
	// model with error diagnostics (ADR-0177), so it is where a decision present but
	// not executable can reach the index at all.
	const brokenXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="broken" name="broken" namespace="http://atlas/dmn">
  <decision id="d_ok" name="Good">
    <variable name="good" typeRef="number"/>
    <literalExpression id="le_ok"><text>1 + 1</text></literalExpression>
  </decision>
  <decision id="d_bad" name="Bad">
    <variable name="bad" typeRef="number"/>
    <literalExpression id="le_bad"><text>nirgendwo + 1</text></literalExpression>
  </decision>
</definitions>`
	r := NewRegistry()
	diag, err := r.ReloadDecision(17, []byte(brokenXML))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diag == "" {
		t.Fatal("expected the broken decision to be diagnosed")
	}
	if _, ok := r.LatestDecisionKey("Good"); !ok {
		t.Error("the decision that compiled is not addressable")
	}
	for _, name := range []string{"Bad", "bad"} {
		if _, ok := r.LatestDecisionKey(name); ok {
			t.Errorf("%q is addressable although it cannot run", name)
		}
	}
}

func TestUndeployingRebuildsBothSpellings(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(21, []byte(labelledXML)); err != nil {
		t.Fatalf("deploy 21: %v", err)
	}
	if err := r.DeployDecision(22, []byte(labelledXML)); err != nil {
		t.Fatalf("deploy 22: %v", err)
	}
	r.UndeployDecision(22)
	for _, addr := range []string{"Kunden-Risiko", "risiko"} {
		key, ok := r.LatestDecisionKey(addr)
		if !ok || key != 21 {
			t.Errorf("LatestDecisionKey(%q) = %d,%v, want 21,true", addr, key, ok)
		}
	}
}

func TestADecisionWithNoLabelIsPublishedUnderItsIdentifier(t *testing.T) {
	// Both decisions are legal XML and temis compiles both. The first is nameless
	// but declares an identifier, so that identifier is the only name it can be
	// addressed by; the second declares neither, so temis itself indexes it under
	// nothing and it is addressable nowhere.
	const namelessXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="nameless" name="nameless" namespace="http://atlas/dmn">
  <decision id="d_y">
    <variable name="y" typeRef="number"/>
    <literalExpression id="le_y"><text>6 * 7</text></literalExpression>
  </decision>
  <decision id="d_none">
    <literalExpression id="le_none"><text>1</text></literalExpression>
  </decision>
</definitions>`
	res := NewValidator(nil).ValidateXML(context.Background(), []byte(namelessXML))
	if !res.Valid {
		t.Fatalf("validate: %s", res.Message)
	}
	if len(res.Decisions) != 1 || res.Decisions[0] != "y" {
		t.Errorf("Decisions = %v, want [y]", res.Decisions)
	}
	if len(res.Aliases) != 0 {
		t.Errorf("Aliases = %v, want none", res.Aliases)
	}
	r := NewRegistry()
	if err := r.DeployDecision(31, []byte(namelessXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	out, err := r.Evaluate(context.Background(), 31, "y", nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if n, ok := out["y"].(json.Number); !ok || n.String() != "42" {
		t.Errorf("y = %#v, want the number 42", out["y"])
	}
}
