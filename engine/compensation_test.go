package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
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

// compensationBroadcastProcess builds Start → seed(trace=0) → a → b → cancel(broadcast
// compensation throw, no activityRef) → park(service) → End, where a and b are compensable
// inline script tasks whose handlers fold their marker into "trace": undoA does trace*10+1,
// undoB does trace*10+2. a completes before b, so reverse completion order is [b, a] and a
// correct broadcast leaves trace = 0→2→21. The trailing "park" task holds the instance open
// so the result is observable (ADR-0103).
func compensationBroadcastProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, parkType int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "compbc", 1)
	start := b.AddStartEvent()
	seed := b.AddScriptTask(mustCompile(t, "0"), "trace")
	a := b.AddScriptTask(mustCompile(t, `"a-done"`), "aOut")
	aComp := b.AddBoundaryCompensationEvent(a)
	undoA := b.AddScriptTask(mustCompile(t, "trace * 10 + 1"), "trace")
	b.SetCompensationHandler(aComp, undoA)
	bb := b.AddScriptTask(mustCompile(t, `"b-done"`), "bOut")
	bComp := b.AddBoundaryCompensationEvent(bb)
	undoB := b.AddScriptTask(mustCompile(t, "trace * 10 + 2"), "trace")
	b.SetCompensationHandler(bComp, undoB)
	cancel := b.AddCompensationThrowEvent() // no activityRef → compensate the whole scope
	park := b.AddServiceTask("park", 3)
	end := b.AddEndEvent()
	b.Connect(start, seed)
	b.Connect(seed, a)
	b.Connect(a, bb)
	b.Connect(bb, cancel)
	b.Connect(cancel, park)
	b.Connect(park, end)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, built.ServiceTask(built.Node(park).Detail).JobType
}

// TestCompensateBroadcastReverseOrder: a compensation throw with no activityRef compensates
// every completed compensable activity in its scope, in reverse completion order (ADR-0103).
func TestCompensateBroadcastReverseOrder(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, parkType := compensationBroadcastProcess(t, 72)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Parked at "park" after the broadcast throw fired: both handlers ran, reverse order.
	if n := len(activatableJobs(t, h.store, parkType)); n != 1 {
		t.Fatalf("park jobs = %d, want 1 (throw took its flow after compensating)", n)
	}
	scope := model.NewKey(1, 1) // the first (only) process instance's root scope
	trace := readVar(t, h.store, scope, "trace")
	if trace == nil {
		t.Fatal("trace variable missing — no handler ran")
	}
	if trace.Text != "21" {
		t.Errorf("trace = %q, want %q (reverse completion order b→a: 0→2→21)", trace.Text, "21")
	}

	// Draining the parked task completes the instance — nothing left hanging.
	p.CompleteJob(activatableJobs(t, h.store, parkType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after park drained: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// compensationEndProcess builds Start → a(compensable) → rollback(compensation END event),
// where a's handler "undoA" records a visit. The end event compensates, then ends the scope
// (ADR-0103) — the trigger-and-stop counterpart of a compensation throw.
func compensationEndProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, undoA, rollback int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "compend", 1)
	start := b.AddStartEvent()
	a := b.AddScriptTask(mustCompile(t, `"a-done"`), "aOut")
	aComp := b.AddBoundaryCompensationEvent(a)
	undoA = b.AddScriptTask(mustCompile(t, `"undone"`), "undo")
	b.SetCompensationHandler(aComp, undoA)
	rollback = b.AddCompensationEndEvent()
	b.Connect(start, a)
	b.Connect(a, rollback)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, undoA, rollback
}

// TestCompensationEndEvent: an end event bearing a <compensateEventDefinition> compensates
// the scope, then ends it — the handler runs and the instance completes (ADR-0103).
func TestCompensationEndEvent(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, undoA, rollback := compensationEndProcess(t, 73)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The compensation end compensated a, then ended the scope: the instance completed.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after compensation end: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[undoA] != 1 {
		t.Errorf("handler visits = %d, want 1 (compensation end ran the handler)", v[undoA])
	}
	if v[rollback] != 1 {
		t.Errorf("compensation end visits = %d, want 1", v[rollback])
	}
}

// TestCompensationScopedToSubprocess: a compensation throw inside a subprocess compensates
// only its own scope — the subprocess's compensable activity runs its handler, while a
// compensable activity in the enclosing root scope is left untouched (ADR-0103).
func TestCompensationScopedToSubprocess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(74, "compsub", 1)
	start := b.AddStartEvent()
	r := b.AddScriptTask(mustCompile(t, `"r"`), "rOut") // compensable, in the ROOT scope
	rComp := b.AddBoundaryCompensationEvent(r)
	undoR := b.AddScriptTask(mustCompile(t, `"undoR"`), "undoR")
	b.SetCompensationHandler(rComp, undoR)
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	a := b.AddScriptTask(mustCompile(t, `"a"`), "aOut") // compensable, in the SUB scope
	aComp := b.AddBoundaryCompensationEvent(a)
	undoA := b.AddScriptTask(mustCompile(t, `"undoA"`), "undoA")
	b.SetCompensationHandler(aComp, undoA)
	cancel := b.AddCompensationThrowEvent() // no activityRef → compensate the sub scope only
	iEnd := b.AddEndEvent()
	b.Connect(iStart, a)
	b.Connect(a, cancel)
	b.Connect(cancel, iEnd)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, r)
	b.Connect(r, sub)
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
	v := elementVisits(t, h.store, cp.Key)
	if v[undoA] != 1 {
		t.Errorf("sub handler visits = %d, want 1 (compensated within its scope)", v[undoA])
	}
	if v[undoR] != 0 {
		t.Errorf("root handler visits = %d, want 0 (a sub-scoped throw must not compensate the root)", v[undoR])
	}
}

// TestCompensableRecordsCleanedOnCompletion: an uncompensated compensable activity leaves no
// record behind once its instance completes — the index is cleaned on scope teardown, so it
// does not leak (ADR-0103).
func TestCompensableRecordsCleanedOnCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(75, "compclean", 1)
	start := b.AddStartEvent()
	a := b.AddScriptTask(mustCompile(t, `"a"`), "aOut")
	aComp := b.AddBoundaryCompensationEvent(a)
	undoA := b.AddScriptTask(mustCompile(t, `"undoA"`), "undoA")
	b.SetCompensationHandler(aComp, undoA)
	end := b.AddEndEvent()
	b.Connect(start, a)
	b.Connect(a, end) // no compensation throw: the record is written, never consumed
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

	if pi, _ := counts(t, h.store); pi != 0 {
		t.Fatalf("instances = %d, want 0 (instance completed)", pi)
	}
	n := 0
	if err := h.store.CompensablesOfScope(model.NewKey(1, 1), func(*model.CompensableValue) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("CompensablesOfScope: %v", err)
	}
	if n != 0 {
		t.Errorf("leftover compensable records = %d, want 0 (cleaned on instance completion)", n)
	}
	if v := elementVisits(t, h.store, cp.Key); v[undoA] != 0 {
		t.Errorf("handler visits = %d, want 0 (nothing triggered compensation)", v[undoA])
	}
}

// TestCompensateMultipleRuns: a compensable activity that runs twice (a loop) records twice,
// and a later broadcast compensates each completed instance — the handler runs twice (ADR-0103).
func TestCompensateMultipleRuns(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(76, "comploop", 1)
	start := b.AddStartEvent()
	initDone := b.AddScriptTask(mustCompile(t, `false`), "done")
	a := b.AddScriptTask(mustCompile(t, `"a"`), "aOut")
	aComp := b.AddBoundaryCompensationEvent(a)
	undoA := b.AddScriptTask(mustCompile(t, `"undoA"`), "undoA")
	b.SetCompensationHandler(aComp, undoA)
	gw := b.AddExclusiveGateway()
	setDone := b.AddScriptTask(mustCompile(t, `true`), "done")
	cancel := b.AddCompensationThrowEvent() // no activityRef → compensate every run of a
	end := b.AddEndEvent()
	b.Connect(start, initDone)
	b.Connect(initDone, a)
	b.Connect(a, gw)
	loop := b.Connect(gw, setDone) // taken on the first pass (done = false)
	b.SetFlowCondition(loop, mustCompile(t, `done = false`))
	b.Connect(setDone, a) // second pass through a
	exit := b.Connect(gw, cancel)
	b.SetFlowDefault(exit)
	b.Connect(cancel, end)
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
	if v := elementVisits(t, h.store, cp.Key); v[undoA] != 2 {
		t.Errorf("handler visits = %d, want 2 (a ran twice, both runs compensated)", v[undoA])
	}
}
