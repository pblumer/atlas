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

// timerStartProcess builds timerStart(schedule) → End for definition key/id. The
// instance a firing creates runs straight to completion, so a test observes fires
// through the completed-instance history.
func timerStartProcess(t testing.TB, key uint64, id string, schedule compiler.TimerSchedule) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, id, 1)
	start := b.AddTimerStartEvent(schedule)
	end := b.AddEndEvent()
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// countStartTimers returns how many armed start timers the store holds — the
// anti-double-arm invariant checks this is exactly one per timer start event.
func countStartTimers(t *testing.T, s *state.Store) int {
	t.Helper()
	n := 0
	if err := s.StartTimers(func(uint64, *model.TimerValue) error { n++; return nil }); err != nil {
		t.Fatalf("StartTimers: %v", err)
	}
	return n
}

// armDeployed mirrors what the API's deployModel does on a fresh deploy: register
// the definition, then arm its timer start events and drain (ADR-0051).
func armDeployed(t *testing.T, p *engine.Processor, cp *compiler.CompiledProcess) {
	t.Helper()
	p.Deploy(cp)
	p.ArmStartTimers(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (arm): %v", err)
	}
}

// TestTimerStartDurationFiresOnce proves a duration timer start event arms a
// durable timer at deploy, creates one instance when it comes due, and does not
// fire again (ADR-0051).
func TestTimerStartDurationFiresOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	cp := timerStartProcess(t, 700, "hourly", compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: dur})
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, cp)

	// Armed but not fired: one start timer waits, no instance exists yet.
	if got := countStartTimers(t, h.store); got != 1 {
		t.Fatalf("armed start timers = %d, want 1", got)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("before due: active=%d, want 0 (a timer start does not run on deploy)", pi)
	}

	// Come due: exactly one instance is created and runs to completion.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if got := len(completedInstances(t, h.store)); got != 1 {
		t.Fatalf("after due: completed=%d, want 1", got)
	}
	if got := countStartTimers(t, h.store); got != 0 {
		t.Fatalf("after a one-shot fire: armed timers=%d, want 0 (not rescheduled)", got)
	}

	// A later tick must not fire it again.
	clk.t += dur * 10
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (later): %v", err)
	}
	if got := len(completedInstances(t, h.store)); got != 1 {
		t.Fatalf("one-shot fired again: completed=%d, want 1", got)
	}
}

// TestTimerStartArmedTimerRecovers proves an armed start timer survives a restart:
// after replaying the log into a fresh store the timer is present (exactly one, no
// double-arm — recovery does not re-arm) and fires afterward (ADR-0051).
func TestTimerStartArmedTimerRecovers(t *testing.T) {
	dir := t.TempDir()
	const dur = int64(60e9)
	cp := timerStartProcess(t, 701, "delayed", compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: dur})
	clk := &fixedClock{t: 5_000}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	armDeployed(t, p1, cp)
	if got := countStartTimers(t, h1.store); got != 1 {
		t.Fatalf("armed timers before restart = %d, want 1", got)
	}
	h1.close(t)

	// Replay into a fresh store. loadDeployments re-registers the definition but,
	// unlike a fresh deploy, does NOT re-arm — the timer comes back from the log.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clk)
	p2.Deploy(cp) // like loadDeployments: register, no re-arm
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if got := countStartTimers(t, store2); got != 1 {
		t.Fatalf("armed timers after replay = %d, want exactly 1 (no double-arm)", got)
	}

	// The recovered timer fires normally.
	clk.t = 5_000 + dur + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers after recovery: %v", err)
	}
	if got := len(completedInstances(t, store2)); got != 1 {
		t.Fatalf("recovered timer did not fire: completed=%d, want 1", got)
	}
}

// TestTimerStartCycleRepeats proves an interval cycle fires the modeled number of
// times (Rn = n total), rescheduling itself each fire and stopping when the count
// runs out (ADR-0051).
func TestTimerStartCycleRepeats(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const interval = int64(10e9)
	// R3: three firings total → Repetitions = 2 (fires after the first).
	cp := timerStartProcess(t, 702, "thrice", compiler.TimerSchedule{Kind: compiler.TimerCycleInterval, BaseNanos: interval, Repetitions: 2})
	clk := &fixedClock{t: 100}

	p := engine.New(1, h.log, h.store, clk)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, cp)

	for fire := 1; fire <= 3; fire++ {
		clk.t += interval + 1
		if err := p.TickTimers(); err != nil {
			t.Fatalf("TickTimers fire %d: %v", fire, err)
		}
		if got := len(completedInstances(t, h.store)); got != fire {
			t.Fatalf("after fire %d: completed=%d, want %d", fire, got, fire)
		}
	}
	// The cycle is exhausted: no timer left, and further ticks create nothing.
	if got := countStartTimers(t, h.store); got != 0 {
		t.Fatalf("after last fire: armed timers=%d, want 0 (cycle exhausted)", got)
	}
	clk.t += interval * 5
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (exhausted): %v", err)
	}
	if got := len(completedInstances(t, h.store)); got != 3 {
		t.Fatalf("cycle over-fired: completed=%d, want 3", got)
	}
}

// TestTimerStartDateDoesNotRefireAfterRestart proves an absolute-date timer fires
// once, and that replaying the log after it fired does not resurrect it — the
// TimerTriggered event is replayed, so the timer stays gone (ADR-0051, invariant
// I4/I6).
func TestTimerStartDateDoesNotRefireAfterRestart(t *testing.T) {
	dir := t.TempDir()
	const instant = int64(2_000_000)
	cp := timerStartProcess(t, 703, "atdate", compiler.TimerSchedule{Kind: compiler.TimerDate, BaseNanos: instant})
	clk := &fixedClock{t: 1_000}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	armDeployed(t, p1, cp)
	clk.t = instant + 1
	if err := p1.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if got := len(completedInstances(t, h1.store)); got != 1 {
		t.Fatalf("date timer: completed=%d, want 1", got)
	}
	h1.close(t)

	// Replay: the fired timer must not come back and must not fire a second time.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if got := countStartTimers(t, store2); got != 0 {
		t.Fatalf("after replay of a fired date timer: armed=%d, want 0", got)
	}
	clk.t = instant + 1_000
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers after recovery: %v", err)
	}
	if got := len(completedInstances(t, store2)); got != 1 {
		t.Fatalf("date timer re-fired after restart: completed=%d, want 1", got)
	}
}

// TestTimerStartNewVersionSupersedes proves deploying a new version of a process
// retires the prior version's start timer, so only the latest schedule fires — one
// instance per due date, not one per deployed version (ADR-0051).
func TestTimerStartNewVersionSupersedes(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(20e9)
	v1 := timerStartProcess(t, 710, "sched", compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: dur})
	v2 := timerStartProcess(t, 711, "sched", compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: dur})
	v2.Version = 2
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, v1)
	armDeployed(t, p, v2) // same process id: supersedes v1's timer

	// Exactly one armed timer remains — v2's — even though both versions deployed.
	if got := countStartTimers(t, h.store); got != 1 {
		t.Fatalf("after superseding deploy: armed timers=%d, want 1", got)
	}

	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	completed := completedInstances(t, h.store)
	if len(completed) != 1 {
		t.Fatalf("superseded schedule fired %d instances, want 1", len(completed))
	}
	for _, pi := range completed {
		if pi.ProcessDefKey != v2.Key {
			t.Fatalf("fired instance defKey=%d, want v2 (%d)", pi.ProcessDefKey, v2.Key)
		}
	}
}

// TestTimerStartUndeployedSelfRetires proves a start timer whose definition was
// undeployed self-retires when it comes due: it creates no instance and does not
// reschedule (ADR-0051).
func TestTimerStartUndeployedSelfRetires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const interval = int64(10e9)
	cp := timerStartProcess(t, 730, "gone", compiler.TimerSchedule{Kind: compiler.TimerCycleInterval, BaseNanos: interval, Repetitions: -1})
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, cp)
	p.Undeploy(cp.Key)

	clk.t = 1_000 + interval + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if got := len(completedInstances(t, h.store)); got != 0 {
		t.Fatalf("undeployed timer created %d instances, want 0", got)
	}
	if got := countStartTimers(t, h.store); got != 0 {
		t.Fatalf("undeployed cycle rescheduled itself: armed=%d, want 0", got)
	}
}

// TestTimerStartArmIsIdempotent proves arming the same definition twice leaves a
// single timer, not two — the guard against re-arming (ADR-0051).
func TestTimerStartArmIsIdempotent(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := timerStartProcess(t, 740, "once", compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: 30e9})

	p := engine.New(1, h.log, h.store, &fixedClock{t: 1_000})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, cp)
	// Arm again: the element is already armed, so nothing new is created.
	p.ArmStartTimers(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (re-arm): %v", err)
	}
	if got := countStartTimers(t, h.store); got != 1 {
		t.Fatalf("after re-arm: armed timers=%d, want 1 (idempotent)", got)
	}
}

// TestTimerStartArmLeavesOtherProcessesAlone proves arming one process's timer
// start does not retire an unrelated process's start timer — supersession is
// scoped to a single process id (ADR-0051).
func TestTimerStartArmLeavesOtherProcessesAlone(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	a := timerStartProcess(t, 750, "aaa", compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: 30e9})
	b := timerStartProcess(t, 751, "bbb", compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: 30e9})

	p := engine.New(1, h.log, h.store, &fixedClock{t: 1_000})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, a)
	armDeployed(t, p, b) // different process id: must not touch a's timer

	if got := countStartTimers(t, h.store); got != 2 {
		t.Fatalf("two unrelated timer starts: armed=%d, want 2", got)
	}
}

// TestTimerStartArmUnknownDefinition proves arming a definition key that was never
// deployed is a harmless no-op (ADR-0051) — the arm handler finds no compiled
// process and does nothing.
func TestTimerStartArmUnknownDefinition(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p := engine.New(1, h.log, h.store, &fixedClock{t: 1_000})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.ArmStartTimers(999) // never deployed
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := countStartTimers(t, h.store); got != 0 {
		t.Fatalf("arming an unknown definition created %d timers, want 0", got)
	}
}
