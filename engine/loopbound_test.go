package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// runawayLoopProcess builds Start → work → End where work is a script task repeating
// on a condition that never turns false and states no loopMaximum — the one shape that
// can spin on its own.
func runawayLoopProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "runaway", 1)
	start := b.AddStartEvent()
	work := b.AddScriptTask(mustCompile(t, `"ran"`), "last")
	b.SetStandardLoop(work, false, 0 /*no maximum*/, mustCompile(t, "true"))
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, work
}

// TestRunawayStandardLoopParksWithIncident: a loop whose condition never turns false
// and that states no maximum runs the safety ceiling's worth of iterations and then
// stops with an incident instead of spinning forever on the partition. It is parked,
// not finished: the body never takes its outgoing flow, so nothing downstream runs on
// a fabricated result (ADR-0133, amended).
func TestRunawayStandardLoopParksWithIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, work := runawayLoopProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// It ran the ceiling, not one more.
	if got := int(elementVisits(t, h.store, cp.Key)[work]) - 1; got != compiler.SafeLoopCeiling {
		t.Errorf("iterations = %d, want %d (the safety ceiling)", got, compiler.SafeLoopCeiling)
	}
	// And it is parked with an incident that says why, on the loop body.
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("incidents = %d, want 1", len(inc))
	}
	for _, v := range inc {
		if !strings.Contains(v.Message, "loop") {
			t.Errorf("incident message = %q, want it to name the loop", v.Message)
		}
		if v.JobKey != 0 {
			t.Errorf("incident JobKey = %d, want 0 (the loop body holds no job)", v.JobKey)
		}
	}
	// The instance is still alive and the end event was never reached.
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Errorf("process instances = %d, want 1 (parked, not completed)", pi)
	}
	if done := completedInstances(t, h.store); len(done) != 0 {
		t.Errorf("completed instances = %d, want 0 (the loop never finished)", len(done))
	}
}

// TestResolvingRunawayLoopGrantsAnotherCeiling: the incident is a checkpoint, not a
// grave. An operator who decides the loop is doing real work resolves it, and the loop
// runs another ceiling's worth before asking again — so a legitimately long loop can be
// carried through by hand, while a broken one keeps stopping.
func TestResolvingRunawayLoopGrantsAnotherCeiling(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, work := runawayLoopProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var body uint64
	for k := range incidents(t, h.store) {
		body = k
	}
	if body == 0 {
		t.Fatal("no incident to resolve")
	}

	p.ResolveIncident(body, 1)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle after resolve: %v", err)
	}
	if got := int(elementVisits(t, h.store, cp.Key)[work]) - 1; got != 2*compiler.SafeLoopCeiling {
		t.Errorf("iterations after one resolve = %d, want %d", got, 2*compiler.SafeLoopCeiling)
	}
	if len(incidents(t, h.store)) != 1 {
		t.Errorf("incidents = %d, want 1 (it parks again at the next ceiling)", len(incidents(t, h.store)))
	}
}

// TestStatedLoopMaximumIsNotCapped: the ceiling is a backstop for a loop that states no
// bound. A model that says loopMaximum out loud is bounded by *that* number — even past
// the ceiling — and finishes normally with no incident.
func TestStatedLoopMaximumIsNotCapped(t *testing.T) {
	b := compiler.NewBuilder(1, "stated-max", 1)
	start := b.AddStartEvent()
	work := b.AddScriptTask(mustCompile(t, `"ran"`), "last")
	b.SetStandardLoop(work, false, compiler.SafeLoopCeiling+5, mustCompile(t, "true"))
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	h := runStdLoop(t, cp) // asserts the instance completed and left nothing parked

	if got := iterations(t, h, cp, work); got != compiler.SafeLoopCeiling+5 {
		t.Errorf("iterations = %d, want %d (the author's maximum governs)", got, compiler.SafeLoopCeiling+5)
	}
	if len(incidents(t, h.store)) != 0 {
		t.Errorf("incidents = %d, want 0 (a stated bound is not a runaway)", len(incidents(t, h.store)))
	}
}

// TestRunawayLoopIncidentRecovers: the parked loop is rebuilt from the log — the
// incident, the body, and the iteration count — so a restart neither loses the incident
// nor restarts the loop from zero.
func TestRunawayLoopIncidentRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, work := runawayLoopProcess(t)
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
	if len(incidents(t, h1.store)) != 1 {
		t.Fatalf("before crash: incidents = %d, want 1", len(incidents(t, h1.store)))
	}
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	inc := incidents(t, h2.store)
	if len(inc) != 1 {
		t.Fatalf("after recovery: incidents = %d, want 1", len(inc))
	}
	var body uint64
	for k := range inc {
		body = k
	}
	// Resolving the recovered incident continues the loop from where it stopped, not
	// from the start.
	p2.ResolveIncident(body, 1)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if got := int(elementVisits(t, h2.store, cp.Key)[work]) - 1; got != 2*compiler.SafeLoopCeiling {
		t.Errorf("iterations after recovery + resolve = %d, want %d", got, 2*compiler.SafeLoopCeiling)
	}
}
