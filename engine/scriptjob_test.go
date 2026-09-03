package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// scriptJobProcess builds a linear process whose single task is a PowerShell
// job script, and returns the compiled process, the script-job node id, and its
// reserved job type.
func scriptJobProcess(t *testing.T, key uint64) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "greeting", 1)
	start := b.AddStartEvent()
	sj := b.AddScriptJobTask(compiler.PwshJobType, "powershell", `Write-Output "Hallo"`, "Greeting", 3)
	end := b.AddEndEvent()
	b.Connect(start, sj)
	b.Connect(sj, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, sj, cp.ScriptJobTask(cp.Node(sj).Detail).JobType
}

// TestScriptJobTaskJobLifecycle drives a polyglot (PowerShell) script task
// through the engine: on activation it creates a job carrying the reserved
// PowerShell job type and parks, and completing that job — what the script worker
// does in production — drives the token onward, exactly like a service or
// task (ADR-0047). It exercises the behavior without the worker.
func TestScriptJobTaskJobLifecycle(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, _, jobType := scriptJobProcess(t, 7)
	if jobType != compiler.PwshJobTypeIndex {
		t.Fatalf("job type = %d, want PwshJobTypeIndex %d", jobType, compiler.PwshJobTypeIndex)
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
	// The script task created a job and parked on it.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after activation: process=%d element=%d, want 1 and 1 (parked on the script job)", pi, ei)
	}

	jobKey := singleActivatableJob(t, h.store, jobType)

	// Completing the job (what the script worker does on success) finishes it.
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestScriptJobTaskRecoversAcrossRestart is the recovery property for the script
// job task: run until the instance waits on its script job, simulate a crash
// (close and reopen the log and store), recover state by replaying the log —
// state after replay must equal state built live — then complete the recovered
// job and finish the instance.
func TestScriptJobTaskRecoversAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, _, jobType := scriptJobProcess(t, 7)
	clock := &manualClock{}

	// First run: start an instance and stop at the waiting script job.
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

	// Completing the recovered job drives the instance to completion.
	p2.CompleteJob(jobsAfter[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
