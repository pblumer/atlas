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

// The point of the whole record, asserted where Atlas can see it: a decision that
// declares a `date` input compares it as a date (ADR-0419, temis ADR-0040).
//
// Before the engine honoured the declaration, the ISO text arrived as a FEEL
// string, `< date("2026-06-01")` was null against it, no rule matched and the
// catch-all answered — the silent wrong answer. This locks in that Atlas passes a
// date through unconverted and gets a date comparison back, because the value
// Atlas sends is decoded JSON and JSON has no date: nothing but the declaration
// can make it one.
func TestADeclaredDateIsComparedAsADate(t *testing.T) {
	const dated = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="dm" name="Stichtag" namespace="http://atlas/dmn">
  <inputData id="d1" name="stichtag"><variable name="stichtag" typeRef="date"/></inputData>
  <decision id="Frist" name="Frist">
    <variable name="Frist" typeRef="string"/>
    <informationRequirement><requiredInput href="#d1"/></informationRequirement>
    <decisionTable id="ddt" hitPolicy="FIRST">
      <input id="din"><inputExpression id="die" typeRef="date"><text>stichtag</text></inputExpression></input>
      <output id="dout" typeRef="string"/>
      <rule id="dr1"><inputEntry id="de1"><text>&lt; date("2026-06-01")</text></inputEntry><outputEntry id="dv1"><text>"vorher"</text></outputEntry></rule>
      <rule id="dr2"><inputEntry id="de2"><text>-</text></inputEntry><outputEntry id="dv2"><text>"nachher"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`
	reg := dmn.NewRegistry()
	if err := reg.Deploy(3, []byte(dated)); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	for _, tc := range []struct{ in, want string }{
		{"2026-03-01", "vorher"},
		{"2026-09-01", "nachher"},
	} {
		out, err := reg.Evaluate(context.Background(), 3, "Frist", map[string]any{"stichtag": tc.in})
		if err != nil {
			t.Fatalf("Evaluate %s: %v", tc.in, err)
		}
		if got := out["Frist"]; got != tc.want {
			t.Errorf("Frist(%s) = %v, want %v — the date was compared as a string", tc.in, got, tc.want)
		}
	}

	// Text the declared type cannot be made from is refused rather than compared as
	// a string that matches nothing.
	if _, err := reg.Evaluate(context.Background(), 3, "Frist", map[string]any{"stichtag": "irgendwann"}); err == nil {
		t.Error("Evaluate with unparseable text: no error, want the job refused")
	}
}

// A composed decision is checked against the inputs a task actually sends it —
// the leaf inputs of its whole requirements cone, not the ones it declares
// directly (ADR-0419).
//
// This is the case a well-factored model has at the top, and the one a check
// built on a decision's own InputSchema misses entirely: `Entscheid` requires two
// decisions and no input data, so its declared inputs are empty and every value a
// task sends would pass unexamined. The task supplies `betrag` and `stoerungen`,
// because that is what the decisions underneath consume.
func TestAComposedDecisionIsCheckedAgainstWhatTheTaskSends(t *testing.T) {
	const layered = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="lm" name="Geschichtet" namespace="http://atlas/dmn">
  <inputData id="l1" name="betrag"><variable name="betrag" typeRef="number"/></inputData>
  <inputData id="l2" name="stoerungen"><variable name="stoerungen" typeRef="number"/></inputData>
  <decision id="Gross" name="Gross">
    <variable name="gross" typeRef="boolean"/>
    <informationRequirement><requiredInput href="#l1"/></informationRequirement>
    <literalExpression id="lg"><text>betrag &gt; 1000</text></literalExpression>
  </decision>
  <decision id="Sauber" name="Sauber">
    <variable name="sauber" typeRef="boolean"/>
    <informationRequirement><requiredInput href="#l2"/></informationRequirement>
    <literalExpression id="ls"><text>stoerungen = 0</text></literalExpression>
  </decision>
  <decision id="Entscheid" name="Entscheid">
    <variable name="entscheid" typeRef="string"/>
    <informationRequirement><requiredDecision href="#Gross"/></informationRequirement>
    <informationRequirement><requiredDecision href="#Sauber"/></informationRequirement>
    <literalExpression id="le"><text>if gross and sauber then "gross und sauber" else "sonst"</text></literalExpression>
  </decision>
</definitions>`
	reg := dmn.NewRegistry()
	if err := reg.Deploy(4, []byte(layered)); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	right := map[string]any{"betrag": 2000, "stoerungen": 0}
	out, err := reg.Evaluate(context.Background(), 4, "Entscheid", right)
	if err != nil {
		t.Fatalf("Evaluate with the right types: %v", err)
	}
	if got := out["entscheid"]; got != "gross und sauber" {
		t.Fatalf("entscheid = %v, want \"gross und sauber\"", got)
	}

	// A string where the leaf input declares a number. `betrag > 1000` is null
	// against it, `gross` is null, and the answer flips to "sonst" with nothing to
	// read — the silent wrong answer, two levels below the decision the task names.
	_, err = reg.Evaluate(context.Background(), 4, "Entscheid", map[string]any{"betrag": "2000", "stoerungen": 0})
	if err == nil {
		t.Fatal("Evaluate with a string two levels down: no error, want the job refused")
	}
	if !strings.Contains(err.Error(), "betrag") || !strings.Contains(err.Error(), "expects number") {
		t.Errorf("error = %q, want it to name the input and its declared type", err)
	}

	// The cone is the bound, not the whole model: an input no decision under
	// `Entscheid` reads is still ignored rather than refused.
	if _, err := reg.Evaluate(context.Background(), 4, "Gross", map[string]any{"betrag": 2000, "stoerungen": "viele"}); err != nil {
		t.Errorf("Evaluate of a decision whose cone excludes the bad input: %v, want it ignored", err)
	}
}
