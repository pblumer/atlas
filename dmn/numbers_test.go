package dmn

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pblumer/atlas/model"
	tdmn "github.com/pblumer/temis/dmn"
)

// A FEEL number leaves temis as its exact decimal *string* — a deliberate,
// documented contract (temis ADR-0007) that keeps an amount exact where a float64
// would round it. Atlas has a lossless home for precisely that: model.VarNumber's
// Text *is* "the canonical decimal string". Between the two, the knowledge that
// the string is a number was dropped, and a decision result landed as VarString.
//
// The cost was not cosmetic. A gateway condition comparing that variable to a
// number is a FEEL type mismatch, which evaluates to null, which is not true — so
// the token took the default flow with no incident and no diagnostic (#991).
//
// These tests drive the real seam: evaluate through evalDecision, then build the
// process variable the way the business rule task does.

// shapesXML carries one decision per result shape a DMN model can produce, since
// which shape it is decides where the declared type has to be read from.
const shapesXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="shapes" name="shapes" namespace="http://atlas/dmn">
  <inputData id="id_a" name="a"><variable name="a" typeRef="number"/></inputData>

  <decision id="amount" name="amount">
    <variable name="amount" typeRef="number"/>
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <decisionTable id="t_amount" hitPolicy="UNIQUE">
      <input id="i_amount"><inputExpression id="ie_amount" typeRef="number"><text>a</text></inputExpression></input>
      <output id="o_amount" name="amount" typeRef="number"/>
      <rule id="r_amount"><inputEntry id="e_amount"><text>&gt;= 0</text></inputEntry><outputEntry id="x_amount"><text>1250</text></outputEntry></rule>
    </decisionTable>
  </decision>

  <decision id="grade" name="grade">
    <variable name="grade" typeRef="string"/>
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <decisionTable id="t_grade" hitPolicy="UNIQUE">
      <input id="i_grade"><inputExpression id="ie_grade" typeRef="number"><text>a</text></inputExpression></input>
      <output id="o_grade" name="grade" typeRef="string"/>
      <rule id="r_grade"><inputEntry id="e_grade"><text>&gt;= 0</text></inputEntry><outputEntry id="x_grade"><text>"0800"</text></outputEntry></rule>
    </decisionTable>
  </decision>

  <decision id="passed" name="passed">
    <variable name="passed" typeRef="boolean"/>
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <decisionTable id="t_passed" hitPolicy="UNIQUE">
      <input id="i_passed"><inputExpression id="ie_passed" typeRef="number"><text>a</text></inputExpression></input>
      <output id="o_passed" name="passed" typeRef="boolean"/>
      <rule id="r_passed"><inputEntry id="e_passed"><text>&gt;= 0</text></inputEntry><outputEntry id="x_passed"><text>true</text></outputEntry></rule>
    </decisionTable>
  </decision>

  <decision id="doubled" name="doubled">
    <variable name="doubled" typeRef="number"/>
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <literalExpression id="le_doubled"><text>a * 2</text></literalExpression>
  </decision>

  <decision id="untyped" name="untyped">
    <variable name="untyped"/>
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <literalExpression id="le_untyped"><text>a * 3</text></literalExpression>
  </decision>

  <decision id="columns" name="columns">
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <decisionTable id="t_columns" hitPolicy="UNIQUE">
      <input id="i_columns"><inputExpression id="ie_columns" typeRef="number"><text>a</text></inputExpression></input>
      <output id="o_net"   name="net"   typeRef="number"/>
      <output id="o_code"  name="code"  typeRef="string"/>
      <output id="o_loose" name="loose"/>
      <rule id="r_columns"><inputEntry id="e_columns"><text>&gt;= 0</text></inputEntry>
        <outputEntry id="x_net"><text>99.50</text></outputEntry>
        <outputEntry id="x_code"><text>"0800"</text></outputEntry>
        <outputEntry id="x_loose"><text>7</text></outputEntry></rule>
    </decisionTable>
  </decision>

  <decision id="premium" name="premium">
    <variable name="premium"/>
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <context id="c_premium">
      <contextEntry><variable name="gross" typeRef="number"/><literalExpression id="q_gross"><text>a * 10</text></literalExpression></contextEntry>
      <contextEntry><variable name="label" typeRef="string"/><literalExpression id="q_label"><text>"0800"</text></literalExpression></contextEntry>
    </context>
  </decision>

  <decision id="resultcell" name="resultcell">
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <context id="c_resultcell">
      <contextEntry><variable name="base" typeRef="number"/><literalExpression id="q_base"><text>a * 5</text></literalExpression></contextEntry>
      <contextEntry><literalExpression id="q_total" typeRef="number"><text>base + 1</text></literalExpression></contextEntry>
    </context>
  </decision>

  <decision id="collected" name="collected">
    <variable name="collected"/>
    <informationRequirement><requiredInput href="#id_a"/></informationRequirement>
    <decisionTable id="t_collected" hitPolicy="COLLECT">
      <input id="i_collected"><inputExpression id="ie_collected" typeRef="number"><text>a</text></inputExpression></input>
      <output id="o_collected" name="collected" typeRef="number"/>
      <rule id="r_c1"><inputEntry id="e_c1"><text>&gt;= 0</text></inputEntry><outputEntry id="x_c1"><text>10</text></outputEntry></rule>
      <rule id="r_c2"><inputEntry id="e_c2"><text>&lt; 100</text></inputEntry><outputEntry id="x_c2"><text>20.5</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

func shapesDefs(t *testing.T) *tdmn.Definitions {
	t.Helper()
	defs, diags, err := tdmn.New().Compile(context.Background(), []byte(shapesXML))
	if err != nil {
		t.Fatalf("compile the shapes model: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("the shapes model has errors: %v", diags)
	}
	return defs
}

// evalShape runs one decision and returns the variable a business rule task would
// write for it — the whole path this is about, not an internal.
func evalShape(t *testing.T, decisionId string) (map[string]any, model.VariableValue) {
	t.Helper()
	out, _, err := evalDecision(context.Background(), shapesDefs(t), decisionId, map[string]any{"a": 7.0}, "the shapes model")
	if err != nil {
		t.Fatalf("evaluate %q: %v", decisionId, err)
	}
	return out, OutputVariable("result", out)
}

// What a decision's result becomes as a process variable, per shape. The exact
// decimal must survive: VarNumber keeps it as text, so nothing here rounds.
func TestADecisionResultKeepsTheTypeTheModelDeclares(t *testing.T) {
	for _, tc := range []struct {
		decision string
		wantKind model.VarKind
		wantText string
		why      string
	}{
		{"amount", model.VarNumber, "1250", "a decision table declaring a number output"},
		{"doubled", model.VarNumber, "14", "a literal expression declaring a number"},
		{"grade", model.VarString, "0800", "a string output stays a string, leading zero and all"},
		{"passed", model.VarBool, "", "a boolean was never affected"},
		{"untyped", model.VarString, "21", "nothing is declared, so nothing is assumed"},
		{"resultcell", model.VarNumber, "36", "a boxed context whose result cell declares a number"},
	} {
		t.Run(tc.decision, func(t *testing.T) {
			_, got := evalShape(t, tc.decision)
			if got.Kind != tc.wantKind {
				t.Errorf("%s: kind = %d, want %d (%s)", tc.decision, got.Kind, tc.wantKind, tc.why)
			}
			if tc.wantKind != model.VarBool && got.Text != tc.wantText {
				t.Errorf("%s: text = %q, want %q", tc.decision, got.Text, tc.wantText)
			}
		})
	}
}

// The condition that started this: a gateway comparing the result to a number has
// to route on it, with no number() wrapper in the model to paper over the type.
func TestAGatewayConditionCanCompareADecisionResultToANumber(t *testing.T) {
	_, v := evalShape(t, "amount")
	if v.Kind != model.VarNumber {
		t.Fatalf("kind = %d, want VarNumber — a string here is null in FEEL, which routes to the default flow", v.Kind)
	}
}

// A structured result carries its members' declared types too, so a downstream
// `premium.gross * 12` is arithmetic rather than null.
func TestTheMembersOfAStructuredResultKeepTheirDeclaredTypes(t *testing.T) {
	for _, tc := range []struct{ decision, want string }{
		{"columns", `{"code":"0800","loose":"7","net":99.5}`},
		{"premium", `{"gross":70,"label":"0800"}`},
	} {
		t.Run(tc.decision, func(t *testing.T) {
			_, got := evalShape(t, tc.decision)
			if got.Kind != model.VarJSON {
				t.Fatalf("kind = %d, want VarJSON", got.Kind)
			}
			if got.Text != tc.want {
				t.Errorf("text = %s, want %s", got.Text, tc.want)
			}
		})
	}
}

// A COLLECT table with no aggregation answers with a list, and the output column's
// declared type is each element's.
func TestEveryElementOfAListResultKeepsTheDeclaredType(t *testing.T) {
	_, got := evalShape(t, "collected")
	if got.Kind != model.VarJSON {
		t.Fatalf("kind = %d, want VarJSON", got.Kind)
	}
	if want := `[10,20.5]`; got.Text != want {
		t.Errorf("text = %s, want %s", got.Text, want)
	}
}

// The durable evaluation record (ADR-0066) is built from the same outputs, so an
// operator reading back how a decision was made sees the number as a number.
func TestTheRetainedEvaluationRecordShowsANumberAsANumber(t *testing.T) {
	out, _ := evalShape(t, "amount")
	if got, want := JSONObject(out), `{"amount":1250}`; got != want {
		t.Errorf("recorded outputs = %s, want %s", got, want)
	}
}

// json.Number is the carrier: a Go string type, so the exact decimal survives, and
// a distinct type, so nothing has to guess which strings are numbers. This pins
// that the conversion produces one rather than a float, which would round.
func TestTheCarrierIsAnExactDecimalAndNotAFloat(t *testing.T) {
	out, _, err := evalDecision(context.Background(), shapesDefs(t), "amount", map[string]any{"a": 7.0}, "the shapes model")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["amount"].(json.Number); !ok {
		t.Fatalf("output is %T, want json.Number — a float64 here would round a long decimal", out["amount"])
	}
}

// [tdmn.Definitions.Decision] accepts an id or a name, so whatever a caller
// addresses a decision by, the evaluation resolves. The type lookup that
// accompanies it has to accept the same, or the two silently disagree: the
// decision evaluates and its declared type is not found, so the number quietly
// stays a string — the exact defect this record exists to remove, reintroduced
// one addressing mode over.
//
// A decision's FEEL identifier is its `<variable name>`, which need not equal
// either. Addressing by *that* is a third mode the pinned temis does not offer at
// all — measured, `Decision("premium")` answers `no decision "premium"` — so it is
// not asserted here. It arrives with the version that binds by variable (#992),
// and the lookup has to grow with it.
func TestTheTypeLookupAcceptsEveryNameTheEvaluationDoes(t *testing.T) {
	const byVariableXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="byvar" name="byvar" namespace="http://atlas/dmn">
  <inputData id="id_x" name="x"><variable name="x" typeRef="number"/></inputData>
  <decision id="d_pol" name="Policy Premium">
    <variable name="premium" typeRef="number"/>
    <informationRequirement><requiredInput href="#id_x"/></informationRequirement>
    <decisionTable id="t_pol" hitPolicy="UNIQUE">
      <input id="i_pol"><inputExpression id="ie_pol" typeRef="number"><text>x</text></inputExpression></input>
      <output id="o_pol" name="premium" typeRef="number"/>
      <rule id="r_pol"><inputEntry id="e_pol"><text>&gt;= 0</text></inputEntry><outputEntry id="x_pol"><text>1250</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

	defs, diags, err := tdmn.New().Compile(context.Background(), []byte(byVariableXML))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", err, diags)
	}
	// The id and the name attribute both name the same decision, so both must
	// produce the same variable.
	for _, addr := range []string{"d_pol", "Policy Premium"} {
		t.Run(addr, func(t *testing.T) {
			out, _, err := evalDecision(context.Background(), defs, addr, map[string]any{"x": 4.0}, "the by-variable model")
			if err != nil {
				t.Fatalf("evaluate by %q: %v", addr, err)
			}
			got := OutputVariable("result", out)
			if got.Kind != model.VarNumber || got.Text != "1250" {
				t.Errorf("addressed by %q: kind = %d text = %q, want VarNumber %q", addr, got.Kind, got.Text, "1250")
			}
		})
	}
}
