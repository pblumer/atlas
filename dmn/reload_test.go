package dmn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// mixedModel bundles a decision that compiles with one that does not: "Menu" is a
// self-contained literal expression, "Broken" reads a variable nothing declares.
// temis compiles the model, reports the second as an error diagnostic, and leaves
// it present but not executable — which is exactly the shape a reload has to deal
// with when a diagnostic that did not exist at deploy time appears later.
const mixedModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="mixeddefs" name="mixed" namespace="http://atlas/dmn">
  <decision id="Menu" name="Menu"><literalExpression id="mle"><text>"Fixed"</text></literalExpression></decision>
  <decision id="Broken" name="Broken"><literalExpression id="ble"><text>Nonexistent + 1</text></literalExpression></decision>
</definitions>`

// TestReloadKeepsAModelWithErrorDiagnostics is the DMN half of
// ADR-draft-reload-skips-the-deploy-gate: a model snapshotted into a deployment
// record comes back even when today's temis reports errors on it, so one decision
// that stopped compiling cannot keep the whole server from starting. The
// diagnostics come back with it, for the caller to report.
func TestReloadKeepsAModelWithErrorDiagnostics(t *testing.T) {
	reg := dmn.NewRegistry()
	const key = 11
	problems, err := reg.Reload(key, []byte(mixedModel))
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if problems == "" {
		t.Fatal("Reload reported no problems, want the broken decision named")
	}
	if !strings.Contains(problems, "Broken") {
		t.Fatalf("problems = %q, want the offending decision id", problems)
	}
	// Registered, not merely tolerated: the decisions that do compile still answer,
	// under their deployment key and as the latest for their id.
	if out, err := reg.Evaluate(context.Background(), key, "Menu", nil); err != nil || out["Menu"] != "Fixed" {
		t.Fatalf("Evaluate(Menu) = %v, %v, want Fixed", out, err)
	}
	if out, err := reg.EvaluateLatest(context.Background(), "Menu", nil); err != nil || out["Menu"] != "Fixed" {
		t.Fatalf("EvaluateLatest(Menu) = %v, %v, want Fixed", out, err)
	}
}

// TestReloadReportsNothingForACleanModel keeps the reported problems meaningful:
// an ordinary model reloads with an empty string, so a caller can report drift on
// problems != "" without inspecting it.
func TestReloadReportsNothingForACleanModel(t *testing.T) {
	reg := dmn.NewRegistry()
	problems, err := reg.Reload(dishDefKey, []byte(dishModel))
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if problems != "" {
		t.Fatalf("problems = %q, want none", problems)
	}
	if out, err := reg.Evaluate(context.Background(), dishDefKey, "Dish", map[string]any{"Season": "Winter"}); err != nil || out["Dish"] != "Roastbeef" {
		t.Fatalf("Evaluate = %v, %v, want Roastbeef", out, err)
	}
}

// TestReloadStillFailsOnMalformedXML draws the same line the BPMN side draws:
// tolerating a diagnostic is not tolerating anything. XML temis cannot parse
// yields no model at all, so there is nothing to bring back and it stays an error.
func TestReloadStillFailsOnMalformedXML(t *testing.T) {
	reg := dmn.NewRegistry()
	if _, err := reg.Reload(1, []byte(malformedModel)); err == nil {
		t.Fatal("Reload of malformed XML: got nil error, want a compile error")
	}
}

// TestDeployStillRefusesWhatReloadTolerates is the other half: the gate stays on
// the deploy path, where the author is watching, so a model whose decision does
// not compile is still refused when it is deployed.
func TestDeployStillRefusesWhatReloadTolerates(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(11, []byte(mixedModel)); err == nil {
		t.Fatal("Deploy of a model with error diagnostics: got nil error, want a refusal")
	}
}
