package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// manualClock is a deterministic, monotonically increasing clock.
type manualClock struct{ t int64 }

func (c *manualClock) Now() int64 { c.t++; return c.t }

const (
	defKey  = 7
	jobName = "work"
)

// linearProcess builds Start → ServiceTask → End and returns it with the
// interned job type.
func linearProcess(t testing.TB) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "linear", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask(jobName, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(task).Detail).JobType
	return cp, jobType
}

// scriptProcess builds Start → ScriptTask(exprText → resultVar) → End.
func scriptProcess(t testing.TB, exprText, resultVar string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "scripted", 1)
	start := b.AddStartEvent()
	e, err := expr.CompileAuto(exprText)
	if err != nil {
		t.Fatalf("expr.CompileAuto: %v", err)
	}
	task := b.AddScriptTask(e, resultVar)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// readVar returns a scope's variable by name, or nil.
func readVar(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	var out *model.VariableValue
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Name == name {
			c := *v
			out = &c
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return out
}

// harness bundles an open wal+store and lets us reopen them to simulate a crash.
type harness struct {
	dir   string
	log   *wal.Log
	store *state.Store
}

func openHarness(t *testing.T, dir string) *harness {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	return &harness{dir: dir, log: log, store: store}
}

func (h *harness) close(t *testing.T) {
	t.Helper()
	if err := h.store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	if err := h.log.Close(); err != nil {
		t.Fatalf("log.Close: %v", err)
	}
}

func activatableJobs(t *testing.T, s *state.Store, jobType int32) []uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActivatableJobs(jobType, func(k uint64) error { keys = append(keys, k); return nil }); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	return keys
}

func counts(t *testing.T, s *state.Store) (procInstances, elementInstances int) {
	t.Helper()
	pi, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	ei, err := s.ActiveElementInstanceCount()
	if err != nil {
		t.Fatalf("ActiveElementInstanceCount: %v", err)
	}
	return pi, ei
}

// TestExecuteStartServiceTaskEnd runs the Milestone 0 vertical slice end to end
// without a restart: start an instance, it waits at the service task, a worker
// completes the job, and the instance finishes.
func TestExecuteStartServiceTaskEnd(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := linearProcess(t)

	notified := 0
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	p.SetJobNotifier(func(int32) { notified++ })
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Waiting at the service task: one instance, one element (the task), one job.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after start: process=%d element=%d, want 1 and 1", pi, ei)
	}
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable jobs = %d, want 1", len(jobs))
	}
	if notified != 1 {
		t.Errorf("job notifications = %d, want 1 (side effect after fsync)", notified)
	}

	// A worker completes the job; the instance runs to completion.
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if jobs := activatableJobs(t, h.store, jobType); len(jobs) != 0 {
		t.Errorf("leftover activatable jobs = %d, want 0", len(jobs))
	}
}

// TestScriptTaskRunsToCompletion executes Start → ScriptTask → End with no
// external worker: the script task evaluates its FEEL expression in-engine,
// writes the result variable, and the instance runs straight to completion.
func TestScriptTaskRunsToCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := scriptProcess(t, "6 * 7", "answer")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The instance finished on its own.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
	}
	// The first minted key is the process instance; it owns the result variable.
	scope := model.NewKey(1, 1)
	got := readVar(t, h.store, scope, "answer")
	if got == nil {
		t.Fatal(`variable "answer" not written`)
	}
	if got.Kind != model.VarNumber || got.Text != "42" {
		t.Fatalf("answer = {kind:%d text:%q}, want number 42", got.Kind, got.Text)
	}
}

// TestScriptTaskReadsInputVariables seeds a start variable and checks the script
// task computes from it: amount 100 with taxRate 0.19 → gross 119.
func TestScriptTaskReadsInputVariables(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := scriptProcess(t, "amount * (1 + taxRate)", "gross")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "100"},
		model.VariableValue{Name: "taxRate", Kind: model.VarNumber, Text: "0.19"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	scope := model.NewKey(1, 1)
	got := readVar(t, h.store, scope, "gross")
	if got == nil || got.Kind != model.VarNumber || got.Text != "119" {
		t.Fatalf("gross = %+v, want number 119", got)
	}
}

// TestScriptTaskRecovers is the recovery property for variables: replaying the
// log into a fresh store rebuilds exactly the state the live run produced
// (invariant I4) — including the FEEL result, which is read back from the event,
// never re-evaluated (invariant I6).
func TestScriptTaskRecovers(t *testing.T) {
	dir := t.TempDir()
	cp := scriptProcess(t, `if 1 < 2 then "yes" else "no"`, "verdict")
	clock := &manualClock{}

	// Live run to completion.
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	scope := model.NewKey(1, 1)
	live := readVar(t, h1.store, scope, "verdict")
	if live == nil || live.Kind != model.VarString || live.Text != "yes" {
		t.Fatalf("live verdict = %+v, want string \"yes\"", live)
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	// Rebuilt state matches the live run.
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after replay: process=%d element=%d, want 0 and 0", pi, ei)
	}
	replayed := readVar(t, store2, scope, "verdict")
	if replayed == nil || replayed.Kind != live.Kind || replayed.Text != live.Text {
		t.Fatalf("replayed verdict = %+v, want %+v", replayed, live)
	}
}

// TestRecoverAcrossRestart is the Milestone 0 goal: run until the instance waits
// on its job, simulate a crash (close and reopen the log and store), recover
// state by replaying the log, then complete the job and finish the instance.
func TestRecoverAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := linearProcess(t)
	clock := &manualClock{}

	// First run: start an instance and stop at the waiting service task.
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
	jobsBefore := activatableJobs(t, h1.store, jobType)
	if len(jobsBefore) != 1 {
		t.Fatalf("before crash: activatable jobs = %d, want 1", len(jobsBefore))
	}

	// Crash.
	h1.close(t)

	// Restart: reopen and recover from the log.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	// State must be rebuilt: same instance, same waiting job.
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 1 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 1", pi, ei)
	}
	jobsAfter := activatableJobs(t, h2.store, jobType)
	if len(jobsAfter) != 1 || jobsAfter[0] != jobsBefore[0] {
		t.Fatalf("after recovery: jobs = %v, want %v", jobsAfter, jobsBefore)
	}

	// Completing the recovered job must drive the instance to completion — which
	// also exercises that the key generator resumed without colliding (the End
	// event gets fresh keys).
	p2.CompleteJob(jobsAfter[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
