package job_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// spyLoop is a single writer that records whether it is currently holding a turn,
// so a test can state where a step ran. Everything here happens on the test's own
// goroutine — Do runs its closure inline — so the flag needs no synchronisation.
type spyLoop struct {
	held  bool
	turns int
}

func (l *spyLoop) Do(fn func()) {
	l.turns++
	l.held = true
	defer func() { l.held = false }()
	fn()
}

// spyEngine reports whether the loop was held when the runner submitted a job's
// outcome, which is where the single-writer invariant has to hold.
type spyEngine struct {
	job.Engine
	loop      *spyLoop
	heldOnRun bool
	heldOnAck bool
}

func (e *spyEngine) RunUntilIdle() error {
	e.heldOnRun = e.loop.held
	return e.Engine.RunUntilIdle()
}

func (e *spyEngine) CompleteJobWithDecision(k uint64, d *model.DecisionEvaluationValue, outs ...model.VariableValue) {
	e.heldOnAck = e.loop.held
	e.Engine.CompleteJobWithDecision(k, d, outs...)
}

func spyRunner(t *testing.T) (*job.Runner, *spyLoop, *spyEngine, *engine.Processor, *state.Store, int32, uint64) {
	t.Helper()
	p, store, jobType, defKey := setup(t)
	loop := &spyLoop{}
	eng := &spyEngine{Engine: p, loop: loop}
	return job.NewRunner(store, eng, job.OnLoop(loop)), loop, eng, p, store, jobType, defKey
}

// TestHandlerRunsOffTheLoop is ADR-0150 in one assertion: the handler — the step
// that reaches outside the process and cannot be bounded — runs while the single
// writer is free, so a slow one stalls its own instance and nothing else. The
// engine work around it still runs on the writer.
func TestHandlerRunsOffTheLoop(t *testing.T) {
	runner, loop, eng, p, store, jobType, defKey := spyRunner(t)

	heldInHandler := true
	runner.Handle(jobType, func(job.Job) error {
		heldInHandler = loop.held
		return nil
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if heldInHandler {
		t.Error("the handler ran while the single writer was held; it must run off the loop")
	}
	if !eng.heldOnRun || !eng.heldOnAck {
		t.Errorf("engine work ran off the loop: RunUntilIdle held=%v, CompleteJob held=%v, want both true",
			eng.heldOnRun, eng.heldOnAck)
	}
	if loop.turns == 0 {
		t.Error("the runner never dispatched onto the loop")
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Fatalf("after Drive: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestClaimedJobIsNotDispatchedTwice covers the bookkeeping that replaces the old
// guarantee "the whole cycle is one run-loop turn": with handlers running off the
// loop, a second driver can poll while a job is in flight, and must not hand that
// job to a handler again. The handler polls re-entrantly to stand in for that
// concurrent driver, deterministically and on one goroutine.
func TestClaimedJobIsNotDispatchedTwice(t *testing.T) {
	runner, _, _, p, _, jobType, defKey := spyRunner(t)

	calls := 0
	var reentrant int
	var reentrantErr error
	runner.Handle(jobType, func(job.Job) error {
		calls++
		if calls == 1 {
			reentrant, reentrantErr = runner.PollOnce()
		}
		return nil
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if reentrantErr != nil {
		t.Fatalf("re-entrant PollOnce: %v", reentrantErr)
	}
	if reentrant != 0 {
		t.Errorf("re-entrant PollOnce dispatched %d, want 0 while the job is in flight", reentrant)
	}
	if calls != 1 {
		t.Errorf("handler calls = %d, want 1", calls)
	}
}

// TestHandlerPanicFailsTheJob covers the failure mode the split introduces: off
// the loop a panic unwinds into the caller's goroutine (an HTTP handler, where
// net/http swallows it), which would leave the claim held forever. It becomes a
// job failure instead — retried, then an incident (ADR-0061) — and the claim is
// released, so the retry can actually run.
func TestHandlerPanicFailsTheJob(t *testing.T) {
	runner, _, _, p, store, jobType, defKey := spyRunner(t)

	calls := 0
	runner.Handle(jobType, func(job.Job) error {
		calls++
		panic("interpreter exploded")
	})

	p.CreateInstance(defKey)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	// Retries=3 on the task, decremented on every failure: three attempts, then
	// the incident parks the token.
	if calls != 3 {
		t.Errorf("handler calls = %d, want 3 (retries exhausted)", calls)
	}
	incidents := 0
	var msg string
	if err := store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		incidents++
		msg = v.Message
		return nil
	}); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if incidents != 1 {
		t.Fatalf("incidents = %d, want 1", incidents)
	}
	if !strings.Contains(msg, "panicked") {
		t.Errorf("incident message = %q, want it to name the panic", msg)
	}
}
