package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// subProcessScriptProcess builds Start → subProcess{ iStart → script → iEnd } →
// End. The inner script is inline FEEL, so the whole thing self-completes with no
// worker — the subprocess scope opens on entry and drains to completion.
func subProcessScriptProcess(t testing.TB) (cp *compiler.CompiledProcess, ids map[string]int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "subscript", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	iTask := b.AddScriptTask(mustCompile(t, `"checked"`), "result")
	iEnd := b.AddEndEvent()
	b.Connect(iStart, iTask)
	b.Connect(iTask, iEnd)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, map[string]int32{"start": start, "sub": sub, "iStart": iStart, "iTask": iTask, "iEnd": iEnd, "end": end}
}

// subProcessServiceProcess builds Start → subProcess{ iStart → serviceTask →
// iEnd } → End. The inner service task parks on a job, so an instance stops with
// a token inside the subprocess — the shape a recovery test needs.
func subProcessServiceProcess(t testing.TB) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "subservice", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	iTask := b.AddServiceTask(jobName, 3)
	iEnd := b.AddEndEvent()
	b.Connect(iStart, iTask)
	b.Connect(iTask, iEnd)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(iTask).Detail).JobType
}

// TestSubProcessRunsToCompletion executes an embedded subprocess end to end with
// no worker: a token enters the subprocess, its inner start → script → end runs
// in the subprocess scope, the subprocess completes when that scope drains, and
// the token flows on to the outer end — the instance finishes on its own. Every
// element (including the subprocess container) is visited exactly once.
func TestSubProcessRunsToCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, ids := subProcessScriptProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (ran to completion)", pi, ei)
	}
	visits := elementVisits(t, h.store, cp.Key)
	for name, id := range ids {
		if visits[id] != 1 {
			t.Errorf("element %q (id %d) visited %d times, want 1", name, id, visits[id])
		}
	}
}

// TestSubProcessEmptyCompletesImmediately runs a subprocess with no inner start
// event: its scope is empty on entry, so it completes at once and the token flows
// straight through to the outer end rather than parking forever.
func TestSubProcessEmptyCompletesImmediately(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "subempty", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess() // no PushScope/children: an empty subprocess
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (empty subprocess completes)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key); v[sub] != 1 {
		t.Errorf("subprocess visited %d times, want 1", v[sub])
	}
}

// TestSubProcessIOMappingScopesVariables checks subprocess-level variable passing
// (ADR-0074 Phase 4): an input mapping writes a computed value into the subprocess
// scope (the inner nodes see it, up the chain), it does NOT leak to the process
// root, and an output mapping promotes a subprocess-local value back out.
func TestSubProcessIOMappingScopesVariables(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "subio", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "5"), "outerVal") // seed a process variable
	sub := b.AddSubProcess()
	// input: innerVal = outerVal + 1  (written to the subprocess scope on entry)
	b.AddInputMapping(sub, "innerVal", mustCompile(t, "outerVal + 1"))
	// output: promoted = innerVal * 100 (promoted to the parent scope on completion)
	b.AddOutputMapping(sub, "promoted", mustCompile(t, "innerVal * 100"))
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	iEcho := b.AddScriptTask(mustCompile(t, "innerVal"), "innerEcho") // reads the subprocess-local
	iEnd := b.AddEndEvent()
	b.Connect(iStart, iEcho)
	b.Connect(iEcho, iEnd)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
	}

	root := model.NewKey(1, 1)
	// The output mapping promoted a subprocess-local value to the process scope —
	// which also proves the inner script saw the input-mapped local (promoted =
	// innerVal * 100 = 600).
	if got := readVar(t, h.store, root, "promoted"); got == nil || got.Text != "600" {
		t.Errorf("promoted = %v, want 600 (output mapping promoted out)", got)
	}
	// The input-mapped local stayed inside the subprocess — dropped on completion,
	// never leaked to the process root.
	if got := readVar(t, h.store, root, "innerVal"); got != nil {
		t.Errorf("innerVal = %v at root, want nil (subprocess-local, dropped)", got)
	}
	// The inner script's own result is written to its enclosing (subprocess) scope,
	// not the process root, so it too is dropped when the subprocess completes — only
	// an explicit output mapping escapes (ADR-0074).
	if got := readVar(t, h.store, root, "innerEcho"); got != nil {
		t.Errorf("innerEcho = %v at root, want nil (inner result is subprocess-local, not leaked)", got)
	}
}

// TestSubProcessInnerResultStaysLocal checks that an inner activity's result does
// not leak to the process root even when the subprocess itself has no I/O mappings:
// it is written to the subprocess scope and dropped when the subprocess completes,
// so only explicitly output-mapped values escape (ADR-0074).
func TestSubProcessInnerResultStaysLocal(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "sublocal", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess() // no I/O mappings on the subprocess
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	iTask := b.AddScriptTask(mustCompile(t, `"checked"`), "innerResult")
	iEnd := b.AddEndEvent()
	b.Connect(iStart, iTask)
	b.Connect(iTask, iEnd)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if got := readVar(t, h.store, model.NewKey(1, 1), "innerResult"); got != nil {
		t.Errorf("innerResult = %v at root, want nil (inner result is subprocess-local, dropped on completion)", got)
	}
}

// TestNestedSubProcessRunsToCompletion runs a subprocess nested inside another
// subprocess: entering the outer scope enters the inner scope, and draining the
// inner completes the outer, which completes the instance (ADR-0074 Phase 4).
func TestNestedSubProcessRunsToCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "nested", 1)
	start := b.AddStartEvent()
	outer := b.AddSubProcess()
	b.PushScope(outer)
	oStart := b.AddStartEvent()
	inner := b.AddSubProcess()
	b.PushScope(inner)
	iStart := b.AddStartEvent()
	iTask := b.AddScriptTask(mustCompile(t, `"deep"`), "reached")
	iEnd := b.AddEndEvent()
	b.Connect(iStart, iTask)
	b.Connect(iTask, iEnd)
	b.PopScope()
	oEnd := b.AddEndEvent()
	b.Connect(oStart, inner)
	b.Connect(inner, oEnd)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, outer)
	b.Connect(outer, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (nested subprocesses drained)", pi, ei)
	}
	for _, id := range []int32{outer, inner, iTask} {
		if v := elementVisits(t, h.store, cp.Key); v[id] != 1 {
			t.Errorf("element %d visited %d times, want 1", id, elementVisits(t, h.store, cp.Key)[id])
		}
	}
}

// TestSubProcessParksAndRecovers stops with a token parked on a service task
// inside a subprocess, crashes, and recovers: the subprocess scope instance and
// its child counter must rebuild from the log so completing the job still drains
// the scope, completes the subprocess, and finishes the instance.
func TestSubProcessParksAndRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := subProcessServiceProcess(t)
	clock := &manualClock{}

	// First run: enter the subprocess and park on the inner service task. Two
	// element instances stay active — the subprocess container (its scope is still
	// open) and the parked inner task.
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
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 2 {
		t.Fatalf("parked in subprocess: process=%d element=%d, want 1 and 2 (subprocess + task)", pi, ei)
	}
	jobsBefore := activatableJobs(t, h1.store, jobType)
	if len(jobsBefore) != 1 {
		t.Fatalf("before crash: activatable jobs = %d, want 1", len(jobsBefore))
	}

	// Crash and recover.
	h1.close(t)
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 2 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 2", pi, ei)
	}
	jobsAfter := activatableJobs(t, h2.store, jobType)
	if len(jobsAfter) != 1 || jobsAfter[0] != jobsBefore[0] {
		t.Fatalf("after recovery: jobs = %v, want %v", jobsAfter, jobsBefore)
	}

	// Completing the recovered job drains the subprocess scope, so the subprocess
	// completes and the outer end finishes the instance.
	p2.CompleteJob(jobsAfter[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
