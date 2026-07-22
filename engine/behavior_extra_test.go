package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// businessRuleProcess builds Start → BusinessRuleTask → End and returns it with
// the interned (reserved DMN) job type.
func businessRuleProcess(t testing.TB) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "decide", 1)
	start := b.AddStartEvent()
	task, err := b.AddBusinessRuleTask("Dish", map[string]any{"Season": "Winter"}, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.BusinessRuleTask(cp.Node(task).Detail).JobType
	return cp, jobType
}

// TestBusinessRuleTaskLifecycle drives a business rule task through its job: on
// activation it creates a job carrying the reserved DMN job type (OnActivated);
// when the in-process DMN worker completes that job, the task completes and the
// instance finishes (OnCompleting).
func TestBusinessRuleTaskLifecycle(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := businessRuleProcess(t)

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

	// Waiting at the business rule task with one DMN job outstanding.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after start: process=%d element=%d, want 1 and 1", pi, ei)
	}
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("activatable DMN jobs = %d, want 1", len(jobs))
	}
	if notified != 1 {
		t.Errorf("job notifications = %d, want 1", notified)
	}

	// The DMN worker completes the job; the instance runs to completion.
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestBusinessRuleTaskRecovers is the recovery property for a business rule task:
// replaying the log into a fresh store rebuilds exactly the waiting-job state the
// live run produced (invariant I4).
func TestBusinessRuleTaskRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := businessRuleProcess(t)
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
	jobsBefore := activatableJobs(t, h1.store, jobType)
	if len(jobsBefore) != 1 {
		t.Fatalf("before crash: jobs = %d, want 1", len(jobsBefore))
	}
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 1 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 1", pi, ei)
	}
	jobsAfter := activatableJobs(t, h2.store, jobType)
	if len(jobsAfter) != 1 || jobsAfter[0] != jobsBefore[0] {
		t.Fatalf("after recovery: jobs = %v, want %v", jobsAfter, jobsBefore)
	}
}

// TestScriptTaskMissingInputVariable exercises the branch where an input the
// expression reads is not present in scope: the binding is skipped and FEEL
// evaluates with the variable unbound (null-propagating), still completing.
func TestScriptTaskMissingInputVariable(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	// "missing" is never seeded, so GetVariable returns nil for it.
	cp := scriptProcess(t, "missing + 1", "out")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The instance still finished; the result variable exists (null-propagated).
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, h.store, model.NewKey(1, 1), "out"); got == nil {
		t.Fatal(`variable "out" not written`)
	}
}

// TestScriptTaskBooleanResult covers toVarKind's bool branch end-to-end: a FEEL
// predicate result is stored as a boolean variable.
func TestScriptTaskBooleanResult(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := scriptProcess(t, "1 < 2", "flag")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	got := readVar(t, h.store, model.NewKey(1, 1), "flag")
	if got == nil || got.Kind != model.VarBool || !got.Bool {
		t.Fatalf("flag = %+v, want bool true", got)
	}
}

// TestScriptTaskReadsBoolAndStringInputs covers toExprKind's bool and string
// branches: a script reads a seeded boolean and string variable back into the
// evaluation.
func TestScriptTaskReadsBoolAndStringInputs(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := scriptProcess(t, `if ok then greeting else "no"`, "out")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "ok", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "greeting", Kind: model.VarString, Text: "hi"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	got := readVar(t, h.store, model.NewKey(1, 1), "out")
	if got == nil || got.Kind != model.VarString || got.Text != "hi" {
		t.Fatalf("out = %+v, want string \"hi\"", got)
	}
}

// TestProcessBatchRecordTooLarge exercises the durability-phase error path: an
// event whose encoded record exceeds the WAL's per-record cap makes Append fail,
// which aborts the batch and surfaces from RunUntilIdle (nothing became visible).
func TestProcessBatchRecordTooLarge(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// A start variable larger than the WAL's 64 MiB per-record limit: its
	// VariableCreated event cannot be appended.
	huge := strings.Repeat("a", (64<<20)+64)
	p.CreateInstance(cp.Key, model.VariableValue{Name: "big", Kind: model.VarString, Text: huge})
	if err := p.RunUntilIdle(); err == nil {
		t.Fatal("RunUntilIdle over an oversized record = nil error, want error")
	}
}

// TestProcessBatchSyncFailure exercises the Sync error path: with a tiny segment
// cap every batch after the first rolls to a new segment, so removing the WAL
// directory makes the next roll (inside Sync) fail, aborting the batch.
func TestProcessBatchSyncFailure(t *testing.T) {
	dir := t.TempDir()
	walDir := filepath.Join(dir, "wal")
	log, err := wal.Open(wal.Options{Dir: walDir, MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer func() { _ = log.Close() }()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	cp, _ := linearProcess(t)
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// First instance runs fine (segments roll successfully while the dir exists).
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}

	// Remove the WAL directory so the next segment roll cannot create a file.
	if err := os.RemoveAll(walDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err == nil {
		t.Fatal("RunUntilIdle with a failing segment roll = nil error, want error")
	}
}
