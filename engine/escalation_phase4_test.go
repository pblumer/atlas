package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// TestEscalationBoundaryCodeMatching puts two escalation boundaries with different codes on one
// subprocess: an escalation end raising one code routes to that boundary, not the other
// (ADR-0125). The nearest *matching* code wins.
func TestEscalationBoundaryCodeMatching(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(80, "esc-twocode", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	iStart := b.AddStartEvent()
	escEnd := b.AddEscalationEndEvent("B")
	b.Connect(iStart, escEnd)
	b.PopScope()
	boundaryA := b.AddBoundaryEscalationEvent(sub, "A", true)
	boundaryB := b.AddBoundaryEscalationEvent(sub, "B", true)
	done := b.AddEndEvent()
	endA := b.AddEndEvent()
	endB := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, done)
	b.Connect(boundaryA, endA)
	b.Connect(boundaryB, endB)
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
		t.Fatalf("after escalation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[endB] != 1 {
		t.Errorf("endB visits = %d, want 1 (the matching-code boundary fired)", v[endB])
	}
	if v[endA] != 0 || v[done] != 0 {
		t.Errorf("visits endA=%d done=%d, want 0 and 0", v[endA], v[done])
	}
}

// TestEscalationCatchAllBoundary proves a code-less escalation boundary catches any raised code
// (ADR-0125).
func TestEscalationCatchAllBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	// The subprocess raises "ANYTHING"; the boundary has an empty code (catch-all), interrupting.
	cp, doneEnd, recEnd := escalationSubprocess(t, 81, "ANYTHING", "", true)

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
		t.Fatalf("after escalation: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recEnd] != 1 || v[doneEnd] != 0 {
		t.Errorf("visits recovered=%d done=%d, want 1 and 0 (catch-all caught the coded escalation)", v[recEnd], v[doneEnd])
	}
}

// escalationChildProcess builds a child process Start → escalationEnd(code) with no escalation
// handler of its own, so its escalation propagates to the caller (ADR-0125/ADR-0076).
func escalationChildProcess(t testing.TB, key uint64, procId, code string) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, procId, 1)
	start := b.AddStartEvent()
	escEnd := b.AddEscalationEndEvent(code)
	b.Connect(start, escEnd)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("child Build: %v", err)
	}
	return built
}

// TestChildEscalationCaughtByCallerBoundary proves an escalation unhandled in a call-activity
// child propagates to the caller: an *interrupting* escalation boundary on the call activity
// catches it, terminating the child and routing the caller to its recovery flow
// (ADR-0125/ADR-0076).
func TestChildEscalationCaughtByCallerBoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	childCp := escalationChildProcess(t, 82, "esc-child", "LATE")

	b := compiler.NewBuilder(83, "esc-caller", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("esc-child", compiler.BindingLatest, false, false)
	boundary := b.AddBoundaryEscalationEvent(call, "LATE", true)
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, done)
	b.Connect(boundary, recovered)
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
	// Both instances are gone: the child aborted, the caller took its recovery flow.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after child escalation: process=%d element=%d, want 0 and 0 (child aborted, caller recovered)", pi, ei)
	}
	v := elementVisits(t, h.store, callerCp.Key)
	if v[recovered] != 1 || v[done] != 0 {
		t.Errorf("caller visits recovered=%d done=%d, want 1 and 0", v[recovered], v[done])
	}
}

// TestChildEscalationUncaughtIsBenign proves a child escalation that no caller handler catches
// is benign — unlike an error, it raises NO incident. Propagation reaches the caller root
// uncaught and returns; the child's escalation end then ends its own path, so the child
// completes, the caller resumes past the call activity, and the whole thing finishes normally
// (ADR-0125, the divergence from ADR-0089's child-error incident).
func TestChildEscalationUncaughtIsBenign(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	childCp := escalationChildProcess(t, 84, "esc-child2", "LATE")

	b := compiler.NewBuilder(85, "esc-caller-nobound", 1)
	start := b.AddStartEvent()
	call := b.AddCallActivity("esc-child2", compiler.BindingLatest, false, false)
	done := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, done)
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
	// No incident, and both instances complete normally.
	if incs := incidents(t, h.store); len(incs) != 0 {
		t.Errorf("incidents = %d, want 0 (an uncaught child escalation is benign, unlike an error)", len(incs))
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after uncaught child escalation: process=%d element=%d, want 0 and 0 (both completed)", pi, ei)
	}
	if v := elementVisits(t, h.store, callerCp.Key)[done]; v != 1 {
		t.Errorf("caller done visits = %d, want 1 (caller resumed past the call activity and finished)", v)
	}
}
