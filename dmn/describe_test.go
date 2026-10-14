package dmn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// approvalServiceModel publishes one decision service over two decisions: Verdict is
// what it answers with, Score is a step on the way, and Amount is what a caller
// supplies. It is the shape the picker has to get right — a caller addresses the
// service, not the decisions inside it.
const approvalServiceModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="Approval" namespace="http://atlas/dmn">
  <decisionService id="svc" name="Approval">
    <variable name="Approval" typeRef="string"/>
    <outputDecision href="#verdict"/>
    <encapsulatedDecision href="#score"/>
    <inputData href="#amount"/>
  </decisionService>
  <inputData id="amount" name="Amount">
    <variable name="Amount" typeRef="number"/>
  </inputData>
  <decision id="score" name="Score">
    <variable name="Score" typeRef="number"/>
    <informationRequirement><requiredInput href="#amount"/></informationRequirement>
    <literalExpression id="ls"><text>Amount / 1000</text></literalExpression>
  </decision>
  <decision id="verdict" name="Verdict">
    <variable name="Verdict" typeRef="string"/>
    <informationRequirement><requiredDecision href="#score"/></informationRequirement>
    <literalExpression id="lv"><text>if Score &gt; 1 then "refer" else "approve"</text></literalExpression>
  </decision>
</definitions>`

// TestDescribeOffersTheServiceFirst proves the picker's data source offers a decision
// service beside the decisions, and offers it first.
//
// Describing only the decisions is what put a service out of reach: a business rule
// task calls either with the same one string, so a catalog that listed only
// decisions left the service to arrive by the deployed route instead — under no
// model handle, and therefore in no application.
//
// First, not merely present: a service is the published interface over part of the
// model and the decisions in it are its workings. Shown the interface first, an
// author picks the interface.
func TestDescribeOffersTheServiceFirst(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "approval.dmn"), []byte(approvalServiceModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	name, offered, err := v.Describe(context.Background(), "approval")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if name != "Approval" {
		t.Errorf("model name = %q, want Approval", name)
	}
	if len(offered) != 3 {
		t.Fatalf("offered = %d entries, want 3 (the service and its two decisions): %+v", len(offered), offered)
	}
	svc := offered[0]
	if !svc.Service || svc.ID != "Approval" {
		t.Fatalf("first entry = %+v, want the decision service Approval", svc)
	}
	// Output decisions, then the ones it evaluates internally. An input decision
	// would not be here: that is the caller's boundary, outside the service.
	if got := strings.Join(svc.Members, ","); got != "Verdict,Score" {
		t.Errorf("service members = %q, want \"Verdict,Score\"", got)
	}
	if len(svc.Inputs) != 1 || svc.Inputs[0].Name != "Amount" {
		t.Errorf("service inputs = %+v, want one Amount", svc.Inputs)
	}
	// The decisions are still offered: a service being listed does not hide them,
	// because calling one directly stays legal — the picker only says which is which.
	rest := map[string]bool{}
	for _, d := range offered[1:] {
		if d.Service {
			t.Errorf("entry %q after the service is marked as a service too", d.ID)
		}
		rest[d.ID] = true
	}
	if !rest["Score"] || !rest["Verdict"] {
		t.Errorf("decisions offered = %v, want both Score and Verdict", rest)
	}
}

// TestDescribeTellsAPublishedDecisionFromAWorking proves the two halves of "made of"
// come back apart.
//
// DMN §10.4 splits them and Members deliberately merges them, because both are what
// the service is built from. The difference is the whole point of a service, though:
// an output decision is what it answers with, so calling that directly gets the same
// value by a longer route, while an encapsulated one is a working — a caller bound to
// it has reached past the interface into an arrangement the service exists to be free
// to change. Nothing later says a word about it: it runs, and it answers correctly.
// So a picker that cannot tell the two apart cannot warn about the one that matters.
func TestDescribeTellsAPublishedDecisionFromAWorking(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "approval.dmn"), []byte(approvalServiceModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	_, offered, err := v.Describe(context.Background(), "approval")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	svc := offered[0]
	if !svc.Service {
		t.Fatalf("first entry = %+v, want the decision service", svc)
	}
	// Members is unchanged — everything the service is made of, output decisions first.
	if got := strings.Join(svc.Members, ","); got != "Verdict,Score" {
		t.Errorf("service members = %q, want \"Verdict,Score\"", got)
	}
	// Internal is the encapsulated half only. Verdict is published and must not be in
	// it: marking the service's own answer as a working would put a warning on the
	// pick an author is supposed to make.
	if got := strings.Join(svc.Internal, ","); got != "Score" {
		t.Errorf("service internal = %q, want \"Score\"", got)
	}
	// And a plain decision claims neither.
	for _, d := range offered[1:] {
		if len(d.Internal) > 0 {
			t.Errorf("%q names internal decisions %v, want none: it is not a service", d.ID, d.Internal)
		}
	}
}

// TestDescribeNamesNoMembersForAPlainDecision proves Members is a statement about a
// service and nothing else: a model without one offers decisions that belong to
// nothing, and say so by naming no members.
func TestDescribeNamesNoMembersForAPlainDecision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chain.dmn"), []byte(chainModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	_, offered, err := v.Describe(context.Background(), "chain")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(offered) == 0 {
		t.Fatal("offered nothing, want the model's decisions")
	}
	for _, d := range offered {
		if d.Service || len(d.Members) > 0 {
			t.Errorf("%q = service %v with members %v, want a plain decision with none", d.ID, d.Service, d.Members)
		}
	}
}
