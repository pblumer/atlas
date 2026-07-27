package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestCatchTimerDateFires proves an intermediate timer catch with a <timeDate>
// waits until the absolute instant, then continues (ADR-0054).
func TestCatchTimerDateFires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const instant = int64(2_000_000)
	b := compiler.NewBuilder(800, "waitdate", 1)
	start := b.AddStartEvent()
	wait := b.AddTimerCatchSchedule(compiler.TimerSchedule{Kind: compiler.TimerDate, BaseNanos: instant})
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
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
	// Parked at the catch before the date.
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("before date: active=%d, want 1 (parked)", pi)
	}
	// At the date it fires and the instance finishes.
	clk.t = instant + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after date: active=%d, want 0 (completed)", pi)
	}
}

// recurringBoundaryProcess builds Start → host service task ("work") → done, with
// a NON-interrupting boundary timer on the host that recurs on an interval cycle
// (Repetitions more fires after the first), each fire routing to a "reminder" end.
func recurringBoundaryProcess(t testing.TB, key uint64, interval int64, reps int32) (cp *compiler.CompiledProcess, jobType, reminderEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "reminders", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("work", 3)
	rem := b.AddBoundaryTimerSchedule(host, false, compiler.TimerSchedule{Kind: compiler.TimerCycleInterval, BaseNanos: interval, Repetitions: reps})
	done := b.AddEndEvent()
	reminder := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(rem, reminder)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, built.ServiceTask(built.Node(host).Detail).JobType, reminder
}

// TestBoundaryTimerCycleRepeats proves a non-interrupting boundary cycle fires the
// modeled number of times while the host keeps waiting, re-arming after each fire
// and stopping when the count runs out — the host is never interrupted (ADR-0054).
func TestBoundaryTimerCycleRepeats(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const interval = int64(10e9)
	cp, jobType, reminderEnd := recurringBoundaryProcess(t, 801, interval, 2) // 3 fires total
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

	for fire := 1; fire <= 3; fire++ {
		clk.t += interval + 1
		if err := p.TickTimers(); err != nil {
			t.Fatalf("TickTimers fire %d: %v", fire, err)
		}
		if got := elementVisits(t, h.store, cp.Key)[reminderEnd]; got != int64(fire) {
			t.Fatalf("after fire %d: reminder visits=%d, want %d", fire, got, fire)
		}
		// The host was never interrupted: its job is still there, instance running.
		if jobGone(t, h.store, jobType) {
			t.Fatalf("fire %d: host job vanished (non-interrupting must not cancel it)", fire)
		}
		if pi := activeProcs(t, h.store); pi != 1 {
			t.Fatalf("fire %d: active procs=%d, want 1 (host still waiting)", fire, pi)
		}
	}

	// The cycle is exhausted after 3 fires: a further tick spawns no more reminders.
	clk.t += interval * 5
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (exhausted): %v", err)
	}
	if got := elementVisits(t, h.store, cp.Key)[reminderEnd]; got != 3 {
		t.Fatalf("cycle over-fired: reminder visits=%d, want 3", got)
	}
}

// TestBoundaryTimerCycleRecovers proves a recurring boundary resumes after a
// restart: its armed timer is restored from the log and keeps firing on schedule
// (ADR-0054, invariant I4).
func TestBoundaryTimerCycleRecovers(t *testing.T) {
	dir := t.TempDir()
	const interval = int64(10e9)
	cp, _, reminderEnd := recurringBoundaryProcess(t, 802, interval, -1) // infinite
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
	clk.t += interval + 1
	if err := p1.TickTimers(); err != nil {
		t.Fatalf("TickTimers 1: %v", err)
	}
	if got := elementVisits(t, h1.store, cp.Key)[reminderEnd]; got != 1 {
		t.Fatalf("before restart: reminder visits=%d, want 1", got)
	}
	h1.close(t)

	// Replay into a fresh store; the still-armed next-occurrence timer comes back.
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
	clk.t += interval + 1
	if err := p2.TickTimers(); err != nil {
		t.Fatalf("TickTimers after recovery: %v", err)
	}
	if got := elementVisits(t, store2, cp.Key)[reminderEnd]; got != 2 {
		t.Fatalf("after recovery: reminder visits=%d, want 2 (resumed)", got)
	}
}
