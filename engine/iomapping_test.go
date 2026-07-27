package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// TestInputMappingWritesLocalScope drives an inline script task whose input
// mapping computes a local variable the script reads: the local is visible to the
// activity's own FEEL (scope-chain resolution) but is dropped when the activity
// completes, so it never leaks into the process scope (ADR-0068).
func TestInputMappingWritesLocalScope(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "iomap-in", 1)
	start := b.AddStartEvent()
	task := b.AddScriptTask(mustCompile(t, "amount * 2"), "doubled")
	b.AddInputMapping(task, "amount", mustCompile(t, "x + 1"))
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "x", Kind: model.VarNumber, Text: "20"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	root := model.NewKey(1, 1)
	// The script saw the mapped local (amount = x+1 = 21) and doubled it.
	if got := readVar(t, h.store, root, "doubled"); got == nil || got.Text != "42" {
		t.Fatalf("doubled = %+v, want 42 (script read the input-mapped local)", got)
	}
	// The input local did not leak into the process scope.
	if got := readVar(t, h.store, root, "amount"); got != nil {
		t.Errorf("amount = %+v at the process scope, want nil (a dropped activity-local)", got)
	}
	// The inherited variable is untouched.
	if got := readVar(t, h.store, root, "x"); got == nil || got.Text != "20" {
		t.Errorf("x = %+v, want 20 (unchanged)", got)
	}
}

// TestOutputMappingPromotesToParent drives an inline script task whose result is
// written to its activity-local scope and promoted to the process scope only
// through an output mapping; the raw result is dropped with the local scope
// (ADR-0068).
func TestOutputMappingPromotesToParent(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp := outputMappingProcess(t, 1)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	root := model.NewKey(1, 1)
	if got := readVar(t, h.store, root, "finalStatus"); got == nil || got.Text != "ok" {
		t.Fatalf("finalStatus = %+v, want ok (promoted by the output mapping)", got)
	}
	// The raw result variable was written to the local scope and dropped, never
	// promoted, so it is absent from the process scope.
	if got := readVar(t, h.store, root, "status"); got != nil {
		t.Errorf("status = %+v at the process scope, want nil (raw result dropped)", got)
	}
}

// outputMappingProcess builds start → script task (result "status" = "ok",
// output-mapped to "finalStatus") → end.
func outputMappingProcess(t *testing.T, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "iomap-out", 1)
	start := b.AddStartEvent()
	task := b.AddScriptTask(mustCompile(t, `"ok"`), "status")
	b.AddOutputMapping(task, "finalStatus", mustCompile(t, "status"))
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestOutputMappingRecoversAcrossRestart is the recovery property for I/O output
// mappings: the promoted variable and the dropped local must be identical after
// replaying the log as they were built live (invariants I4/I6).
func TestOutputMappingRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp := outputMappingProcess(t, 1)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	root := model.NewKey(1, 1)
	live := readVar(t, h1.store, root, "finalStatus")
	if live == nil || live.Text != "ok" {
		t.Fatalf("live finalStatus = %+v, want ok", live)
	}
	h1.close(t)

	// Restart and replay.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if replayed := readVar(t, h2.store, root, "finalStatus"); replayed == nil || replayed.Text != live.Text {
		t.Fatalf("replayed finalStatus = %+v, want %q", replayed, live.Text)
	}
	if got := readVar(t, h2.store, root, "status"); got != nil {
		t.Errorf("replayed status = %+v, want nil (drop reproduced on replay)", got)
	}
}

// TestJobOutputMappingPromotesResult exercises the job path: a service task's
// worker result is written to the activity-local scope, an output mapping promotes
// a selected value to the process scope, and the raw result is dropped (ADR-0068).
func TestJobOutputMappingPromotesResult(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "iomap-job", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("worker", 3)
	b.AddOutputMapping(task, "promoted", mustCompile(t, "raw"))
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(task).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jobKey := singleActivatableJob(t, h.store, jobType)
	p.CompleteJob(jobKey, model.VariableValue{Name: "raw", Kind: model.VarString, Text: "hello"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}

	root := model.NewKey(1, 1)
	if got := readVar(t, h.store, root, "promoted"); got == nil || got.Text != "hello" {
		t.Fatalf("promoted = %+v, want hello (output mapping over the worker result)", got)
	}
	if got := readVar(t, h.store, root, "raw"); got != nil {
		t.Errorf("raw = %+v at the process scope, want nil (worker result dropped)", got)
	}
}

// TestJobWithoutOutputMappingMergesToParent confirms the compatibility path is
// unchanged: with no output mappings a worker's result variables merge straight
// into the process scope, exactly as before ADR-0068.
func TestJobWithoutOutputMappingMergesToParent(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "iomap-none", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("worker", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(task).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)
	p.CompleteJob(jobKey, model.VariableValue{Name: "raw", Kind: model.VarString, Text: "hello"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}

	root := model.NewKey(1, 1)
	if got := readVar(t, h.store, root, "raw"); got == nil || got.Text != "hello" {
		t.Fatalf("raw = %+v, want hello merged into the process scope (unchanged behavior)", got)
	}
}
