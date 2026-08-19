package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// TestCallActivityIsolatesVariables runs a caller that invokes a separate process
// via a call activity with propagation off and explicit I/O mappings, and checks
// the isolation both ways (ADR-0076): the child sees only the input-mapped variable
// (not the caller's other variables), and the caller gets back only the
// output-mapped variable (not the child's internals).
func TestCallActivityIsolatesVariables(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	// Child process: echoes its input into a result variable.
	cb := compiler.NewBuilder(8, "child-proc", 1)
	cs := cb.AddStartEvent()
	ct := cb.AddScriptTask(mustCompile(t, "childOrder"), "childResult")
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	childCp, err := cb.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}

	// Caller: seeds orderId, then calls the child with no propagation, mapping
	// orderId → childOrder in and childResult → result out.
	b := compiler.NewBuilder(9, "caller", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, `"ORD-1"`), "orderId")
	call := b.AddCallActivity("child-proc", compiler.BindingLatest, false /*propParent*/, false /*propChild*/)
	b.AddInputMapping(call, "childOrder", mustCompile(t, "orderId"))
	b.AddOutputMapping(call, "result", mustCompile(t, "childResult"))
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, call)
	b.Connect(call, end)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(childCp)
	p.Deploy(callerCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(callerCp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Both the caller and the child ran to completion.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (caller + child completed)", pi, ei)
	}

	callerRoot := model.NewKey(1, 1) // the first instance created is the caller
	// Caller keeps its own orderId and gets the output-mapped result back.
	if got := readVar(t, h.store, callerRoot, "orderId"); got == nil || got.Text != "ORD-1" {
		t.Errorf("caller orderId = %v, want ORD-1", got)
	}
	if got := readVar(t, h.store, callerRoot, "result"); got == nil || got.Text != "ORD-1" {
		t.Errorf("caller result = %v, want ORD-1 (mapped back from childResult)", got)
	}
	// The child's internal variables never leak back to the caller.
	if got := readVar(t, h.store, callerRoot, "childOrder"); got != nil {
		t.Errorf("caller childOrder = %v, want nil (a child-local input)", got)
	}
	if got := readVar(t, h.store, callerRoot, "childResult"); got != nil {
		t.Errorf("caller childResult = %v, want nil (only the mapped result escapes)", got)
	}

	// The child instance saw only the input-mapped variable, not the caller's.
	var childKey uint64
	for k, v := range completedInstances(t, h.store) {
		if v.ParentElementInstanceKey != 0 {
			childKey = k
		}
	}
	if childKey == 0 {
		t.Fatal("no completed child instance with a parent link found")
	}
	if got := readVar(t, h.store, childKey, "childOrder"); got == nil || got.Text != "ORD-1" {
		t.Errorf("child childOrder = %v, want ORD-1 (input mapping)", got)
	}
	if got := readVar(t, h.store, childKey, "orderId"); got != nil {
		t.Errorf("child orderId = %v, want nil (isolated — not propagated in)", got)
	}
}

// TestCallActivityRecovers parks with a child instance waiting on a service task,
// crashes, and recovers: the child's parent link and the caller's parked call
// activity must rebuild from the log so completing the child job still resumes the
// caller and finishes both instances (ADR-0076).
func TestCallActivityRecovers(t *testing.T) {
	dir := t.TempDir()

	cb := compiler.NewBuilder(8, "child-svc", 1)
	cs := cb.AddStartEvent()
	ct := cb.AddServiceTask("childwork", 3)
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	childCp, err := cb.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}
	jobType := childCp.ServiceTask(childCp.Node(ct).Detail).JobType

	b := compiler.NewBuilder(9, "caller-svc", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("child-svc", compiler.BindingLatest, true, true)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}
	clock := &manualClock{}

	// First run: enter the call activity, which starts the child; the child parks on
	// its service task. Two active instances (caller + child), two active elements
	// (the parked call activity and the parked service task).
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(childCp)
	p1.Deploy(callerCp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(callerCp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if pi, ei := counts(t, h1.store); pi != 2 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 2 and 2 (caller call-activity + child task)", pi, ei)
	}
	jobsBefore := activatableJobs(t, h1.store, jobType)
	if len(jobsBefore) != 1 {
		t.Fatalf("before crash: activatable jobs = %d, want 1", len(jobsBefore))
	}
	h1.close(t)

	// Recover.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(childCp)
	p2.Deploy(callerCp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 2 || ei != 2 {
		t.Fatalf("after recovery: process=%d element=%d, want 2 and 2", pi, ei)
	}
	jobsAfter := activatableJobs(t, h2.store, jobType)
	if len(jobsAfter) != 1 || jobsAfter[0] != jobsBefore[0] {
		t.Fatalf("after recovery: jobs = %v, want %v", jobsAfter, jobsBefore)
	}

	// Completing the child's job drives the child to completion, which resumes the
	// caller through the recovered parent link, finishing both instances.
	p2.CompleteJob(jobsAfter[0])
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// callerWithChildServiceTask deploys a caller whose call activity invokes a child
// that parks on a service task, and returns the processor, the caller instance key,
// and the child's job type. Used by the termination tests.
func callerWithChildServiceTask(t *testing.T, h *harness, clk engine.Clock, callBuild func(b *compiler.Builder, call int32)) (*engine.Processor, int32) {
	t.Helper()
	cb := compiler.NewBuilder(8, "child-term", 1)
	cs := cb.AddStartEvent()
	ct := cb.AddServiceTask("childwork", 3)
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	childCp, err := cb.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}
	jobType := childCp.ServiceTask(childCp.Node(ct).Detail).JobType

	b := compiler.NewBuilder(9, "caller-term", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("child-term", compiler.BindingLatest, true, true)
	b.Connect(start, call)
	callBuild(b, call)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(childCp)
	p.Deploy(callerCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(callerCp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Two live instances (caller + child); the caller's call activity and the child's
	// service task are both parked. A boundary-armed caller also has a boundary element
	// instance, so assert only the instance count here.
	if pi, _ := counts(t, h.store); pi != 2 {
		t.Fatalf("parked: process=%d, want 2 (caller + child)", pi)
	}
	return p, jobType
}

// TestCallActivityCancelTerminatesChild cancels a caller whose call activity is
// still running: the child instance must be terminated too, not left running
// (ADR-0076).
func TestCallActivityCancelTerminatesChild(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, _ := callerWithChildServiceTask(t, h, &manualClock{}, func(b *compiler.Builder, call int32) {
		b.Connect(call, b.AddEndEvent())
	})

	p.CancelInstance(model.NewKey(1, 1)) // cancel the caller
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after cancel: process=%d element=%d, want 0 and 0 (caller and child terminated)", pi, ei)
	}
}

// TestCallActivityBoundaryTerminatesChild fires an interrupting boundary timer on a
// running call activity: the child instance is terminated and the escalation flow
// runs to completion (ADR-0076).
func TestCallActivityBoundaryTerminatesChild(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	clk := &fixedClock{t: 1_000}
	var escEnd int32
	p, _ := callerWithChildServiceTask(t, h, clk, func(b *compiler.Builder, call int32) {
		b.Connect(call, b.AddEndEvent()) // normal path
		boundary := b.AddBoundaryTimerEvent(call, true, dur)
		escEnd = b.AddEndEvent()
		b.Connect(boundary, escEnd)
	})

	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after interrupt: process=%d element=%d, want 0 and 0 (child terminated, caller escalated)", pi, ei)
	}
	_ = escEnd
}

// TestCallActivityCancelTerminatesGrandchild cancels the top of a three-level call
// chain (A calls B calls C, with C parked on a service task): terminating A must
// cascade through B down to C, so no instance is left running (ADR-0076). This
// exercises the recursion — terminating a call-activity element enqueues its child's
// termination, which in turn terminates that child's call activity.
func TestCallActivityCancelTerminatesGrandchild(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	// Leaf C: parks on a service task.
	cb := compiler.NewBuilder(7, "leaf-c", 1)
	cs := cb.AddStartEvent()
	ct := cb.AddServiceTask("leafwork", 3)
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	leafCp, err := cb.Build()
	if err != nil {
		t.Fatalf("leaf Build: %v", err)
	}

	// Middle B: calls C.
	bb := compiler.NewBuilder(8, "mid-b", 1)
	bs := bb.AddStartEvent()
	bcall := bb.AddCallActivity("leaf-c", compiler.BindingLatest, true, true)
	be := bb.AddEndEvent()
	bb.Connect(bs, bcall)
	bb.Connect(bcall, be)
	midCp, err := bb.Build()
	if err != nil {
		t.Fatalf("mid Build: %v", err)
	}

	// Top A: calls B.
	ab := compiler.NewBuilder(9, "top-a", 1)
	as := ab.AddStartEvent()
	acall := ab.AddCallActivity("mid-b", compiler.BindingLatest, true, true)
	ae := ab.AddEndEvent()
	ab.Connect(as, acall)
	ab.Connect(acall, ae)
	topCp, err := ab.Build()
	if err != nil {
		t.Fatalf("top Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(leafCp)
	p.Deploy(midCp)
	p.Deploy(topCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(topCp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, _ := counts(t, h.store); pi != 3 {
		t.Fatalf("parked: process=%d, want 3 (A + B + C)", pi)
	}

	p.CancelInstance(model.NewKey(1, 1)) // cancel the top instance
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after cancel: process=%d element=%d, want 0 and 0 (whole chain terminated)", pi, ei)
	}
}

// TestCallActivityPropagatesAll runs a call activity with propagation on (the
// default): all caller variables flow into the child and all child variables flow
// back, no mappings needed (ADR-0076).
func TestCallActivityPropagatesAll(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cb := compiler.NewBuilder(8, "child-all", 1)
	cs := cb.AddStartEvent()
	ct := cb.AddScriptTask(mustCompile(t, "seed"), "childSaw") // reads a propagated caller var
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	childCp, err := cb.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}

	b := compiler.NewBuilder(9, "caller-all", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "42"), "seed")
	call := b.AddCallActivity("child-all", compiler.BindingLatest, true /*propParent*/, true /*propChild*/)
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, call)
	b.Connect(call, end)
	callerCp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(childCp)
	p.Deploy(callerCp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(callerCp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0", pi, ei)
	}
	callerRoot := model.NewKey(1, 1)
	// The child saw the propagated seed, computed childSaw, and it flowed back.
	if got := readVar(t, h.store, callerRoot, "childSaw"); got == nil || got.Text != "42" {
		t.Errorf("caller childSaw = %v, want 42 (propagated in, computed, propagated back)", got)
	}
}
