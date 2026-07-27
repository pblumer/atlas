package dmn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// chainModel has A ← X and B ← A, X, so a decision's inputs include those feeding
// the sub-decision it depends on, and the shared input X is reached twice (once
// directly, once through A) — exercising transitive collection and dedup.
const chainModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="chain" namespace="http://atlas/dmn">
  <inputData id="x" name="X"/>
  <decision id="A" name="A">
    <informationRequirement><requiredInput href="#x"/></informationRequirement>
    <literalExpression id="la"><text>X</text></literalExpression>
  </decision>
  <decision id="B" name="B">
    <informationRequirement><requiredDecision href="#A"/></informationRequirement>
    <informationRequirement><requiredInput href="#x"/></informationRequirement>
    <literalExpression id="lb"><text>A + X</text></literalExpression>
  </decision>
</definitions>`

// errResolver returns a non-not-found (infrastructure) error, to drive Describe's
// error path.
type errResolver struct{}

func (errResolver) Resolve(context.Context, string) ([]byte, error) {
	return nil, errors.New("resolver down")
}

// TestDescribeTransitiveInputs proves a decision's inputs include those feeding the
// decisions it depends on, with the shared input listed once.
func TestDescribeTransitiveInputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chain.dmn"), []byte(chainModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	_, decisions, err := v.Describe(context.Background(), "chain")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	byName := map[string]dmn.DecisionInfo{}
	for _, d := range decisions {
		byName[d.Name] = d
	}
	if b, ok := byName["B"]; !ok {
		t.Fatalf("decision B missing from %v", decisions)
	} else if len(b.Inputs) != 1 || b.Inputs[0].Name != "X" {
		t.Errorf("B inputs = %+v, want a single X (transitive, deduped)", b.Inputs)
	}
}

// TestDescribeInvalidModel returns an empty (not error) result for a model that
// does not compile, so one broken reference does not blank the catalog.
func TestDescribeInvalidModel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.dmn"),
		[]byte(`<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="broken"><decision id="Bad" name="Bad"><literalExpression id="le"><text>1 +</text></literalExpression></decision></definitions>`), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	_, decisions, err := v.Describe(context.Background(), "broken")
	if err != nil || decisions != nil {
		t.Fatalf("Describe of an invalid model = %v/%v, want empty/nil", decisions, err)
	}
}

// TestDescribeResolverError surfaces an infrastructure resolver failure as an
// error (distinct from an unresolved handle).
func TestDescribeResolverError(t *testing.T) {
	v := dmn.NewValidator(errResolver{})
	if _, _, err := v.Describe(context.Background(), "x"); err == nil {
		t.Fatal("Describe with a failing resolver = nil error, want an error")
	}
}

// TestDescribeDecisions proves the decision picker's data source: a compiled model
// self-describes each decision's id, its input data, and its output — so the
// Modeler can list decisions and auto-fill a business rule task's mappings instead
// of making the author type them (ADR-0050).
func TestDescribeDecisions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dish.dmn"), []byte(dishModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	_, decisions, err := v.Describe(context.Background(), "dish")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	d := decisions[0]
	if d.ID != "Dish" || d.Name != "Dish" {
		t.Errorf("decision id/name = %q/%q, want Dish/Dish", d.ID, d.Name)
	}
	if len(d.Inputs) != 1 || d.Inputs[0].Name != "Season" {
		t.Errorf("inputs = %+v, want one Season input", d.Inputs)
	}
	if d.Output.Name != "Dish" {
		t.Errorf("output name = %q, want Dish", d.Output.Name)
	}
}

// TestDescribeUnresolved returns an empty (not error) result for a handle that
// resolves to nothing, so one broken reference does not blank the catalog.
func TestDescribeUnresolved(t *testing.T) {
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})
	name, decisions, err := v.Describe(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Describe of a missing model = %v, want nil error", err)
	}
	if name != "" || decisions != nil {
		t.Fatalf("Describe of a missing model = %q/%v, want empty", name, decisions)
	}
}
