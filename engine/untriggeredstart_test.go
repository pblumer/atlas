package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// A product's lifecycle process: one message start per operation, each branch
// writing that it ran. It has no none start, so it is exactly the shape ADR-0426
// is about — something only its triggers can start. withNone adds a none start,
// which is what a modeller does to make it callable by hand again.
func lifecycleProcess(t *testing.T, key uint64, version int32, withNone bool) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "lifecycle", version)
	for _, op := range []string{"provision", "change", "deprovision"} {
		s := b.AddMessageStartEvent("laptop."+op, nil, false)
		st := b.AddScriptTask(mustCompile(t, "true"), "ran_"+op)
		b.Connect(s, st)
		b.Connect(st, b.AddEndEvent())
	}
	if withNone {
		s := b.AddStartEvent()
		st := b.AddScriptTask(mustCompile(t, "true"), "ran_by_hand")
		b.Connect(s, st)
		b.Connect(st, b.AddEndEvent())
	}
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

func callerOf(t *testing.T, key uint64) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "caller", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("lifecycle", compiler.BindingLatest, false, true)
	b.Connect(start, call)
	b.Connect(call, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, call
}

// TestCallActivityRefusesATargetOnlyItsTriggersCanStart: a call activity is a create
// nobody triggered. Into a process with three message starts and no none start it
// used to seed all three — provisioning, changing and deprovisioning in one
// instant. It now creates no child and parks on one incident naming the target's
// start events; resolving it without a fix parks it again rather than clearing.
func TestCallActivityRefusesATargetOnlyItsTriggersCanStart(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	caller, call := callerOf(t, 9)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(lifecycleProcess(t, 7, 1, false))
	p.Deploy(caller)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(9)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if n := activeProcs(t, h.store); n != 1 {
		t.Fatalf("active instances = %d, want 1 — the caller, parked; no child may have been created", n)
	}
	elKey, inc := oneIncident(t, h)
	if inc.ElementId != call {
		t.Errorf("incident on element %d, want the call activity (%d)", inc.ElementId, call)
	}
	for _, want := range []string{"lifecycle", "ADR-0426"} {
		if !strings.Contains(inc.Message, want) {
			t.Errorf("incident message %q does not name %q", inc.Message, want)
		}
	}

	// Resolving without fixing the target is a genuine retry: it parks again.
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := activeProcs(t, h.store); n != 1 {
		t.Fatalf("after an unfixed resolve: active = %d, want 1", n)
	}
	oneIncident(t, h)
}

// TestResolvingAfterTheTargetGainsANoneStartCallsIt: the fix a modeller makes is
// to give the target a none start. Once that version is deployed, resolving the
// incident re-runs the activation against it: the child is created at its none
// start only — none of the triggered branches — and the caller completes.
func TestResolvingAfterTheTargetGainsANoneStartCallsIt(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	caller, _ := callerOf(t, 9)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(lifecycleProcess(t, 7, 1, false))
	p.Deploy(caller)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(9)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	elKey, _ := oneIncident(t, h)

	p.Deploy(lifecycleProcess(t, 8, 2, true))
	p.ResolveIncident(elKey, 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Fatalf("active instances = %d, want 0 — caller and child both complete", n)
	}
	if all := incidents(t, h.store); len(all) != 0 {
		t.Fatalf("incidents after resolve = %d, want 0", len(all))
	}
}

// TestCallActivityStillCallsASingleTriggerTarget: the permissiveness ADR-0035
// recorded stays. A target whose only entry is one message start is still called,
// and flows on from that start.
func TestCallActivityStillCallsASingleTriggerTarget(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(7, "lifecycle", 1)
	ms := b.AddMessageStartEvent("only", nil, false)
	b.Connect(ms, b.AddEndEvent())
	target, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	caller, _ := callerOf(t, 9)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(target)
	p.Deploy(caller)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(9)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := activeProcs(t, h.store); n != 0 {
		t.Fatalf("active instances = %d, want 0 — a single-trigger target is called as before", n)
	}
	if all := incidents(t, h.store); len(all) != 0 {
		t.Fatalf("incidents = %d, want 0", len(all))
	}
}
