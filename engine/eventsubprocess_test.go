package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// eventSubMessageProcess builds start → work(service task) → end, plus a root-level
// message-triggered event subprocess {es(message "cancel") → he(end)} — interrupting or
// not. It returns the process and the work job type.
func eventSubMessageProcess(t *testing.T, interrupting bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "esp", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)

	// The event subprocess handler, added at the root scope, then filled under its scope.
	handler := b.AddSubProcess()
	b.PushScope(handler)
	corr := mustCompile(t, `"k"`)
	es := b.AddMessageStartEvent("cancel", corr, false)
	he := b.AddEndEvent()
	b.Connect(es, he)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: es, Interrupting: interrupting, Kind: compiler.BoundaryMessage,
		MessageName: "cancel", CorrelationKey: corr,
	})

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType
}

// TestEventSubprocessMessageInterrupts arms a root message-triggered interrupting event
// subprocess while a service task waits; a correlating message terminates the main flow
// (cancelling its job), runs the handler, and the instance completes (ADR-0082 Phase 2).
func TestEventSubprocessMessageInterrupts(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := eventSubMessageProcess(t, true)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: the service task (its job) and the armed event-sub trigger are the two live
	// element instances; the trigger is uncounted but still a real element instance.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (task + armed trigger)", pi, ei)
	}
	if jobGone(t, h.store, jobType) {
		t.Fatal("work job missing while parked")
	}

	// A correlating message fires the trigger: interrupt the main flow, run the handler.
	p.PublishMessage("cancel", "k")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after publish: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after trigger: process=%d element=%d, want 0 and 0 (main flow interrupted, handler ran)", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("work job survived the interrupting event subprocess (should be canceled)")
	}
}

// TestEventSubprocessDisarmedOnCompletion arms a message event subprocess but never
// triggers it: the main flow completes normally, and the armed trigger is disarmed as the
// instance completes rather than lingering (ADR-0082).
func TestEventSubprocessDisarmedOnCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := eventSubMessageProcess(t, true)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Complete the main-flow job normally; the instance finishes and the trigger disarms.
	p.CompleteJob(activatableJobs(t, h.store, jobType)[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after complete: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after normal completion: process=%d element=%d, want 0 and 0 (trigger disarmed)", pi, ei)
	}
	// A late message must not resurrect anything — the subscription self-retired.
	p.PublishMessage("cancel", "k")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after late publish: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after late message: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestEventSubprocessRecovers arms the trigger, parks, crashes, and recovers: the armed
// subscription and the parked main flow rebuild from the log so a message after restart
// still interrupts the flow and runs the handler (ADR-0082).
func TestEventSubprocessRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := eventSubMessageProcess(t, true)
	clk := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 2 {
		t.Fatalf("before crash: process=%d element=%d, want 1 and 2", pi, ei)
	}
	h1.close(t)

	// Recover, then correlate the message.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 2 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 2 (task + trigger rebuilt)", pi, ei)
	}
	p2.PublishMessage("cancel", "k")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after recovered trigger: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h2.store, jobType) {
		t.Error("work job survived the recovered interrupt")
	}
}
