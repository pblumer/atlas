package dmn_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { c.t++; return c.t }

// dishModel is a minimal DMN 1.3 model with a single decision table that maps a
// Season to a Dish (unique hit policy). It is the decision an Atlas business
// rule task delegates to temis.
const dishModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs" name="dish" namespace="http://atlas/dmn">
  <inputData id="id_season" name="Season"/>
  <decision id="Dish" name="Dish">
    <informationRequirement><requiredInput href="#id_season"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in1" label="Season"><inputExpression id="ie1" typeRef="string"><text>Season</text></inputExpression></input>
      <output id="out1" label="Dish" name="Dish" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>"Winter"</text></inputEntry><outputEntry id="o1"><text>"Roastbeef"</text></outputEntry></rule>
      <rule id="r2"><inputEntry id="e2"><text>"Summer"</text></inputEntry><outputEntry id="o2"><text>"Salad"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

const dishDefKey = 42

// dishProcess builds Start → BusinessRuleTask(decision "Dish") → End with a
// static Season input, and returns it with the business-rule job type index.
func dishProcess(t *testing.T, season string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(dishDefKey, "dinner", 1)
	start := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTask("Dish", map[string]any{"Season": season}, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(start, rule)
	b.Connect(rule, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.BusinessRuleTask(cp.Node(rule).Detail).JobType
	return cp, jobType
}

func active(t *testing.T, s *state.Store) (pi, ei int) {
	t.Helper()
	pi, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	ei, err = s.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
}

// TestBusinessRuleTaskEvaluatesDMN is the vertical slice end to end: a business
// rule task creates a DMN job, the in-process temis worker evaluates the
// decision against the task's static input, completes the job, and the instance
// runs to completion — proving Atlas drives temis through the normal job path.
func TestBusinessRuleTaskEvaluatesDMN(t *testing.T) {
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

	var got []dmn.Result
	runner := job.NewRunner(store, p)
	lookup := func(defKey uint64) *compiler.CompiledProcess {
		if defKey == cp.Key {
			return cp
		}
		return nil
	}
	runner.Handle(jobType, dmn.Handler(store, lookup, reg, func(r dmn.Result) { got = append(got, r) }))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("decisions evaluated = %d, want 1", len(got))
	}
	if got[0].DecisionId != "Dish" {
		t.Errorf("decision id = %q, want Dish", got[0].DecisionId)
	}
	if dish := got[0].Outputs["Dish"]; dish != "Roastbeef" {
		t.Errorf("Dish = %#v, want Roastbeef", dish)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestBusinessRuleTaskRecoversAcrossRestart runs to the waiting DMN job,
// simulates a crash (reopen the log and store), recovers state by replaying the
// log, then lets the worker evaluate the decision and finish the instance —
// proving the business rule job survives recovery like any other job.
func TestBusinessRuleTaskRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := dishProcess(t, "Summer")
	clock := &fixedClock{}

	reg := dmn.NewRegistry()
	if err := reg.Deploy(cp.Key, []byte(dishModel)); err != nil {
		t.Fatalf("Registry.Deploy: %v", err)
	}
	lookup := func(uint64) *compiler.CompiledProcess { return cp }

	// First run: start an instance and stop at the waiting business rule job.
	log1, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store1, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p1 := engine.New(1, log1, store1, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	var before []uint64
	if err := store1.ActivatableJobs(jobType, func(k uint64) error { before = append(before, k); return nil }); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("before crash: activatable jobs = %d, want 1", len(before))
	}

	// Crash.
	store1.Close()
	log1.Close()

	// Restart: reopen and recover from the log.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	t.Cleanup(func() { store2.Close(); log2.Close() })
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := active(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 1", pi, ei)
	}

	// The recovered job evaluates and drives the instance to completion.
	var got []dmn.Result
	runner := job.NewRunner(store2, p2)
	runner.Handle(jobType, dmn.Handler(store2, lookup, reg, func(r dmn.Result) { got = append(got, r) }))
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if len(got) != 1 || got[0].Outputs["Dish"] != "Salad" {
		t.Fatalf("after recovery: results = %#v, want one Salad", got)
	}
	if pi, ei := active(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestRegistryEvaluatesDirectly checks the temis wrapper in isolation: deploy a
// model, evaluate a decision, get its output.
func TestRegistryEvaluatesDirectly(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(dishDefKey, []byte(dishModel)); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	out, err := reg.Evaluate(context.Background(), dishDefKey, "Dish", map[string]any{"Season": "Summer"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if out["Dish"] != "Salad" {
		t.Errorf("Dish = %#v, want Salad", out["Dish"])
	}
}

// TestRegistryUnknownDecision surfaces a missing decision as an error rather than
// a silent empty result.
func TestRegistryUnknownDecision(t *testing.T) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(dishDefKey, []byte(dishModel)); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if _, err := reg.Evaluate(context.Background(), dishDefKey, "Nope", nil); err == nil {
		t.Fatal("Evaluate of unknown decision: got nil error, want an error")
	}
	if _, err := reg.Evaluate(context.Background(), 999, "Dish", nil); err == nil {
		t.Fatal("Evaluate against undeployed def: got nil error, want an error")
	}
}
