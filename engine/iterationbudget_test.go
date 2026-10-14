package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// cardinalityLoop builds a multi-instance script task whose iteration count comes
// from a FEEL expression — which is where the danger is: the number is not in the
// model, it is whatever an instance variable happens to hold.
func cardinalityLoop(t *testing.T, key uint64, cardinality string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "iterations", 1)
	start := b.AddStartEvent()
	task := b.AddScriptTask(mustCompile(t, `1`), "item")
	b.SetMultiInstance(task, false, "", "", nil, mustCompile(t, cardinality), nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, task
}

// TestALoopThatAsksForTooMuchIsRefusedBeforeItAllocates is the audit's F16 case at
// its sharpest. The iteration count is read from an instance variable and the engine
// built the list from it — a variable holding a billion is a billion FEEL nulls,
// asked for in one call, before anything looks at the number.
//
// The check now happens before the allocation, and the refusal is an incident on the
// body rather than a process that dies taking its partition with it.
func TestALoopThatAsksForTooMuchIsRefusedBeforeItAllocates(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, task := cardinalityLoop(t, 1, "wanted")
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetMaxIterations(5)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "wanted", Kind: model.VarNumber, Text: "1000"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	all := incidents(t, h.store)
	if len(all) != 1 {
		t.Fatalf("incidents = %d, want exactly 1: %v", len(all), all)
	}
	var elKey uint64
	for k, v := range all {
		elKey = k
		if v.Reason != model.IncidentTooManyIterations {
			t.Errorf("incident reason = %d, want IncidentTooManyIterations (%d): %q", v.Reason, model.IncidentTooManyIterations, v.Message)
		}
		if v.ElementId != task {
			t.Errorf("incident on element %d, want the loop body (%d)", v.ElementId, task)
		}
	}
	// The body is parked holding its token, and no iteration was seeded.
	if _, ei := counts(t, h.store); ei != 1 {
		t.Fatalf("element instances = %d, want 1 (the parked body alone)", ei)
	}

	// Resolving re-evaluates: with the variable still too large it parks again, which
	// is what makes the resolve a retry rather than a way to clear the refusal.
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	again := incidents(t, h.store)
	if len(again) != 1 {
		t.Fatalf("incidents after resolve = %d, want 1 (still too many)", len(again))
	}

	// Bring the count under the limit and resolve again: the loop runs.
	instance := model.NewKey(1, 1)
	p.SetVariables(instance, instance, "operator",
		model.VariableValue{Name: "wanted", Kind: model.VarNumber, Text: "3"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if left := incidents(t, h.store); len(left) != 0 {
		t.Fatalf("incidents after fixing the count = %d, want 0: %v", len(left), left)
	}
	if len(completedInstances(t, h.store)) != 1 {
		t.Error("the instance did not finish once the loop was allowed to run")
	}
}

// TestALoopAtTheLimitStillRuns pins the boundary the plan asks for: limit−1, limit
// and limit+1. The limit itself is allowed — a budget that refused the number it
// names would be a budget of one less.
func TestALoopAtTheLimitStillRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		want   string
		refuse bool
	}{
		{"below the limit", "4", false},
		{"exactly the limit", "5", false},
		{"one over", "6", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openHarness(t, t.TempDir())
			defer h.close(t)

			cp, _ := cardinalityLoop(t, 2, tc.want)
			p := engine.New(1, h.log, h.store, &manualClock{})
			p.SetMaxIterations(5)
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			p.CreateInstance(cp.Key)
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v", err)
			}
			raised := len(incidents(t, h.store))
			if tc.refuse && raised != 1 {
				t.Fatalf("incidents = %d, want 1", raised)
			}
			if !tc.refuse {
				if raised != 0 {
					t.Fatalf("incidents = %d, want 0", raised)
				}
				if len(completedInstances(t, h.store)) != 1 {
					t.Error("the instance did not finish")
				}
			}
		})
	}
}

// TestACollectionTooLargeToSeedIsRefusedToo: the list itself already exists — it came
// from a variable — but seeding an element instance and a variable event per element
// does not, and that is the part that is unbounded. So the same budget applies.
func TestACollectionTooLargeToSeedIsRefusedToo(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(3, "big-collection", 1)
	start := b.AddStartEvent()
	task := b.AddScriptTask(mustCompile(t, `1`), "item")
	b.SetMultiInstance(task, false, "item", "", mustCompile(t, "[1,2,3,4,5,6,7]"), nil, nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetMaxIterations(5)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	all := incidents(t, h.store)
	if len(all) != 1 {
		t.Fatalf("incidents = %d, want 1 for a seven-element collection under a limit of five", len(all))
	}
	for _, v := range all {
		if v.Reason != model.IncidentTooManyIterations {
			t.Errorf("reason = %d, want IncidentTooManyIterations", v.Reason)
		}
	}
}
