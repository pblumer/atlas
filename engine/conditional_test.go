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

// TestConditionalCatchAlreadyTrue: a conditional catch whose condition already holds when the
// token arrives fires at once and passes straight through (ADR-0134). The instance is seeded
// with ready=true, so the catch's arm-time self-evaluation completes it immediately.
func TestConditionalCatchAlreadyTrue(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(100, "cond-catch-true", 1)
	start := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, boolVar("ready", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after conditional catch: process=%d element=%d, want 0 and 0 (condition true at arm, passed through)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the catch fired at once)", v)
	}
}

// TestConditionalCatchWaitsThenFires: a conditional catch whose condition is false at arrival
// parks, then fires when a later SetVariables makes it true — proving the variable-change
// re-check drives an armed conditional (ADR-0134), including via an external SetVariables with
// no activity completing.
func TestConditionalCatchWaitsThenFires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(101, "cond-catch-wait", 1)
	start := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
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
	// Parked at the conditional catch — its condition is false (ready unset).
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 1 (waiting on the condition)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 0 {
		t.Fatalf("end visits = %d, want 0 (condition not yet true)", v)
	}
	// An external variable write makes the condition true; the re-check fires the catch.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("ready", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after set: process=%d element=%d, want 0 and 0 (condition became true, passed through)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the re-check fired the catch)", v)
	}
}

// TestConditionalBoundaryInterruptingFires: an interrupting conditional boundary on a waiting
// service task fires when a variable makes its condition true — cancelling the host and routing
// the recovery flow (ADR-0134).
func TestConditionalBoundaryInterruptingFires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(102, "cond-boundary", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	boundary := b.AddBoundaryConditionalEvent(work, mustCompile(t, "cancelled"), true)
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, done)
	b.Connect(boundary, recovered)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked on the service task's job, boundary armed (condition false).
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (task + armed boundary)", pi, ei)
	}
	// A variable makes the condition true; the interrupting boundary fires.
	p.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("cancelled", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after set: process=%d element=%d, want 0 and 0 (boundary fired, host cancelled)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 {
		t.Errorf("recovery end visits = %d, want 1 (conditional boundary fired)", v[recovered])
	}
	if v[done] != 0 {
		t.Errorf("normal end visits = %d, want 0 (host cancelled)", v[done])
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived the interrupting conditional boundary")
	}
}

// TestConditionalBoundaryFiresImmediatelyIfTrue: a conditional boundary whose condition already
// holds when its host activates fires at once (ADR-0134) — the host never really runs.
func TestConditionalBoundaryFiresImmediatelyIfTrue(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(103, "cond-boundary-now", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	boundary := b.AddBoundaryConditionalEvent(work, mustCompile(t, "cancelled"), true)
	done := b.AddEndEvent()
	recovered := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, done)
	b.Connect(boundary, recovered)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// Seed cancelled=true, so the condition holds the moment the boundary arms.
	p.CreateInstance(cp.Key, boolVar("cancelled", true))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after create: process=%d element=%d, want 0 and 0 (boundary fired at arm)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[recovered] != 1 || v[done] != 0 {
		t.Errorf("visits recovered=%d done=%d, want 1 and 0 (fired immediately)", v[recovered], v[done])
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived an immediately-firing conditional boundary")
	}
}

// TestConditionalCatchRecovers proves an armed conditional catch survives a crash: it parks with
// its condition false; after replaying the log into a fresh store the catch is rebuilt, so a
// post-recovery variable change re-checks it and fires it (ADR-0134, invariant I6).
func TestConditionalCatchRecovers(t *testing.T) {
	dir := t.TempDir()

	b := compiler.NewBuilder(104, "cond-catch-rec", 1)
	start := b.AddStartEvent()
	wait := b.AddConditionalCatchEvent(mustCompile(t, "ready"))
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

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
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 1 (armed conditional catch)", pi, ei)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the armed conditional catch must rebuild.
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
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1 (armed catch rebuilt)", pi, ei)
	}
	// A post-recovery variable change re-checks the recovered catch and fires it.
	p2.SetVariables(model.NewKey(1, 1), 0, "operator", boolVar("ready", true))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after set post-recovery: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 (recovered catch fired on the variable change)", v)
	}
}
