package dmn_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// routingProcess builds Start → BusinessRuleTask → ExclusiveGateway with two
// branches: an End taken when the decision result equals "Roastbeef", and a
// parking service task (no worker) as the default. The business rule task reads
// its Season decision input from the process variable "season" (an input mapping,
// not a static input) and writes the decision result into the variable "dish".
//
// The gateway routing is the observable proof of the whole output path: the
// decision result reached a process variable the downstream condition could read.
func routingProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(dishDefKey, "dinner", 1)
	start := b.AddStartEvent()

	seasonSrc, err := expr.Compile("season", "season")
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	rule, err := b.AddBusinessRuleTaskMapped("Dish", "dish", nil,
		[]compiler.DecisionInputMapping{{Target: "Season", Source: seasonSrc}}, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTaskMapped: %v", err)
	}
	gw := b.AddExclusiveGateway()
	end := b.AddEndEvent()
	park := b.AddServiceTask("io.parks", 3) // no worker registered → a token parks here

	b.Connect(start, rule)
	b.Connect(rule, gw)

	toEnd := b.Connect(gw, end)
	cond, err := expr.Compile(`dish = "Roastbeef"`, "dish")
	if err != nil {
		t.Fatalf("compile condition: %v", err)
	}
	b.SetFlowCondition(toEnd, cond)
	toPark := b.Connect(gw, park)
	b.SetFlowDefault(toPark)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.BusinessRuleTask(cp.Node(rule).Detail).JobType
}

// TestBusinessRuleTaskRoutesOnMappedResult is the end-to-end proof of real
// input/output variable wiring: an input mapping feeds a process variable into the
// decision, and the decision's result — written back as a process variable — drives
// a downstream exclusive gateway. Winter yields "Roastbeef", so the instance takes
// the End branch and completes; Summer yields "Salad", so it falls to the default
// branch and parks on the (unworked) service task.
func TestBusinessRuleTaskRoutesOnMappedResult(t *testing.T) {
	cases := []struct {
		season      string
		wantActive  int // active element instances after driving
		description string
	}{
		{"Winter", 0, "Roastbeef routes to End; instance completes"},
		{"Summer", 1, "Salad falls to default; parks on the service task"},
	}
	for _, tc := range cases {
		t.Run(tc.season, func(t *testing.T) {
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

			cp, jobType := routingProcess(t)

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
			runner.HandleWithOutput(jobType, dmn.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg, nil))

			p.CreateInstance(cp.Key, model.VariableValue{Name: "season", Kind: model.VarString, Text: tc.season})
			if err := runner.Drive(); err != nil {
				t.Fatalf("Drive: %v", err)
			}

			ei, err := store.ActiveElementInstanceCount()
			if err != nil {
				t.Fatalf("ActiveElementInstanceCount: %v", err)
			}
			if ei != tc.wantActive {
				t.Fatalf("%s: active elements = %d, want %d", tc.description, ei, tc.wantActive)
			}
		})
	}
}
