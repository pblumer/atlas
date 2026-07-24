package engine_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// feelCatchProcess builds Start → catch(FEEL schedule over feelSrc) → End.
func feelCatchProcess(t testing.TB, key uint64, kind compiler.TimerScheduleKind, feelSrc string) *compiler.CompiledProcess {
	t.Helper()
	e, err := expr.CompileAuto(feelSrc)
	if err != nil {
		t.Fatalf("CompileAuto(%q): %v", feelSrc, err)
	}
	b := compiler.NewBuilder(key, "feeltimer", 1)
	start := b.AddStartEvent()
	wait := b.AddTimerCatchSchedule(compiler.TimerSchedule{Kind: kind, Expr: e})
	end := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

func strVar(name, text string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarString, Text: text}
}

// TestCatchTimerFeelFirstClassDuration proves a catch timer whose FEEL expression
// yields a first-class duration value (duration(...) over a variable) resolves to
// the exact delay, not via a string round-trip (ADR-0057).
func TestCatchTimerFeelFirstClassDuration(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(45e9) // PT45S
	cp := feelCatchProcess(t, 920, compiler.TimerFeelDuration, `duration(timeout)`)
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, strVar("timeout", "PT45S"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	clk.t = 1_000 + dur - 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (before): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("before due: active=%d, want 1 (parked)", pi)
	}
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (after): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after due: active=%d, want 0 (fired at the exact resolved delay)", pi)
	}
}

// TestBoundaryTimerFeelCycle proves a non-interrupting boundary with a FEEL
// <timeCycle> resolves the cadence from an instance variable and recurs the
// resolved number of times (ADR-0056).
func TestBoundaryTimerFeelCycle(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const interval = int64(10e9)
	e, err := expr.CompileAuto("cadence")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b := compiler.NewBuilder(910, "feelcycle", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("work", 3)
	rem := b.AddBoundaryTimerSchedule(host, false, compiler.TimerSchedule{Kind: compiler.TimerFeelCycle, Expr: e})
	done := b.AddEndEvent()
	reminder := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(rem, reminder)
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
	// "R3/PT10S": 3 fires total, every 10s.
	p.CreateInstance(cp.Key, strVar("cadence", "R3/PT10S"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	for fire := 1; fire <= 3; fire++ {
		clk.t += interval + 1
		if err := p.TickTimers(); err != nil {
			t.Fatalf("TickTimers fire %d: %v", fire, err)
		}
		if got := elementVisits(t, h.store, cp.Key)[reminder]; got != int64(fire) {
			t.Fatalf("after fire %d: reminder visits=%d, want %d", fire, got, fire)
		}
	}
	// Exhausted after 3 fires.
	clk.t += interval * 5
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (exhausted): %v", err)
	}
	if got := elementVisits(t, h.store, cp.Key)[reminder]; got != 3 {
		t.Fatalf("FEEL cycle over-fired: reminder visits=%d, want 3", got)
	}
}

// TestStartTimerFeelConstant proves a timer start event with a constant FEEL
// schedule (no variables) resolves against an empty scope at arm and fires
// (ADR-0056).
func TestStartTimerFeelConstant(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	e, err := expr.CompileAuto(`"PT30S"`)
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b := compiler.NewBuilder(911, "feelstart", 1)
	st := b.AddTimerStartEvent(compiler.TimerSchedule{Kind: compiler.TimerFeelDuration, Expr: e})
	end := b.AddEndEvent()
	b.Connect(st, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, cp)
	if got := countStartTimers(t, h.store); got != 1 {
		t.Fatalf("armed start timers = %d, want 1 (constant FEEL resolved at arm)", got)
	}
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if got := len(completedInstances(t, h.store)); got != 1 {
		t.Fatalf("constant FEEL start: completed=%d, want 1", got)
	}
}

// TestCatchTimerFeelDuration proves a catch timer whose <timeDuration> is a FEEL
// expression waits the duration the instance variable resolves to (ADR-0055).
func TestCatchTimerFeelDuration(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	cp := feelCatchProcess(t, 900, compiler.TimerFeelDuration, "timeout")
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, strVar("timeout", "PT30S"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Just before the resolved due date: still parked.
	clk.t = 1_000 + dur - 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (before): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("before due: active=%d, want 1 (parked)", pi)
	}
	// After it: fires and the instance finishes.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (after): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after due: active=%d, want 0 (fired)", pi)
	}
}

// TestCatchTimerFeelDate proves a catch timer whose <timeDate> is a FEEL
// expression fires at the absolute instant the variable resolves to (ADR-0055).
func TestCatchTimerFeelDate(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	instant, _ := time.Parse(time.RFC3339, "2026-08-01T09:00:00Z")
	cp := feelCatchProcess(t, 901, compiler.TimerFeelDate, "deadline")
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, strVar("deadline", "2026-08-01T09:00:00Z"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("before instant: active=%d, want 1 (parked)", pi)
	}
	clk.t = instant.UnixNano() + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after instant: active=%d, want 0 (fired)", pi)
	}
}

// TestCatchTimerFeelUnresolvableFiresNow proves a FEEL timer whose expression
// can't resolve to a valid due date (here, a missing variable → null) fires
// immediately rather than wedging the token (ADR-0055).
func TestCatchTimerFeelUnresolvableFiresNow(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := feelCatchProcess(t, 902, compiler.TimerFeelDuration, "timeout")
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key) // no "timeout" variable → unresolvable
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The timer's due date was frozen to the creation clock, so it is already due.
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("unresolvable FEEL timer: active=%d, want 0 (fires immediately)", pi)
	}
}

// TestBoundaryTimerFeelDuration proves a FEEL duration works on a boundary timer:
// an interrupting boundary fires after the resolved delay and cancels the host.
func TestBoundaryTimerFeelDuration(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(10e9)
	e, err := expr.CompileAuto("timeout")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b := compiler.NewBuilder(903, "feelboundary", 1)
	start := b.AddStartEvent()
	host := b.AddServiceTask("work", 3)
	bnd := b.AddBoundaryTimerSchedule(host, true, compiler.TimerSchedule{Kind: compiler.TimerFeelDuration, Expr: e})
	done := b.AddEndEvent()
	esc := b.AddEndEvent()
	b.Connect(start, host)
	b.Connect(host, done)
	b.Connect(bnd, esc)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(host).Detail).JobType

	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, strVar("timeout", "PT10S"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (host + armed boundary)", pi, ei)
	}

	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after boundary fire: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if !jobGone(t, h.store, jobType) {
		t.Error("host job survived an interrupting FEEL boundary")
	}
}
