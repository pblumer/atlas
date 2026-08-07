package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// compensationProcess builds Start → charge(service) → wait(service) → cancel(compensation
// throw, activityRef=charge) → End, where "charge" is compensable: a compensation boundary
// links it to the off-flow "refund" handler (ADR-0103). The "wait" task between charge and
// the throw gives a parking point where charge's compensable record exists but has not yet
// been consumed — the shape a recovery test needs.
func compensationProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, chargeType, waitType, refundType int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "comp", 1)
	start := b.AddStartEvent()
	charge := b.AddServiceTask("charge-card", 3)
	chargeComp := b.AddBoundaryCompensationEvent(charge)
	refund := b.AddServiceTask("refund-card", 3)
	b.SetCompensationHandler(chargeComp, refund)
	wait := b.AddServiceTask("wait", 3)
	cancel := b.AddCompensationThrowEvent()
	b.SetCompensationActivityRef(cancel, charge)
	done := b.AddEndEvent()
	b.Connect(start, charge)
	b.Connect(charge, wait)
	b.Connect(wait, cancel)
	b.Connect(cancel, done)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	chargeType = built.ServiceTask(built.Node(charge).Detail).JobType
	waitType = built.ServiceTask(built.Node(wait).Detail).JobType
	refundType = built.ServiceTask(built.Node(refund).Detail).JobType
	return built, chargeType, waitType, refundType
}

// TestCompensateOneActivity: after a compensable activity completes, a compensation throw
// naming it runs its handler — and only after the throw, not before (ADR-0103).
func TestCompensateOneActivity(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, chargeType, waitType, refundType := compensationProcess(t, 70)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Waiting at charge. No refund handler has run.
	cjobs := activatableJobs(t, h.store, chargeType)
	if len(cjobs) != 1 {
		t.Fatalf("charge jobs = %d, want 1", len(cjobs))
	}
	if n := len(activatableJobs(t, h.store, refundType)); n != 0 {
		t.Fatalf("refund jobs before compensation = %d, want 0", n)
	}

	// Complete charge → the token parks at "wait"; charge is now compensable but its
	// handler has not run (compensation is only triggered by the throw).
	p.CompleteJob(cjobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, refundType)); n != 0 {
		t.Fatalf("refund jobs after charge (before throw) = %d, want 0", n)
	}
	wjobs := activatableJobs(t, h.store, waitType)
	if len(wjobs) != 1 {
		t.Fatalf("wait jobs = %d, want 1", len(wjobs))
	}

	// Complete "wait" → the compensation throw fires → the refund handler is activated.
	p.CompleteJob(wjobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	rjobs := activatableJobs(t, h.store, refundType)
	if len(rjobs) != 1 {
		t.Fatalf("refund jobs after throw = %d, want 1 (handler activated)", len(rjobs))
	}

	// The handler is counted in the scope, so the instance is still running until it finishes.
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("instances while handler runs = %d, want 1", pi)
	}
	p.CompleteJob(rjobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after compensation: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestCompensableIndexSurvivesRecovery: the compensable index rebuilds from the log, so a
// compensation throw after a restart still finds the completed activity and runs its handler
// (ADR-0103, invariant I4/I6). The crash happens while the record exists but is unconsumed.
func TestCompensableIndexSurvivesRecovery(t *testing.T) {
	dir := t.TempDir()
	cp, chargeType, waitType, refundType := compensationProcess(t, 71)
	clock := &manualClock{}

	// First run: complete charge, park at "wait" — charge's compensable record is written
	// but the throw has not yet consumed it.
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
	cjobs := activatableJobs(t, h1.store, chargeType)
	p1.CompleteJob(cjobs[0])
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1b: %v", err)
	}
	if n := len(activatableJobs(t, h1.store, waitType)); n != 1 {
		t.Fatalf("wait jobs before crash = %d, want 1", n)
	}

	// Crash and recover: the compensable index must rebuild from the log.
	h1.close(t)
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	// Complete "wait" → the throw compensates using the rebuilt index → refund handler runs.
	wjobs := activatableJobs(t, h2.store, waitType)
	if len(wjobs) != 1 {
		t.Fatalf("wait jobs after recovery = %d, want 1", len(wjobs))
	}
	p2.CompleteJob(wjobs[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	rjobs := activatableJobs(t, h2.store, refundType)
	if len(rjobs) != 1 {
		t.Fatalf("refund jobs after recovered throw = %d, want 1 (index rebuilt)", len(rjobs))
	}
	p2.CompleteJob(rjobs[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2b: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after recovered compensation: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
