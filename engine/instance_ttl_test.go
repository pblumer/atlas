package engine_test

import (
	"math"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// ttlProcess builds Start → ServiceTask → End with a per-definition instance TTL
// (ADR-0085). The instance parks at the service task, so nothing completes it until
// either the TTL fires or the job is completed — the shape a TTL is meant to bound.
func ttlProcess(t testing.TB, ttlNanos int64) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "ttl", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask(jobName, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.SetInstanceTtl(ttlNanos)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(task).Detail).JobType
}

// countTimers returns how many timers of any kind the store holds — the whole due-date
// index (an "all timers" scan via a far-future upper bound).
func countTimers(t *testing.T, s *state.Store) int {
	t.Helper()
	n := 0
	if err := s.DueTimers(math.MaxInt64, func(uint64, *model.TimerValue) error { n++; return nil }); err != nil {
		t.Fatalf("DueTimers: %v", err)
	}
	return n
}

// timersDueAt returns how many timers are due at or before now — what the scheduler
// would fire on a tick at that clock value.
func timersDueAt(t *testing.T, s *state.Store, now int64) int {
	t.Helper()
	n := 0
	if err := s.DueTimers(now, func(uint64, *model.TimerValue) error { n++; return nil }); err != nil {
		t.Fatalf("DueTimers: %v", err)
	}
	return n
}

// TestInstanceTTLExpires parks an instance at a service task and lets its TTL come due:
// the expiry timer fires, the instance is terminated through the normal cancellation
// path (gone from active state, recorded in history as terminated), and its timer is
// removed from the index.
func TestInstanceTTLExpires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const ttl = int64(60e9)
	cp, _ := ttlProcess(t, ttl)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked at the task, with exactly one armed expiry timer due at CreatedAt+TTL.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after start: process=%d element=%d, want 1 and 1 (parked at task)", pi, ei)
	}
	if n := countTimers(t, h.store); n != 1 {
		t.Fatalf("armed timers = %d, want 1 (the expiry timer)", n)
	}
	// Not yet due one nanosecond before the TTL; due at the TTL boundary.
	if n := timersDueAt(t, h.store, 1_000+ttl-1); n != 0 {
		t.Fatalf("timers due before TTL = %d, want 0", n)
	}
	if n := timersDueAt(t, h.store, 1_000+ttl); n != 1 {
		t.Fatalf("timers due at TTL = %d, want 1", n)
	}

	// Advance past the TTL and tick: the instance expires.
	clk.t = 1_000 + ttl + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after expiry: process=%d element=%d, want 0 and 0", pi, ei)
	}
	piKey := model.NewKey(1, 1)
	done := completedInstances(t, h.store)
	if v, ok := done[piKey]; !ok || v.State != model.PITerminated {
		t.Fatalf("history[%d] = %+v (ok=%v), want a terminated record", piKey, v, ok)
	}
	if n := countTimers(t, h.store); n != 0 {
		t.Fatalf("timers after expiry = %d, want 0 (fired and removed)", n)
	}
}

// TestInstanceTTLClearedOnCompletion completes the parked job before the TTL is due:
// the instance finishes normally and its expiry timer is retired in the same batch, so
// no armed timer lingers and a later tick past the TTL is a clean no-op.
func TestInstanceTTLClearedOnCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const ttl = int64(60e9)
	cp, jobType := ttlProcess(t, ttl)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := countTimers(t, h.store); n != 1 {
		t.Fatalf("armed timers = %d, want 1 (the expiry timer)", n)
	}

	// Complete the job before the TTL: the instance finishes down the normal path.
	job := singleActivatableJob(t, h.store, jobType)
	p.CompleteJob(job)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	piKey := model.NewKey(1, 1)
	done := completedInstances(t, h.store)
	if v, ok := done[piKey]; !ok || v.State != model.PICompleted {
		t.Fatalf("history[%d] = %+v (ok=%v), want a completed record", piKey, v, ok)
	}
	if n := countTimers(t, h.store); n != 0 {
		t.Fatalf("timers after completion = %d, want 0 (expiry timer retired)", n)
	}

	// A tick past the (now-cleared) TTL changes nothing: no timer to fire.
	clk.t = 1_000 + ttl + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (post-completion): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after post-completion tick: procs=%d, want 0", pi)
	}
}

// TestInstanceTTLCanceledManuallyRetiresTimer cancels a TTL-bearing instance before it
// expires — the API / bulk-drain path an operator uses to clear a pile-up. The instance
// terminates and its still-armed expiry timer is retired in the same batch, so a drained
// definition leaves no dangling timers behind.
func TestInstanceTTLCanceledManuallyRetiresTimer(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const ttl = int64(60e9)
	cp, _ := ttlProcess(t, ttl)
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := countTimers(t, h.store); n != 1 {
		t.Fatalf("armed timers = %d, want 1 (the expiry timer)", n)
	}

	// Cancel well before the TTL: the still-armed timer must be retired with the instance.
	piKey := model.NewKey(1, 1)
	p.CancelInstance(piKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after cancel: procs=%d, want 0", pi)
	}
	if n := countTimers(t, h.store); n != 0 {
		t.Fatalf("timers after cancel = %d, want 0 (expiry timer retired)", n)
	}
	// The retired timer never fires: a later tick past the TTL is a clean no-op.
	clk.t = 1_000 + ttl + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (post-cancel): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after post-cancel tick: procs=%d, want 0", pi)
	}
}

// TestInstanceWithoutTTLArmsNoTimer confirms the feature is opt-in: a definition with
// no TTL schedules no expiry timer, so the default behavior (instances live until
// completed or cancelled) is unchanged.
func TestInstanceWithoutTTLArmsNoTimer(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t) // no instanceTtl

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := countTimers(t, h.store); n != 0 {
		t.Fatalf("armed timers = %d, want 0 (no TTL configured)", n)
	}
}

// TestInstanceTTLRecovers proves the expiry timer is durable: after a restart that
// replays the log into a fresh store, the timer is rebuilt from the events (identical
// due date, no wall-clock read on replay — I4/I6) and still fires the instance.
func TestInstanceTTLRecovers(t *testing.T) {
	dir := t.TempDir()
	const ttl = int64(60e9)
	cp, _ := ttlProcess(t, ttl)
	clk := &fixedClock{t: 1_000}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := countTimers(t, h1.store); n != 1 {
		t.Fatalf("armed timers before restart = %d, want 1", n)
	}
	h1.close(t)

	// Restart into a fresh store from the same log.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if n := countTimers(t, h2.store); n != 1 {
		t.Fatalf("armed timers after recovery = %d, want 1 (rebuilt from the log)", n)
	}

	// The recovered timer still fires and terminates the instance.
	clk.t = 1_000 + ttl + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers (recovered): %v", err)
	}
	piKey := model.NewKey(1, 1)
	done := completedInstances(t, h2.store)
	if v, ok := done[piKey]; !ok || v.State != model.PITerminated {
		t.Fatalf("history[%d] = %+v (ok=%v), want a terminated record after recovery", piKey, v, ok)
	}
}
