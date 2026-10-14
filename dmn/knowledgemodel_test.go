package dmn_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// A business knowledge model is a reusable FEEL function. It is not a decision:
// nothing in a process can call one, and it produces a result only when a decision
// invokes it through a knowledge requirement. The decision editor lets one be
// authored, so what an author draws there has to survive the whole path — compile,
// the description the picker and the deploy gate read, and the evaluation the
// business-rule-task worker runs.
//
// feeModel is that path in miniature: an input datum, a knowledge model holding the
// calculation, and a decision that reads the input and invokes the model.
const feeModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="fee" name="Fee" namespace="http://atlas/dmn">
  <inputData id="id_amount" name="amount">
    <variable name="amount" typeRef="number"/>
  </inputData>
  <businessKnowledgeModel id="bkm_fee" name="fee">
    <variable name="fee" typeRef="number"/>
    <encapsulatedLogic kind="FEEL">
      <formalParameter name="base" typeRef="number"/>
      <literalExpression id="le_fee"><text>base * 0.1</text></literalExpression>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <decision id="dec_total" name="total">
    <variable name="total" typeRef="number"/>
    <informationRequirement id="ir1"><requiredInput href="#id_amount"/></informationRequirement>
    <knowledgeRequirement id="kr1"><requiredKnowledge href="#bkm_fee"/></knowledgeRequirement>
    <literalExpression id="le_total"><text>amount + fee(amount)</text></literalExpression>
  </decision>
</definitions>`

// TestADecisionInvokesItsKnowledgeModel pins the three answers this path gives, each
// of which is easy to get subtly wrong in a different way:
//
//   - the model is valid and exposes the *decision*, never the knowledge model. A
//     knowledge model in that list would offer a process a decision it cannot call;
//   - the decision's inputs come from its information requirements alone. A knowledge
//     requirement supplies a function, not a value, and counting it as an input would
//     put a phantom field on the business rule task and on the Test panel's form;
//   - the invocation runs. That last one is the pinned temis version's to honour
//     rather than Atlas's, which is why it is worth asserting here: it is the
//     difference between a knowledge model being a modelling device and it being a
//     thing that computes.
func TestADecisionInvokesItsKnowledgeModel(t *testing.T) {
	v := dmn.NewValidator(nil)
	ctx := context.Background()

	res := v.ValidateXML(ctx, []byte(feeModel))
	if !res.Valid {
		t.Fatalf("model did not compile: %s", res.Message)
	}
	if len(res.Decisions) != 1 || res.Decisions[0] != "total" {
		t.Errorf("decisions = %v, want exactly [total] — a knowledge model is not "+
			"invocable, so it must not be offered as one", res.Decisions)
	}

	trial := v.Try(ctx, []byte(feeModel), "total", map[string]any{"amount": 200})
	if !trial.OK {
		t.Fatalf("evaluating total: %s", trial.Message)
	}
	if len(trial.Decisions) != 1 {
		t.Fatalf("described %d decisions, want 1: %+v", len(trial.Decisions), trial.Decisions)
	}
	inputs := trial.Decisions[0].Inputs
	if len(inputs) != 1 || inputs[0].Name != "amount" {
		t.Errorf("inputs = %+v, want only the input datum — the knowledge requirement "+
			"names a function to call, not a value to be given", inputs)
	}
	// 200 + fee(200) = 200 + 20. A total of 200 would mean the invocation returned
	// nothing; an error would mean the name did not resolve at all.
	if got := trial.Outputs["total"]; !numEq(got, 220) {
		t.Errorf("total = %v (%T), want 220 — the decision did not invoke its knowledge model", got, got)
	}
}

// TestAKnowledgeModelAloneIsNotDeployable: a model holding a knowledge model and no
// decision is a valid DMN model that offers nothing to run. The deploy gate refuses
// on an empty decision list rather than on invalidity, so this is the state it
// refuses — and the reason the editor can leave an author holding one.
func TestAKnowledgeModelAloneIsNotDeployable(t *testing.T) {
	const bkmOnly = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="only" name="FeeOnly" namespace="http://atlas/dmn">
  <businessKnowledgeModel id="bkm_fee" name="fee">
    <variable name="fee" typeRef="number"/>
    <encapsulatedLogic kind="FEEL">
      <formalParameter name="base" typeRef="number"/>
      <literalExpression id="le_fee"><text>base * 0.1</text></literalExpression>
    </encapsulatedLogic>
  </businessKnowledgeModel>
</definitions>`

	res := dmn.NewValidator(nil).ValidateXML(context.Background(), []byte(bkmOnly))
	if !res.Valid {
		t.Fatalf("a knowledge model on its own is a valid model; got: %s", res.Message)
	}
	if len(res.Decisions) != 0 {
		t.Errorf("decisions = %v, want none", res.Decisions)
	}
}

// TestTheGraphCarriesAKnowledgeRequirement: the DRG viewer draws the arrow from a
// knowledge model to the decision that invokes it, so the edge has to come back
// typed and pointed the way DMN points it — from the required element to the one
// requiring it.
func TestTheGraphCarriesAKnowledgeRequirement(t *testing.T) {
	dir := t.TempDir()
	res := dmn.NewValidator(fileResolver{dir: dir, body: []byte(feeModel)})
	g, err := res.Graph(context.Background(), "fee")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if !g.Valid {
		t.Fatalf("graph not valid: %s", g.Message)
	}
	var found bool
	for _, e := range g.Edges {
		if e.Type == "knowledgeRequirement" && e.Source == "bkm_fee" && e.Target == "dec_total" {
			found = true
		}
	}
	if !found {
		t.Errorf("edges = %+v, want a knowledgeRequirement bkm_fee → dec_total", g.Edges)
	}
}

// fileResolver hands back one model for any handle, which is all Graph needs.
type fileResolver struct {
	dir  string
	body []byte
}

func (f fileResolver) Resolve(context.Context, string) ([]byte, error) { return f.body, nil }

// numEq compares a decoded FEEL number against a want without pinning how temis
// represents one. It currently hands numbers back as decimal strings — DMN numbers
// are arbitrary-precision decimals, and a float64 would not carry one faithfully —
// so a test that asserted a float64 here would be asserting the representation
// rather than the arithmetic. This is not particular to a knowledge model: a
// decision doing its own arithmetic answers the same way.
func numEq(got any, want float64) bool {
	switch n := got.(type) {
	case json.Number:
		// What a decision's number arrives as now: the exact decimal, tagged so
		// nothing downstream has to guess it is one (ADR-0386).
		f, err := n.Float64()
		return err == nil && f == want
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return err == nil && f == want
	case float64:
		return n == want
	case float32:
		return float64(n) == want
	case int:
		return float64(n) == want
	case int64:
		return float64(n) == want
	default:
		return false
	}
}
