package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// transactionProcess builds Start → book(transaction) → done(end), where the transaction
// body is ts(start) → reserve(script, compensable) → cancel(cancel end). The "reserve"
// activity's compensation handler "unreserve" and the transaction's cancel-boundary target
// "recover" are service tasks, so a test can step them and observe that compensation runs
// before the cancel boundary fires (ADR-0108). Returns the job types and the node ids the
// visit assertions need.
func transactionProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, unreserveType, recoverType int32, done, recover, handled int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "booking", 1)
	start := b.AddStartEvent()
	book := b.AddSubProcess()
	b.SetTransaction(book)
	b.PushScope(book)
	ts := b.AddStartEvent()
	reserve := b.AddScriptTask(mustCompile(t, `"reserved"`), "r")
	reserveComp := b.AddBoundaryCompensationEvent(reserve)
	unreserve := b.AddServiceTask("unreserve", 3)
	b.SetCompensationHandler(reserveComp, unreserve)
	cancel := b.AddCancelEndEvent()
	b.Connect(ts, reserve)
	b.Connect(reserve, cancel)
	b.PopScope()
	bookCancelled := b.AddBoundaryCancelEvent(book)
	recover = b.AddServiceTask("recover", 3)
	handled = b.AddEndEvent()
	done = b.AddEndEvent()
	b.Connect(start, book)
	b.Connect(book, done)
	b.Connect(bookCancelled, recover)
	b.Connect(recover, handled)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	unreserveType = built.ServiceTask(built.Node(unreserve).Detail).JobType
	recoverType = built.ServiceTask(built.Node(recover).Detail).JobType
	return built, unreserveType, recoverType, done, recover, handled
}

// TestTransactionCancelCompensatesThenRoutesBoundary: a cancel end event compensates the
// transaction's completed activities, and only after compensation finishes does the cancel
// boundary fire and route the recovery flow — the transaction's normal outgoing flow is never
// taken (ADR-0108).
func TestTransactionCancelCompensatesThenRoutesBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, unreserveType, recoverType, done, recoverNode, _ := transactionProcess(t, 80)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The cancel end fired: reserve's compensation handler is running, but the cancel
	// boundary has NOT yet fired (compensation must complete first).
	ujobs := activatableJobs(t, h.store, unreserveType)
	if len(ujobs) != 1 {
		t.Fatalf("compensation-handler jobs = %d, want 1", len(ujobs))
	}
	if n := len(activatableJobs(t, h.store, recoverType)); n != 0 {
		t.Fatalf("recover jobs before compensation finished = %d, want 0 (boundary must fire after compensation)", n)
	}

	// Complete the compensation handler → the transaction scope drains → the cancel boundary
	// fires and activates the recovery flow.
	p.CompleteJob(ujobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	rjobs := activatableJobs(t, h.store, recoverType)
	if len(rjobs) != 1 {
		t.Fatalf("recover jobs after compensation = %d, want 1 (cancel boundary fired)", len(rjobs))
	}

	p.CompleteJob(rjobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after cancellation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[done] != 0 {
		t.Errorf("transaction's normal end visits = %d, want 0 (a cancelled transaction never commits)", v[done])
	}
	if v[recoverNode] != 1 {
		t.Errorf("recover visits = %d, want 1 (cancel boundary routed the recovery flow)", v[recoverNode])
	}
}

// TestTransactionCommitTakesNormalFlow: a transaction that reaches a normal end commits — it
// takes its normal outgoing flow, does not compensate, and its cancel boundary is disarmed
// (ADR-0108).
func TestTransactionCommitTakesNormalFlow(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(81, "commit", 1)
	start := b.AddStartEvent()
	book := b.AddSubProcess()
	b.SetTransaction(book)
	b.PushScope(book)
	ts := b.AddStartEvent()
	reserve := b.AddScriptTask(mustCompile(t, `"reserved"`), "r")
	reserveComp := b.AddBoundaryCompensationEvent(reserve)
	unreserve := b.AddScriptTask(mustCompile(t, `"undone"`), "u")
	b.SetCompensationHandler(reserveComp, unreserve)
	normalEnd := b.AddEndEvent()
	b.Connect(ts, reserve)
	b.Connect(reserve, normalEnd)
	b.PopScope()
	bookCancelled := b.AddBoundaryCancelEvent(book)
	recoverType := "recover"
	recover := b.AddServiceTask(recoverType, 3)
	handled := b.AddEndEvent()
	done := b.AddEndEvent()
	b.Connect(start, book)
	b.Connect(book, done)
	b.Connect(bookCancelled, recover)
	b.Connect(recover, handled)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	recoverJobType := cp.ServiceTask(cp.Node(recover).Detail).JobType

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
		t.Fatalf("after commit: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[done] != 1 {
		t.Errorf("normal end visits = %d, want 1 (a committed transaction takes its normal flow)", v[done])
	}
	if v[unreserve] != 0 {
		t.Errorf("compensation handler visits = %d, want 0 (a committed transaction does not compensate)", v[unreserve])
	}
	if n := len(activatableJobs(t, h.store, recoverJobType)); n != 0 {
		t.Errorf("recover jobs = %d, want 0 (the cancel boundary is disarmed on commit)", n)
	}
}

// TestTransactionCancelTerminatesRunningSiblings: when a cancel end event fires, the
// transaction's other still-running activities are terminated, so the transaction drains to
// its compensation and cancel-boundary flow rather than hanging on the parked sibling (ADR-0108).
func TestTransactionCancelTerminatesRunningSiblings(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(82, "cancelsib", 1)
	start := b.AddStartEvent()
	book := b.AddSubProcess()
	b.SetTransaction(book)
	b.PushScope(book)
	ts := b.AddStartEvent()
	fork := b.AddParallelGateway()
	reserve := b.AddScriptTask(mustCompile(t, `"reserved"`), "r")
	reserveComp := b.AddBoundaryCompensationEvent(reserve)
	unreserve := b.AddScriptTask(mustCompile(t, `"undone"`), "u")
	b.SetCompensationHandler(reserveComp, unreserve)
	cancel := b.AddCancelEndEvent()
	wait := b.AddServiceTask("wait", 3) // the sibling branch, parked on a job
	branchEnd := b.AddEndEvent()
	b.Connect(ts, fork)
	b.Connect(fork, reserve)
	b.Connect(reserve, cancel)
	b.Connect(fork, wait)
	b.Connect(wait, branchEnd)
	b.PopScope()
	bookCancelled := b.AddBoundaryCancelEvent(book)
	recover := b.AddScriptTask(mustCompile(t, `"recovered"`), "rec")
	handled := b.AddEndEvent()
	done := b.AddEndEvent()
	b.Connect(start, book)
	b.Connect(book, done)
	b.Connect(bookCancelled, recover)
	b.Connect(recover, handled)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	waitType := cp.ServiceTask(cp.Node(wait).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The whole thing ran to completion: the parked sibling was terminated (its job is gone),
	// the compensation handler ran, and the cancel boundary routed to recover.
	if n := len(activatableJobs(t, h.store, waitType)); n != 0 {
		t.Errorf("sibling jobs after cancellation = %d, want 0 (the running sibling is terminated)", n)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after cancellation: process=%d element=%d, want 0 and 0 (no hang on the sibling)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[unreserve] != 1 {
		t.Errorf("compensation handler visits = %d, want 1", v[unreserve])
	}
	if v[recover] != 1 {
		t.Errorf("recover visits = %d, want 1", v[recover])
	}
	if v[branchEnd] != 0 {
		t.Errorf("sibling branch end visits = %d, want 0 (the sibling was terminated, not completed)", v[branchEnd])
	}
}

// TestTransactionCancelBroadcastCompensatesAll: a cancel end compensates every completed
// compensable activity in the transaction, then routes out the cancel boundary (ADR-0108).
func TestTransactionCancelBroadcastCompensatesAll(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(83, "cancelall", 1)
	start := b.AddStartEvent()
	book := b.AddSubProcess()
	b.SetTransaction(book)
	b.PushScope(book)
	ts := b.AddStartEvent()
	a := b.AddScriptTask(mustCompile(t, `"a"`), "aOut")
	aComp := b.AddBoundaryCompensationEvent(a)
	undoA := b.AddScriptTask(mustCompile(t, `"undoA"`), "ua")
	b.SetCompensationHandler(aComp, undoA)
	bb := b.AddScriptTask(mustCompile(t, `"b"`), "bOut")
	bComp := b.AddBoundaryCompensationEvent(bb)
	undoB := b.AddScriptTask(mustCompile(t, `"undoB"`), "ub")
	b.SetCompensationHandler(bComp, undoB)
	cancel := b.AddCancelEndEvent()
	b.Connect(ts, a)
	b.Connect(a, bb)
	b.Connect(bb, cancel)
	b.PopScope()
	bookCancelled := b.AddBoundaryCancelEvent(book)
	recover := b.AddScriptTask(mustCompile(t, `"recovered"`), "rec")
	handled := b.AddEndEvent()
	done := b.AddEndEvent()
	b.Connect(start, book)
	b.Connect(book, done)
	b.Connect(bookCancelled, recover)
	b.Connect(recover, handled)
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
		t.Fatalf("after cancellation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[undoA] != 1 || v[undoB] != 1 {
		t.Errorf("handler visits undoA=%d undoB=%d, want 1 and 1 (both completed activities compensated)", v[undoA], v[undoB])
	}
	if v[recover] != 1 {
		t.Errorf("recover visits = %d, want 1 (cancel boundary routed after compensation)", v[recover])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0", v[done])
	}
}

// TestTransactionCancelNoBoundaryTearsDown: a transaction cancelled with no cancel boundary
// compensates its completed work, then simply tears the transaction down (no recovery route)
// and the enclosing scope drains — the instance completes rather than hanging (ADR-0108).
func TestTransactionCancelNoBoundaryTearsDown(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(85, "cancelnobound", 1)
	start := b.AddStartEvent()
	book := b.AddSubProcess()
	b.SetTransaction(book)
	b.PushScope(book)
	ts := b.AddStartEvent()
	reserve := b.AddScriptTask(mustCompile(t, `"reserved"`), "r")
	reserveComp := b.AddBoundaryCompensationEvent(reserve)
	unreserve := b.AddScriptTask(mustCompile(t, `"undone"`), "u")
	b.SetCompensationHandler(reserveComp, unreserve)
	cancel := b.AddCancelEndEvent()
	b.Connect(ts, reserve)
	b.Connect(reserve, cancel)
	b.PopScope()
	// No cancel boundary on the transaction, and no outgoing flow taken on cancel.
	done := b.AddEndEvent()
	b.Connect(start, book)
	b.Connect(book, done)
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
		t.Fatalf("after boundary-less cancellation: process=%d element=%d, want 0 and 0 (torn down, no hang)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[unreserve] != 1 {
		t.Errorf("compensation handler visits = %d, want 1 (cancel still compensates)", v[unreserve])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0 (a cancelled transaction never commits)", v[done])
	}
}

// TestTransactionCancelRecovers: the cancellation spans batches (compensate → worker completes
// → boundary fires). A crash while the compensation handler's job waits recovers from the log
// — the canceling marker and the compensable index rebuild — and completing the handler after
// restart still fires the cancel boundary (ADR-0108, invariant I4/I6).
func TestTransactionCancelRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, unreserveType, recoverType, _, _, _ := transactionProcess(t, 84)
	clock := &manualClock{}

	// First run: reach the point where the cancel end fired and the compensation handler's
	// job is waiting — the transaction is mid-cancellation.
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
	ujobs := activatableJobs(t, h1.store, unreserveType)
	if len(ujobs) != 1 {
		t.Fatalf("compensation-handler jobs before crash = %d, want 1", len(ujobs))
	}

	// Crash and recover: the canceling marker and compensable index must rebuild from the log.
	h1.close(t)
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	// Complete the compensation handler after restart → the transaction scope drains → the
	// rebuilt canceling marker routes the completion out the cancel boundary.
	ujobs = activatableJobs(t, h2.store, unreserveType)
	if len(ujobs) != 1 {
		t.Fatalf("compensation-handler jobs after recovery = %d, want 1", len(ujobs))
	}
	p2.CompleteJob(ujobs[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	rjobs := activatableJobs(t, h2.store, recoverType)
	if len(rjobs) != 1 {
		t.Fatalf("recover jobs after recovered cancellation = %d, want 1 (marker rebuilt, boundary fired)", len(rjobs))
	}
	p2.CompleteJob(rjobs[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2b: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after recovered cancellation: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
