package dmn

import (
	"context"
	"encoding/json"
	"testing"
)

// A business rule task that calls a decision *service* used to retain its inputs
// and outputs and nothing about how it got there: temis's service evaluation took
// no option to trace with, so ADR-0066's record was missing exactly the half it
// exists for. The option landed upstream (temis#226) and evalService threads it
// through, so an evaluation made from now on carries the rules.
//
// These tests drive the registry — the seam a business rule task passes through —
// over the shape that provoked the gap: a credit approval published as a service,
// with a rating handed in at the boundary.

// serviceTableXML is a trimmed credit approval. Its output decision is a decision
// TABLE (so there are rules to record), one required decision is a literal
// expression (so the trace shows only tables, which is a fact about the model),
// and the rating is an *input decision* — supplied by the caller, never computed,
// which is the boundary a service exists to draw.
const serviceTableXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="kredit" name="Kreditfreigabe" namespace="http://atlas/dmn/kredit">
  <inputData id="in_einkommen" name="einkommen"><variable name="einkommen" typeRef="number"/></inputData>
  <inputData id="in_betrag" name="betrag"><variable name="betrag" typeRef="number"/></inputData>
  <inputData id="in_laufzeit" name="laufzeitMonate"><variable name="laufzeitMonate" typeRef="number"/></inputData>
  <inputData id="in_stoerungen" name="zahlungsstoerungen"><variable name="zahlungsstoerungen" typeRef="number"/></inputData>

  <decision id="dec_bonitaet" name="Bonität">
    <variable name="bonitaet" typeRef="string"/>
    <informationRequirement id="ir_b1"><requiredInput href="#in_stoerungen"/></informationRequirement>
    <decisionTable id="dt_bonitaet" hitPolicy="FIRST">
      <input id="i_bon"><inputExpression id="ie_bon" typeRef="number"><text>zahlungsstoerungen</text></inputExpression></input>
      <output id="o_bon" name="bonitaet" typeRef="string"/>
      <rule id="r_bon_1"><inputEntry id="e_bon_1"><text>&gt;= 1</text></inputEntry><outputEntry id="x_bon_1"><text>"C"</text></outputEntry></rule>
      <rule id="r_bon_2"><inputEntry id="e_bon_2"><text>-</text></inputEntry><outputEntry id="x_bon_2"><text>"A"</text></outputEntry></rule>
    </decisionTable>
  </decision>

  <decision id="dec_tragbarkeit" name="Tragbarkeit">
    <variable name="tragbarkeit" typeRef="number"/>
    <informationRequirement id="ir_t1"><requiredInput href="#in_betrag"/></informationRequirement>
    <informationRequirement id="ir_t2"><requiredInput href="#in_laufzeit"/></informationRequirement>
    <informationRequirement id="ir_t3"><requiredInput href="#in_einkommen"/></informationRequirement>
    <literalExpression id="le_t"><text>(betrag / laufzeitMonate) / einkommen</text></literalExpression>
  </decision>

  <decision id="dec_entscheid" name="Kreditentscheid">
    <variable name="entscheid" typeRef="string"/>
    <informationRequirement id="ir_e1"><requiredDecision href="#dec_bonitaet"/></informationRequirement>
    <informationRequirement id="ir_e2"><requiredDecision href="#dec_tragbarkeit"/></informationRequirement>
    <decisionTable id="dt_entscheid" hitPolicy="FIRST">
      <input id="i_e1"><inputExpression id="ie_e1" typeRef="string"><text>bonitaet</text></inputExpression></input>
      <input id="i_e2"><inputExpression id="ie_e2" typeRef="number"><text>tragbarkeit</text></inputExpression></input>
      <output id="o_e" name="entscheid" typeRef="string"/>
      <rule id="r_e_1">
        <inputEntry id="e_e_1a"><text>"A"</text></inputEntry>
        <inputEntry id="e_e_1b"><text>&lt;= 0.35</text></inputEntry>
        <outputEntry id="x_e_1"><text>"bewilligt"</text></outputEntry>
      </rule>
      <rule id="r_e_2">
        <inputEntry id="e_e_2a"><text>"C"</text></inputEntry>
        <inputEntry id="e_e_2b"><text>&lt;= 0.15</text></inputEntry>
        <outputEntry id="x_e_2"><text>"manuelle Prüfung"</text></outputEntry>
      </rule>
      <rule id="r_e_3">
        <inputEntry id="e_e_3a"><text>-</text></inputEntry>
        <inputEntry id="e_e_3b"><text>-</text></inputEntry>
        <outputEntry id="x_e_3"><text>"abgelehnt"</text></outputEntry>
      </rule>
    </decisionTable>
  </decision>

  <decisionService id="svc_kredit" name="Kreditfreigabe">
    <variable name="Kreditfreigabe" typeRef="string"/>
    <outputDecision href="#dec_entscheid"/>
    <encapsulatedDecision href="#dec_tragbarkeit"/>
    <inputDecision href="#dec_bonitaet"/>
    <inputData href="#in_betrag"/>
    <inputData href="#in_laufzeit"/>
    <inputData href="#in_einkommen"/>
  </decisionService>
</definitions>`

// traceTables is the shape the DMN worker retains and every reader parses: temis's
// trace tree, whose JSON tags are its wire contract.
type traceTables struct {
	Tables []struct {
		HitPolicy string `json:"hitPolicy"`
		Inputs    []struct {
			Expression string `json:"expression"`
			Value      any    `json:"value"`
		} `json:"inputs"`
		Rules []struct {
			Index      int    `json:"index"`
			ID         string `json:"id"`
			Matched    bool   `json:"matched"`
			Conditions []struct {
				Input   string `json:"input"`
				Entry   string `json:"entry"`
				Matched bool   `json:"matched"`
			} `json:"conditions"`
			Outputs []any `json:"outputs"`
		} `json:"rules"`
		Matched []int `json:"matched"`
	} `json:"tables"`
}

// creditCase is the case the report was written from: 60 000 over 48 months on
// 5 000 a month with a C rating handed in. 1250/5000 = 0.25, past what a C
// carries, so the catch-all takes it.
func creditCase() map[string]any {
	return map[string]any{"betrag": 60000, "laufzeitMonate": 48, "einkommen": 5000, "bonitaet": "C"}
}

func TestAServiceEvaluationCarriesItsRules(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(21, []byte(serviceTableXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	out, trace, err := r.EvaluateTraced(context.Background(), 21, "Kreditfreigabe", creditCase())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := out["entscheid"]; got != "abgelehnt" {
		t.Fatalf("entscheid = %#v, want abgelehnt", got)
	}
	if len(trace) == 0 {
		t.Fatal("a service evaluation recorded no trace; it is the half of the record that says why")
	}
	var tr traceTables
	if err := json.Unmarshal(trace, &tr); err != nil {
		t.Fatalf("decode trace: %v", err)
	}

	// One table: the output decision's. Tragbarkeit is a literal expression and has
	// no rules to report, which is a silence about the model rather than the trace.
	if len(tr.Tables) != 1 {
		t.Fatalf("traced %d tables, want only the decision table that ran: %s", len(tr.Tables), trace)
	}
	tbl := tr.Tables[0]
	if len(tbl.Inputs) != 2 || tbl.Inputs[0].Expression != "bonitaet" || tbl.Inputs[1].Expression != "tragbarkeit" {
		t.Fatalf("traced columns = %+v, want bonitaet and tragbarkeit", tbl.Inputs)
	}
	if tbl.Inputs[0].Value != "C" {
		t.Errorf("the table read bonitaet = %v, want the C handed in at the boundary", tbl.Inputs[0].Value)
	}
	if len(tbl.Matched) != 1 || tbl.Matched[0] != 2 {
		t.Fatalf("matched = %v, want the catch-all (index 2)", tbl.Matched)
	}
	if got := tbl.Rules[2].Outputs; len(got) != 1 || got[0] != "abgelehnt" {
		t.Errorf("the rule that fired carried %v, want [abgelehnt]", got)
	}
	// The near miss is the whole explanation of the rejection: the rating matched
	// and the ratio did not. Without it a reader is told the answer, not the reason.
	near := tbl.Rules[1]
	if near.Matched {
		t.Fatalf("rule 2 matched, but 0.25 is past <= 0.15")
	}
	if len(near.Conditions) != 2 || !near.Conditions[0].Matched || near.Conditions[1].Matched {
		t.Errorf("rule 2 conditions = %+v, want the rating held and the ratio failed", near.Conditions)
	}
}

// The boundary holds in the trace as it does in the result. Bonität is an input
// decision, supplied rather than computed, so its table never ran — reporting it
// would be an account of an evaluation that did not happen.
func TestAServiceTraceStopsAtTheBoundary(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(22, []byte(serviceTableXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	_, trace, err := r.EvaluateTraced(context.Background(), 22, "Kreditfreigabe", creditCase())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var tr traceTables
	if err := json.Unmarshal(trace, &tr); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	for _, tbl := range tr.Tables {
		for _, col := range tbl.Inputs {
			if col.Expression == "zahlungsstoerungen" {
				t.Fatalf("the rating's own table was traced, but the caller supplied the rating: %s", trace)
			}
		}
	}
}

// Asking for the trace must not move the answer: the decision inside the service
// is still bounded by what the caller supplied, and the values are the same ones
// the untraced path produced.
func TestTracingAServiceDoesNotChangeItsAnswer(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(23, []byte(serviceTableXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	for _, c := range []struct {
		name     string
		bonitaet string
		want     string
	}{
		{"a rating that carries it", "A", "bewilligt"},
		{"a rating that does not", "C", "abgelehnt"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := creditCase()
			in["bonitaet"] = c.bonitaet
			out, trace, err := r.EvaluateTraced(context.Background(), 23, "Kreditfreigabe", in)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got := out["entscheid"]; got != c.want {
				t.Errorf("entscheid = %#v, want %q — the boundary still decides it", got, c.want)
			}
			if len(trace) == 0 {
				t.Errorf("no trace recorded")
			}
		})
	}
}

// A service whose decisions are all literal expressions traces to a trace with no
// tables — a valid answer about the model, and a different thing from no trace at
// all. The existing service fixture is exactly that shape.
func TestAServiceWithNoTableLogicTracesNoTables(t *testing.T) {
	r := NewRegistry()
	if err := r.DeployDecision(24, []byte(serviceXML)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	_, trace, err := r.EvaluateTraced(context.Background(), 24, "Praemienrechnung", serviceInputs())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(trace) == 0 {
		t.Fatal("want a trace with no tables, not the absence of one")
	}
	var tr traceTables
	if err := json.Unmarshal(trace, &tr); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	if len(tr.Tables) != 0 {
		t.Errorf("traced %d tables for a model whose decisions are literal expressions", len(tr.Tables))
	}
}
