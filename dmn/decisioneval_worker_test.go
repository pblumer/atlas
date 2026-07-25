package dmn_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestBusinessRuleTaskRetainsDecisionEvaluation drives a real DMN business rule
// task end to end and asserts the debugging record it leaves behind (ADR-0064): the
// worker evaluates the "Dish" decision for Season "Winter", and once the instance
// completes the store holds one decision-evaluation record under the instance whose
// inputs, outputs, and temis trace explain exactly how the decision was made. This
// is the "look up after the fact what happened" requirement, backed by the durable
// record rather than the diagnostics-only sink.
func TestBusinessRuleTaskRetainsDecisionEvaluation(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })

	cp, jobType := dishProcess(t, "Winter")

	reg := dmn.NewRegistry()
	if err := reg.Deploy(cp.Key, []byte(dishModel)); err != nil {
		t.Fatalf("Registry.Deploy: %v", err)
	}

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	lookup := func(uint64) *compiler.CompiledProcess { return cp }
	runner.HandleCompleting(jobType, dmn.Handler(store, lookup, reg, nil))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	// The instance has completed; find its key among the completed instances.
	var instanceKey uint64
	if err := store.CompletedProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		instanceKey = key
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	if instanceKey == 0 {
		t.Fatal("no completed instance found")
	}

	var evals []model.DecisionEvaluationValue
	if err := store.DecisionEvaluationHistory(instanceKey, func(_ int64, _ uint64, v *model.DecisionEvaluationValue) error {
		evals = append(evals, *v)
		return nil
	}); err != nil {
		t.Fatalf("DecisionEvaluationHistory: %v", err)
	}
	if len(evals) != 1 {
		t.Fatalf("decision evaluations = %d, want 1", len(evals))
	}
	e := evals[0]
	if e.DecisionId != "Dish" {
		t.Errorf("DecisionId = %q, want Dish", e.DecisionId)
	}
	if e.ProcessInstanceKey != instanceKey {
		t.Errorf("ProcessInstanceKey = %d, want %d", e.ProcessInstanceKey, instanceKey)
	}
	if e.ProcessDefKey != cp.Key {
		t.Errorf("ProcessDefKey = %d, want %d", e.ProcessDefKey, cp.Key)
	}
	if e.InputsJSON != `{"Season":"Winter"}` {
		t.Errorf("InputsJSON = %s, want {\"Season\":\"Winter\"}", e.InputsJSON)
	}
	if e.OutputsJSON != `{"Dish":"Roastbeef"}` {
		t.Errorf("OutputsJSON = %s, want {\"Dish\":\"Roastbeef\"}", e.OutputsJSON)
	}
	// The trace explains the evaluation: the table ran, tested Season = "Winter",
	// and the matching rule produced "Roastbeef".
	if e.TraceJSON == "" {
		t.Fatal("TraceJSON is empty, want the temis explanation")
	}
	for _, want := range []string{`"matched"`, "Winter", "Roastbeef"} {
		if !strings.Contains(e.TraceJSON, want) {
			t.Errorf("TraceJSON missing %q; got %s", want, e.TraceJSON)
		}
	}
}
