package dmn_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
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

// dishProcess builds Start → BusinessRuleTask(cfg) → End (optionally with a
// holding service task after the rule task, so the instance stays alive for
// output-variable assertions), and returns it with the business-rule job type.
func dishProcess(t *testing.T, cfg compiler.BusinessRule, hold bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(dishDefKey, "dinner", 1)
	start := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTask(cfg)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(start, rule)
	if hold {
		hold := b.AddServiceTask("hold", 1) // no worker registered → instance waits here
		b.Connect(rule, hold)
		b.Connect(hold, end)
	} else {
		b.Connect(rule, end)
	}
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.BusinessRuleTask(cp.Node(rule).Detail).JobType
	return cp, jobType
}

func openStore(t *testing.T) (*wal.Log, *state.Store) {
	t.Helper()
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
	return log, store
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

func onlyInstanceKey(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("active instances = %d, want 1", len(keys))
	}
	return keys[0]
}

func variableJSON(t *testing.T, s *state.Store, piKey uint64, name string) (string, bool) {
	t.Helper()
	raw, ok, err := s.GetVariable(piKey, name)
	if err != nil {
		t.Fatalf("GetVariable %q: %v", name, err)
	}
	return string(raw), ok
}

func lookupOf(cp *compiler.CompiledProcess) dmn.ProcessLookup {
	return func(defKey uint64) *compiler.CompiledProcess {
		if defKey == cp.Key {
			return cp
		}
		return nil
	}
}

// TestBusinessRuleTaskMapsVariables is the input/output mapping slice end to
// end: an instance is seeded with a "theSeason" variable, the business rule task
// maps it into the decision's Season input, evaluates via temis, and writes the
// outputs back into the "dish" variable. A holding service task keeps the
// instance alive so the written-back variable can be observed in state.
func TestBusinessRuleTaskMapsVariables(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := dishProcess(t, compiler.BusinessRule{
		DecisionId:     "Dish",
		InputMappings:  map[string]string{"Season": "theSeason"},
		ResultVariable: "dish",
		Retries:        3,
	}, true)

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
	runner.Handle(jobType, dmn.Handler(store, lookupOf(cp), reg, func(r dmn.Result) { got = append(got, r) }))

	p.CreateInstanceWithVariables(cp.Key, []model.NamedVariable{
		{Name: "theSeason", Value: []byte(`"Winter"`)},
	})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	// Input mapping fed the seeded variable into the decision.
	if len(got) != 1 || got[0].Outputs["Dish"] != "Roastbeef" {
		t.Fatalf("results = %#v, want one Roastbeef (mapped from theSeason=Winter)", got)
	}

	// The instance waits at the holding service task, so its variables are live.
	if pi, _ := active(t, store); pi != 1 {
		t.Fatalf("active instances = %d, want 1 (held at service task)", pi)
	}
	piKey := onlyInstanceKey(t, store)

	// Output mapping wrote the decision result back into the "dish" variable.
	if v, ok := variableJSON(t, store, piKey, "dish"); !ok || v != `{"Dish":"Roastbeef"}` {
		t.Errorf("dish variable = %q (present=%v), want {\"Dish\":\"Roastbeef\"}", v, ok)
	}
	// The seeded input variable is untouched.
	if v, ok := variableJSON(t, store, piKey, "theSeason"); !ok || v != `"Winter"` {
		t.Errorf("theSeason variable = %q (present=%v), want \"Winter\"", v, ok)
	}
}

// TestBusinessRuleTaskStaticInputs runs a task whose inputs are static
// constants (no variables) straight through to completion.
func TestBusinessRuleTaskStaticInputs(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := dishProcess(t, compiler.BusinessRule{
		DecisionId:   "Dish",
		StaticInputs: map[string]any{"Season": "Winter"},
		Retries:      3,
	}, false)

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
	runner.Handle(jobType, dmn.Handler(store, lookupOf(cp), reg, func(r dmn.Result) { got = append(got, r) }))

	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if len(got) != 1 || got[0].Outputs["Dish"] != "Roastbeef" {
		t.Fatalf("results = %#v, want one Roastbeef", got)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestVariableMappingRecoversAcrossRestart proves the variable subsystem
// survives recovery: an instance is seeded with a variable, run to the waiting
// DMN job, crashed, and recovered — the seeded variable is rebuilt by replaying
// VariableCreated events through applyToState, and the recovered job then maps
// it and finishes the instance.
func TestVariableMappingRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := dishProcess(t, compiler.BusinessRule{
		DecisionId:     "Dish",
		InputMappings:  map[string]string{"Season": "theSeason"},
		ResultVariable: "dish",
		Retries:        3,
	}, false)
	clock := &fixedClock{}

	reg := dmn.NewRegistry()
	if err := reg.Deploy(cp.Key, []byte(dishModel)); err != nil {
		t.Fatalf("Registry.Deploy: %v", err)
	}

	// First run: seed a variable and stop at the waiting business rule job.
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
	p1.CreateInstanceWithVariables(cp.Key, []model.NamedVariable{
		{Name: "theSeason", Value: []byte(`"Summer"`)},
	})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	store1.Close()
	log1.Close()

	// Restart: recover from the log.
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

	// The seeded variable was rebuilt by replay.
	piKey := onlyInstanceKey(t, store2)
	if v, ok := variableJSON(t, store2, piKey, "theSeason"); !ok || v != `"Summer"` {
		t.Fatalf("recovered theSeason = %q (present=%v), want \"Summer\"", v, ok)
	}

	// The recovered job maps it and drives the instance to completion.
	var got []dmn.Result
	runner := job.NewRunner(store2, p2)
	runner.Handle(jobType, dmn.Handler(store2, lookupOf(cp), reg, func(r dmn.Result) { got = append(got, r) }))
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
