package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// buildWhoRanChild builds a trivial child process that writes a constant into
// whoRan, so a caller propagating child variables back can report which target
// actually ran.
func buildWhoRanChild(t *testing.T, key uint64, processId string, version int32, who string) *compiler.CompiledProcess {
	t.Helper()
	cb := compiler.NewBuilder(key, processId, version)
	cs := cb.AddStartEvent()
	ct := cb.AddScriptTask(mustCompile(t, `"`+who+`"`), "whoRan")
	ce := cb.AddEndEvent()
	cb.Connect(cs, ct)
	cb.Connect(ct, ce)
	cp, err := cb.Build()
	if err != nil {
		t.Fatalf("child %q Build: %v", processId, err)
	}
	return cp
}

// buildWhoRanCaller builds a caller that calls calledId (latest binding, propagate
// all both ways) so the child's whoRan comes back to the caller's root scope.
func buildWhoRanCaller(t *testing.T, key uint64, calledId string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "caller", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity(calledId, compiler.BindingLatest, true /*propParent*/, true /*propChild*/)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("caller Build: %v", err)
	}
	return cp
}

// TestCallTargetOverrideRedirect redirects calls to "child-proc" to "child-alt" on
// this server: the caller ends up running the alt child, not the default one, and
// its whoRan reflects that — without any change to the model (ADR-0105).
func TestCallTargetOverrideRedirect(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	proc := buildWhoRanChild(t, 8, "child-proc", 1, "proc")
	alt := buildWhoRanChild(t, 7, "child-alt", 1, "alt")
	caller := buildWhoRanCaller(t, 9, "child-proc")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(proc)
	p.Deploy(alt)
	p.Deploy(caller)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.SetCallTargetOverride("child-proc", engine.CallTargetOverride{RedirectProcessId: "child-alt"})

	p.CreateInstance(caller.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (both completed)", pi, ei)
	}
	callerRoot := model.NewKey(1, 1)
	if got := readVar(t, h.store, callerRoot, "whoRan"); got == nil || got.Text != "alt" {
		t.Errorf("caller whoRan = %v, want alt (redirected)", got)
	}
}

// TestCallTargetOverridePin pins "child-proc" to its older version even though a
// newer one is deployed (latest would otherwise win), proving the override selects
// an exact definition key (ADR-0105).
func TestCallTargetOverridePin(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	v1 := buildWhoRanChild(t, 8, "child-proc", 1, "v1")
	v2 := buildWhoRanChild(t, 10, "child-proc", 2, "v2")
	caller := buildWhoRanCaller(t, 9, "child-proc")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(v1)
	p.Deploy(v2) // latest is now v2
	p.Deploy(caller)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// Pin to v1's key; without the pin, latest resolution would pick v2.
	p.SetCallTargetOverride("child-proc", engine.CallTargetOverride{PinnedDefKey: v1.Key})

	p.CreateInstance(caller.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	callerRoot := model.NewKey(1, 1)
	if got := readVar(t, h.store, callerRoot, "whoRan"); got == nil || got.Text != "v1" {
		t.Errorf("caller whoRan = %v, want v1 (pinned)", got)
	}
}

// TestCallTargetOverrideDisable disables a call target: the call activity parks
// exactly as an undeployed callee does — the caller stays active and no child is
// created (ADR-0105). Clearing the override then lets it resolve again.
func TestCallTargetOverrideDisable(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	proc := buildWhoRanChild(t, 8, "child-proc", 1, "proc")
	caller := buildWhoRanCaller(t, 9, "child-proc")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(proc)
	p.Deploy(caller)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.SetCallTargetOverride("child-proc", engine.CallTargetOverride{Disabled: true})

	p.CreateInstance(caller.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The caller is parked on its call activity; nothing completed.
	if pi, _ := counts(t, h.store); pi == 0 {
		t.Fatalf("after run with disabled target: process count = 0, want the caller still active (parked)")
	}

	// Clearing the override lets a fresh caller resolve and complete.
	p.ClearCallTargetOverride("child-proc")
	p.CreateInstance(caller.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after clear): %v", err)
	}
	// Exactly one root instance completes (the second caller); the first is still
	// parked. Its whoRan proves the target resolved again after the clear.
	var completedRoot uint64
	for k, v := range completedInstances(t, h.store) {
		if v.ParentElementInstanceKey == 0 {
			completedRoot = k
		}
	}
	if completedRoot == 0 {
		t.Fatal("no completed caller instance after clearing the override")
	}
	if got := readVar(t, h.store, completedRoot, "whoRan"); got == nil || got.Text != "proc" {
		t.Errorf("second caller whoRan = %v, want proc (override cleared)", got)
	}
}
