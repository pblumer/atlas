package dmn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// A business rule task whose variable carries the wrong type refuses the job
// (ADR-0419).
//
// The failure it prevents is silent. A wrongly-typed variable does not error in
// FEEL: every comparison that reads it is null, so no rule matches, the catch-all
// row answers, and the token carries on with a plausible wrong result. Nothing
// downstream can tell it from a right one, and there is no diagnostic, no trace
// entry and no incident to read. Refusing makes the job fail, and the retries run
// out into an incident (ADR-0061).
const typedModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="tm" name="Freigabe" namespace="http://atlas/dmn">
  <inputData id="ti1" name="betrag"><variable name="betrag" typeRef="number"/></inputData>
  <decision id="Freigabe" name="Freigabe">
    <variable name="Freigabe" typeRef="string"/>
    <informationRequirement><requiredInput href="#ti1"/></informationRequirement>
    <decisionTable id="tdt" hitPolicy="FIRST">
      <input id="tin"><inputExpression id="tie" typeRef="number"><text>betrag</text></inputExpression></input>
      <output id="tout" typeRef="string"/>
      <rule id="tr1"><inputEntry id="te1"><text>&lt; 1000</text></inputEntry><outputEntry id="tv1"><text>"klein"</text></outputEntry></rule>
      <rule id="tr2"><inputEntry id="te2"><text>-</text></inputEntry><outputEntry id="tv2"><text>"gross"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

func typedRegistry(t *testing.T) *dmn.Registry {
	t.Helper()
	reg := dmn.NewRegistry()
	if err := reg.Deploy(1, []byte(typedModel)); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	return reg
}

func TestAWronglyTypedInputRefusesTheEvaluation(t *testing.T) {
	reg := typedRegistry(t)

	// The right type answers, so the refusal below is about the type and not about
	// the model being broken.
	out, err := reg.Evaluate(context.Background(), 1, "Freigabe", map[string]any{"betrag": 500})
	if err != nil {
		t.Fatalf("Evaluate with a number: %v", err)
	}
	if got := out["Freigabe"]; got != "klein" {
		t.Fatalf("Freigabe = %v, want klein", got)
	}

	// A string where the model declares a number. Before this, the comparison was
	// null, no rule matched, and the catch-all answered "gross" — a wrong answer
	// with nothing to read.
	_, err = reg.Evaluate(context.Background(), 1, "Freigabe", map[string]any{"betrag": "500"})
	if err == nil {
		t.Fatal("Evaluate with a string: no error, want the job refused")
	}
	if !strings.Contains(err.Error(), "betrag") {
		t.Errorf("error = %q, want it to name the input", err)
	}
	if !strings.Contains(err.Error(), "expects number") {
		t.Errorf("error = %q, want it to name the declared type", err)
	}
}

// An input the decision does not declare stays ignored. A business rule task's
// io-mapping may carry a row the decision never reads — one mapping shared across
// decisions, or a row left behind when a column went away — and temis simply does
// not look at it. Failing the job over a value that costs nothing would break
// processes that run correctly today.
func TestAnUndeclaredInputIsStillIgnored(t *testing.T) {
	reg := typedRegistry(t)

	out, err := reg.Evaluate(context.Background(), 1, "Freigabe",
		map[string]any{"betrag": 500, "waehrung": "CHF"})
	if err != nil {
		t.Fatalf("Evaluate with an extra input: %v, want it ignored", err)
	}
	if got := out["Freigabe"]; got != "klein" {
		t.Errorf("Freigabe = %v, want klein", got)
	}
}

// Every mismatch is named, not just the first: an operator reading the incident
// should see the whole problem rather than fix one input and meet the next.
func TestEveryTypeMismatchIsNamed(t *testing.T) {
	const two = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="tm2" name="Zwei" namespace="http://atlas/dmn">
  <inputData id="u1" name="betrag"><variable name="betrag" typeRef="number"/></inputData>
  <inputData id="u2" name="aktiv"><variable name="aktiv" typeRef="boolean"/></inputData>
  <decision id="Zwei" name="Zwei">
    <informationRequirement><requiredInput href="#u1"/></informationRequirement>
    <informationRequirement><requiredInput href="#u2"/></informationRequirement>
    <literalExpression id="ul"><text>"x"</text></literalExpression>
  </decision>
</definitions>`
	reg := dmn.NewRegistry()
	if err := reg.Deploy(2, []byte(two)); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	_, err := reg.Evaluate(context.Background(), 2, "Zwei",
		map[string]any{"betrag": "500", "aktiv": "ja"})
	if err == nil {
		t.Fatal("no error, want both mismatches reported")
	}
	for _, want := range []string{"betrag", "aktiv"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q too", err, want)
		}
	}
}
