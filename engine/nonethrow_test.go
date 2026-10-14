package engine_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// milestoneProcess builds Start → milestone → End, where the milestone is a none
// intermediate throw event (ADR-0305): a throw that throws nothing and waits for nothing.
func milestoneProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, start, milestone, end int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "milestone", 1)
	s := b.AddStartEvent()
	m := b.AddNoneThrowEvent()
	e := b.AddEndEvent()
	b.Connect(s, m)
	b.Connect(m, e)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, s, m, e
}

// TestMilestoneLeavesATraceAndDoesNotStop is the whole of what a none throw event has to
// do, stated as two facts that pull in opposite directions.
//
// It must not stop: the token flows through without a job, a subscription or a wait, and
// the instance runs to completion in one go. And it must leave a trace: were it not
// recorded, it would be indistinguishable from not having drawn it, and the reason to
// draw one is to be able to ask later when a case reached this point.
func TestMilestoneLeavesATraceAndDoesNotStop(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, start, milestone, end := milestoneProcess(t, 91)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// It did not stop: nothing is left waiting anywhere.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the milestone: process=%d element=%d, want 0 and 0 — a milestone waits for nothing", pi, ei)
	}
	// It left a trace: the per-definition visit counter carries it, which is what the
	// Operations overlay and any later count read (ADR-0080).
	if v := elementVisits(t, h.store, cp.Key); v[milestone] != 1 {
		t.Errorf("milestone visits = %d, want 1 — the point of the element is that it is recorded", v[milestone])
	}
	// And it is on the instance's timeline, in order, between the start and the end —
	// so "when did this case reach the milestone" is answerable per case, not only in
	// aggregate (ADR-0046).
	want := []int32{start, milestone, end}
	if got := elementSteps(t, h.store, model.NewKey(1, 1)); !reflect.DeepEqual(got, want) {
		t.Errorf("step trail = %v, want %v (start → milestone → end)", got, want)
	}
}

// TestMilestoneSurvivesReplay holds the milestone to invariant I4: state after replay
// equals state built live. A pass-through element writes its events and nothing else, so
// there is no bespoke recovery path to get wrong — which is exactly why it is worth
// asserting rather than assuming, since the assumption is what a later change would break.
func TestMilestoneSurvivesReplay(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	cp, start, milestone, end := milestoneProcess(t, 92)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	instKey := model.NewKey(1, 1)
	live := elementSteps(t, h.store, instKey)
	if want := []int32{start, milestone, end}; !reflect.DeepEqual(live, want) {
		t.Fatalf("live step trail = %v, want %v", live, want)
	}
	liveVisits := elementVisits(t, h.store, cp.Key)
	h.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	if replayed := elementSteps(t, h2.store, instKey); !reflect.DeepEqual(replayed, live) {
		t.Errorf("replayed step trail = %v, want the live one %v", replayed, live)
	}
	if replayed := elementVisits(t, h2.store, cp.Key); !reflect.DeepEqual(replayed, liveVisits) {
		t.Errorf("replayed visits = %v, want the live ones %v", replayed, liveVisits)
	}
}
