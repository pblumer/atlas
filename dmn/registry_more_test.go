package dmn_test

import (
	"context"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// malformedModel is not well-formed DMN XML, so temis fails to parse it and
// Compile returns a hard error.
const malformedModel = `<this is not dmn`

// erroneousModel is well-formed XML whose decision references an undeclared
// variable: temis compiles without a hard error but reports a diagnostic with
// errors, which Deploy must surface.
const erroneousModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="bad" name="bad" namespace="http://atlas/dmn">
  <decision id="Dec" name="Dec">
    <literalExpression><text>Nonexistent + 1</text></literalExpression>
  </decision>
</definitions>`

// TestDeployRejectsMalformedXML covers Deploy's compile-error path.
func TestDeployRejectsMalformedXML(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(1, []byte(malformedModel)); err == nil {
		t.Fatal("Deploy of malformed XML: got nil error, want a compile error")
	}
}

// TestDeployRejectsModelWithDiagnostics covers Deploy's diags.HasErrors path: a
// parseable model that nonetheless fails semantic checks.
func TestDeployRejectsModelWithDiagnostics(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(2, []byte(erroneousModel)); err == nil {
		t.Fatal("Deploy of model with errors: got nil error, want a diagnostics error")
	}
}

// TestEvaluateContextCancelled covers Evaluate's decision-evaluation error path.
// temis returns FEEL null (not an error) for runtime issues, but it honors a
// cancelled context by returning an error before evaluating — a deterministic way
// to exercise the failure branch.
func TestEvaluateContextCancelled(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(dishDefKey, []byte(dishModel)); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reg.Evaluate(ctx, dishDefKey, "Dish", map[string]any{"Season": "Winter"}); err == nil {
		t.Fatal("Evaluate with cancelled context: got nil error, want an error")
	}
}
