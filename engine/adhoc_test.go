package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// adHocTwoTasks builds: start → adhoc{a, b} → end, where a and b are unconnected contained
// service tasks (both entry activities). cond, when non-nil, is the ad-hoc's completion condition.
func adHocTwoTasks(t *testing.T, key uint64, id string, d compiler.AdHocDetail) (cp *compiler.CompiledProcess, adhoc, a, b, end int32) {
	t.Helper()
	bl := compiler.NewBuilder(key, id, 1)
	start := bl.AddStartEvent()
	adhoc = bl.AddAdHocSubProcess(d)
	end = bl.AddEndEvent()
	bl.Connect(start, adhoc)
	bl.Connect(adhoc, end)
	bl.PushScope(adhoc)
	a = bl.AddServiceTask("ta", 3)
	b = bl.AddServiceTask("tb", 3)
	bl.PopScope()
	cp, err := bl.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, adhoc, a, b, end
}

// TestAdHocRunsEntryActivitiesInParallel: entering an ad-hoc subprocess activates every entry
// activity at once (ADR-0138). With no completion condition the ad-hoc completes when its scope
// drains — both jobs done — and then takes its outgoing flow like any activity.
func TestAdHocRunsEntryActivitiesInParallel(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, _, a, b, end := adHocTwoTasks(t, 200, "adhoc-parallel", compiler.AdHocDetail{CancelRemaining: true})
	jobA := cp.ServiceTask(cp.Node(a).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(b).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Both contained activities run at once: the ad-hoc container plus its two tasks.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 3 {
		t.Fatalf("after entry: process=%d element=%d, want 1 and 3 (ad-hoc + both entry activities)", pi, ei)
	}
	if jobGone(t, h.store, jobA) || jobGone(t, h.store, jobB) {
		t.Fatal("both entry activities should have a live job")
	}
	// Completing one leaves the ad-hoc running — it drains only when both are done.
	p.CompleteJob(singleActivatableJob(t, h.store, jobA))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job a): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after job a: process=%d element=%d, want 1 and 2 (ad-hoc + remaining task)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 0 {
		t.Fatalf("end visits = %d, want 0 (the ad-hoc is still running)", v)
	}
	// Completing the last one drains the scope: the ad-hoc completes and takes its flow.
	p.CompleteJob(singleActivatableJob(t, h.store, jobB))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job b): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job b: process=%d element=%d, want 0 and 0 (ad-hoc drained and completed)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the ad-hoc took its outgoing flow)", v)
	}
}

// TestAdHocCompletionConditionCancelsRemaining: when a contained activity completes and the
// ad-hoc's FEEL completion condition holds, the ad-hoc cancels the still-running activities
// (cancelRemainingInstances defaults true) and completes at once, taking its outgoing flow
// (ADR-0138). The second task's job is gone even though nobody worked it.
func TestAdHocCompletionConditionCancelsRemaining(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, _, a, b, end := adHocTwoTasks(t, 201, "adhoc-cond", compiler.AdHocDetail{
		CompletionCondition: mustCompile(t, "done"),
		CancelRemaining:     true,
	})
	jobA := cp.ServiceTask(cp.Node(a).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(b).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 3 {
		t.Fatalf("after entry: process=%d element=%d, want 1 and 3", pi, ei)
	}
	// Completing job a with done=true makes the completion condition hold at the checkpoint.
	p.CompleteJob(singleActivatableJob(t, h.store, jobA), boolVar("done", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job a): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job a: process=%d element=%d, want 0 and 0 (condition held: rest cancelled, ad-hoc completed)", pi, ei)
	}
	if !jobGone(t, h.store, jobB) {
		t.Error("the remaining activity's job survived the completion condition")
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the ad-hoc completed and took its flow)", v)
	}
}

// TestAdHocCompletionConditionFalseKeepsRunning: a contained activity completing while the
// completion condition is false leaves the ad-hoc running — the checkpoint fires only when the
// predicate actually holds (ADR-0138).
func TestAdHocCompletionConditionFalseKeepsRunning(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, _, a, b, end := adHocTwoTasks(t, 202, "adhoc-cond-false", compiler.AdHocDetail{
		CompletionCondition: mustCompile(t, "done"),
		CancelRemaining:     true,
	})
	jobA := cp.ServiceTask(cp.Node(a).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(b).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Complete a with done=false: the condition does not hold, so b keeps running.
	p.CompleteJob(singleActivatableJob(t, h.store, jobA), boolVar("done", false))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job a): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after job a: process=%d element=%d, want 1 and 2 (condition false, b still running)", pi, ei)
	}
	if jobGone(t, h.store, jobB) {
		t.Fatal("b's job was cancelled even though the completion condition was false")
	}
	// Completing b drains the scope; the ad-hoc completes normally.
	p.CompleteJob(singleActivatableJob(t, h.store, jobB), boolVar("done", false))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job b): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job b: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1", v)
	}
}

// TestAdHocCompletionConditionWithoutCancel: with cancelRemainingInstances="false" a holding
// completion condition does not cancel the still-running activities — the ad-hoc waits for them
// and completes when its scope drains (ADR-0138).
func TestAdHocCompletionConditionWithoutCancel(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, _, a, b, end := adHocTwoTasks(t, 203, "adhoc-nocancel", compiler.AdHocDetail{
		CompletionCondition: mustCompile(t, "done"),
		CancelRemaining:     false,
	})
	jobA := cp.ServiceTask(cp.Node(a).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(b).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The condition holds, but cancelRemaining is false: b is left to finish.
	p.CompleteJob(singleActivatableJob(t, h.store, jobA), boolVar("done", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job a): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after job a: process=%d element=%d, want 1 and 2 (b left running)", pi, ei)
	}
	if jobGone(t, h.store, jobB) {
		t.Fatal("b's job was cancelled despite cancelRemainingInstances=false")
	}
	p.CompleteJob(singleActivatableJob(t, h.store, jobB))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job b): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job b: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1", v)
	}
}

// TestAdHocInnerSequenceFlow: contained activities may still be connected — an entry activity's
// outgoing flow carries the token on inside the ad-hoc scope, and only when that chain drains
// does the ad-hoc complete (ADR-0138). `a` is the only entry; `b` is reached by the inner flow.
func TestAdHocInnerSequenceFlow(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	bl := compiler.NewBuilder(204, "adhoc-chain", 1)
	start := bl.AddStartEvent()
	adhoc := bl.AddAdHocSubProcess(compiler.AdHocDetail{CancelRemaining: true})
	end := bl.AddEndEvent()
	bl.Connect(start, adhoc)
	bl.Connect(adhoc, end)
	bl.PushScope(adhoc)
	a := bl.AddServiceTask("ta", 3)
	b := bl.AddServiceTask("tb", 3)
	bl.Connect(a, b) // an inner chain: only `a` is an entry activity
	bl.PopScope()
	cp, err := bl.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if entries := cp.AdHocEntries(adhoc); len(entries) != 1 || entries[0] != a {
		t.Fatalf("entries = %v, want just the chain head", entries)
	}
	jobA := cp.ServiceTask(cp.Node(a).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(b).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Only the chain head runs: the ad-hoc plus `a`.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after entry: process=%d element=%d, want 1 and 2 (ad-hoc + chain head)", pi, ei)
	}
	// Completing a takes the inner flow to b rather than draining the ad-hoc.
	p.CompleteJob(singleActivatableJob(t, h.store, jobA))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job a): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after job a: process=%d element=%d, want 1 and 2 (ad-hoc + b)", pi, ei)
	}
	if jobGone(t, h.store, jobB) {
		t.Fatal("the inner flow did not reach b")
	}
	p.CompleteJob(singleActivatableJob(t, h.store, jobB))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after job b): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after job b: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1", v)
	}
}

// TestAdHocEmptyPassesThrough: an ad-hoc subprocess with no contained activity is an empty scope —
// it completes at once and takes its outgoing flow rather than parking a token forever (ADR-0138).
func TestAdHocEmptyPassesThrough(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	bl := compiler.NewBuilder(205, "adhoc-empty", 1)
	start := bl.AddStartEvent()
	adhoc := bl.AddAdHocSubProcess(compiler.AdHocDetail{CancelRemaining: true})
	end := bl.AddEndEvent()
	bl.Connect(start, adhoc)
	bl.Connect(adhoc, end)
	cp, err := bl.Build()
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
		t.Fatalf("after create: process=%d element=%d, want 0 and 0 (empty ad-hoc passed through)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1", v)
	}
}

// TestAdHocRecovers proves a running ad-hoc subprocess survives a crash (ADR-0138, invariant I4):
// it parks with both entry activities' jobs live; after replaying the log into a fresh store the
// container and its contained activities are rebuilt, so completing the jobs drains the scope and
// the ad-hoc completes exactly as it would have without the restart.
func TestAdHocRecovers(t *testing.T) {
	dir := t.TempDir()

	cp, _, a, b, end := adHocTwoTasks(t, 206, "adhoc-rec", compiler.AdHocDetail{CancelRemaining: true})
	jobA := cp.ServiceTask(cp.Node(a).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(b).Detail).JobType

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 3 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 3", pi, ei)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the ad-hoc scope and its activities must rebuild.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 1 || ei != 3 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 3 (ad-hoc + both activities rebuilt)", pi, ei)
	}
	// The recovered ad-hoc behaves normally: both jobs complete, the scope drains, it flows on.
	p2.CompleteJob(singleActivatableJob(t, store2, jobA))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (post-recovery job a): %v", err)
	}
	p2.CompleteJob(singleActivatableJob(t, store2, jobB))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (post-recovery job b): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after recovery run: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the recovered ad-hoc completed)", v)
	}
}

// TestAdHocBoundaryEventInterrupts: an ad-hoc subprocess is an activity, so an interrupting
// boundary event on it tears the whole ad-hoc scope down — its contained activities and their
// jobs — and routes out the boundary's flow (ADR-0138, reusing ADR-0040).
func TestAdHocBoundaryEventInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	bl := compiler.NewBuilder(207, "adhoc-boundary", 1)
	start := bl.AddStartEvent()
	adhoc := bl.AddAdHocSubProcess(compiler.AdHocDetail{CancelRemaining: true})
	done := bl.AddEndEvent()
	recovered := bl.AddEndEvent()
	boundary := bl.AddBoundaryConditionalEvent(adhoc, mustCompile(t, "abort"), true)
	bl.Connect(start, adhoc)
	bl.Connect(adhoc, done)
	bl.Connect(boundary, recovered)
	bl.PushScope(adhoc)
	a := bl.AddServiceTask("ta", 3)
	b := bl.AddServiceTask("tb", 3)
	bl.PopScope()
	cp, err := bl.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobA := cp.ServiceTask(cp.Node(a).Detail).JobType
	jobB := cp.ServiceTask(cp.Node(b).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The ad-hoc, its two activities, and the armed boundary.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 4 {
		t.Fatalf("after entry: process=%d element=%d, want 1 and 4 (ad-hoc + 2 activities + boundary)", pi, ei)
	}
	// A variable fires the interrupting boundary: the whole ad-hoc scope is torn down.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("abort", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after abort): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after abort: process=%d element=%d, want 0 and 0 (ad-hoc scope torn down)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 || v[done] != 0 {
		t.Errorf("visits recovered=%d done=%d, want 1 and 0", v[recovered], v[done])
	}
	if !jobGone(t, h.store, jobA) || !jobGone(t, h.store, jobB) {
		t.Error("a contained activity's job survived the interrupting boundary")
	}
}
