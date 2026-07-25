package engine_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
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

// TestCatchTimerFeelUnresolvableRaisesIncident proves a FEEL catch timer whose
// expression can't resolve to a valid due date (here, a missing variable → null)
// parks its token and raises a job-less incident instead of firing immediately —
// so a broken schedule is visible and resolvable, not silently wrong (ADR-0063).
func TestCatchTimerFeelUnresolvableRaisesIncident(t *testing.T) {
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

	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("unresolvable FEEL catch raised %d incidents, want 1", len(inc))
	}
	for _, v := range inc {
		if v.JobKey != 0 {
			t.Errorf("timer incident JobKey=%d, want 0 (a timer incident has no job)", v.JobKey)
		}
		if v.Message == "" || v.RaisedAt == 0 {
			t.Errorf("incident = %+v, want a non-empty message and a frozen raised-at", v)
		}
	}
	// No timer was created, so ticking fires nothing — the token stays parked.
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after tick: active=%d, want 1 (parked on the incident, not fired)", pi)
	}
}

// TestBoundaryTimerFeelUnresolvableRaisesIncident proves an unresolvable FEEL
// boundary timer parks and raises an incident rather than firing immediately —
// which would have wrongly cancelled the host activity (ADR-0063).
func TestBoundaryTimerFeelUnresolvableRaisesIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	e, err := expr.CompileAuto("timeout")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b := compiler.NewBuilder(904, "feelboundaryinc", 1)
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
	p.CreateInstance(cp.Key) // no "timeout" → the boundary can't arm
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if n := len(incidents(t, h.store)); n != 1 {
		t.Fatalf("unresolvable FEEL boundary raised %d incidents, want 1", n)
	}
	// The host was NOT cancelled: its job is still activatable and both element
	// instances (host + boundary) remain parked.
	if jobGone(t, h.store, jobType) {
		t.Error("host job was cancelled — the boundary fired immediately instead of parking on an incident")
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (host + boundary)", pi, ei)
	}
}

// TestResolveTimerIncidentReRaisesWhenStillBroken proves resolving a timer
// incident genuinely re-arms — re-evaluating the schedule — rather than blindly
// clearing it: with the variable still missing, a fresh incident is raised
// (ADR-0063).
func TestResolveTimerIncidentReRaisesWhenStillBroken(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := feelCatchProcess(t, 905, compiler.TimerFeelDuration, "timeout")
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key) // still no "timeout"
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var elKey uint64
	for k := range incidents(t, h.store) {
		elKey = k
	}

	clk.t = 2_000 // a later clock so a re-raised incident is distinguishable
	p.ResolveIncident(elKey, 1)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}

	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("after resolving a still-broken timer: %d incidents, want 1 (re-raised)", len(inc))
	}
	for _, v := range inc {
		if v.RaisedAt != 2_000 {
			t.Errorf("re-raised incident RaisedAt=%d, want 2000 (a fresh raise from the re-arm)", v.RaisedAt)
		}
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("still parked after resolve: active=%d, want 1", pi)
	}
}

// TestResolveTimerIncidentRearmsWhenFixed proves the payoff: once the variable a
// FEEL timer needs is present, resolving the incident re-arms the timer, which
// then fires normally and the instance completes (ADR-0063). A parallel branch
// supplies the variable after the timer branch has already parked on its incident.
func TestResolveTimerIncidentRearmsWhenFixed(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)
	e, err := expr.CompileAuto("deadline")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b := compiler.NewBuilder(906, "feelrearm", 1)
	start := b.AddStartEvent()
	split := b.AddParallelGateway()
	wait := b.AddTimerCatchSchedule(compiler.TimerSchedule{Kind: compiler.TimerFeelDuration, Expr: e})
	waitEnd := b.AddEndEvent()
	setter := b.AddServiceTask("setup", 3) // its output supplies "deadline"
	setEnd := b.AddEndEvent()
	b.Connect(start, split)
	b.Connect(split, wait)
	b.Connect(wait, waitEnd)
	b.Connect(split, setter)
	b.Connect(setter, setEnd)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(setter).Detail).JobType

	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key) // no "deadline" yet → the catch parks on an incident
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var elKey uint64
	for k := range incidents(t, h.store) {
		elKey = k
	}
	if elKey == 0 {
		t.Fatal("expected a timer incident on the catch branch")
	}

	// The parallel branch's job now supplies the variable the timer needs.
	p.CompleteJob(singleActivatableJob(t, h.store, jobType), strVar("deadline", "PT30S"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (setter): %v", err)
	}

	// Resolving re-arms the catch against the now-present variable — no re-raise.
	p.ResolveIncident(elKey, 1)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("after fixing and resolving: %d incidents remain, want 0", n)
	}

	// The re-armed timer fires at the resolved delay and the instance completes.
	clk.t = 2_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after the re-armed timer fires: active=%d, want 0 (completed)", pi)
	}
}

// TestResolveBoundaryTimerIncidentReRaises proves the resolve/re-arm loop covers
// boundary timers too (not just catch): resolving a still-broken boundary incident
// re-arms the boundary and raises a fresh incident, leaving the host untouched
// (ADR-0063).
func TestResolveBoundaryTimerIncidentReRaises(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	e, err := expr.CompileAuto("timeout")
	if err != nil {
		t.Fatalf("CompileAuto: %v", err)
	}
	b := compiler.NewBuilder(909, "feelboundaryrearm", 1)
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
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key) // no "timeout"
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var elKey uint64
	for k := range incidents(t, h.store) {
		elKey = k
	}

	clk.t = 2_000
	p.ResolveIncident(elKey, 1)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("after resolving a still-broken boundary: %d incidents, want 1 (re-raised)", len(inc))
	}
	for _, v := range inc {
		if v.RaisedAt != 2_000 {
			t.Errorf("re-raised boundary incident RaisedAt=%d, want 2000", v.RaisedAt)
		}
	}
	if jobGone(t, h.store, jobType) {
		t.Error("host job vanished across a boundary incident resolve")
	}
}

// TestCatchTimerFeelDateIncidentNamesField proves the incident message names the
// field that failed to resolve — here a FEEL <timeDate> — so an operator sees what
// kind of schedule broke (ADR-0063).
func TestCatchTimerFeelDateIncidentNamesField(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := feelCatchProcess(t, 908, compiler.TimerFeelDate, "deadline")
	clk := &fixedClock{t: 1_000}
	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key) // no "deadline"
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("unresolvable FEEL date catch raised %d incidents, want 1", len(inc))
	}
	for _, v := range inc {
		if !strings.Contains(v.Message, "date") {
			t.Errorf("incident message = %q, want it to name the failing field (date)", v.Message)
		}
	}
}

// TestTimerIncidentRecovers proves a raised timer incident survives a restart:
// replaying the log restores the incident and the parked, timer-less element
// (ADR-0063, invariant I4).
func TestTimerIncidentRecovers(t *testing.T) {
	dir := t.TempDir()
	cp := feelCatchProcess(t, 907, compiler.TimerFeelDuration, "timeout")

	h1 := openHarness(t, dir)
	clk := &fixedClock{t: 1_000}
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key) // no "timeout" → an incident is raised on the catch
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if len(incidents(t, h1.store)) != 1 {
		t.Fatal("expected one timer incident before restart")
	}
	h1.close(t)

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
	if n := len(incidents(t, store2)); n != 1 {
		t.Fatalf("after replay: %d incidents, want 1 (restored)", n)
	}
	if pi := activeProcs(t, store2); pi != 1 {
		t.Fatalf("after replay: active=%d, want 1 (parked, no timer)", pi)
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
