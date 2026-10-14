package rungraph

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// W4 of ADR-0404 §4, first half: *"seed from a consistent snapshot of the state store …
// keep current from the tailer, **starting at the snapshot's position**."*
//
// Nothing can start at the snapshot's position while the snapshot does not say what its
// position was. W1 built the seed and W2 the queries, and neither carries one: a `Graph` was
// a structure with no statement about *when* it is true. That is also what §5's own rendering
// rule forbids downstream — "a density without a named reference is a picture of nothing … it
// must say dense in what, and as of when" — so this is the precondition for the cloud as much
// as for the tailer.
//
// So a source has to be able to state its position, and the interface requires it rather than
// asking politely. An optional one would let a source omit it, and then a projection claiming
// "as of 0" would be indistinguishable from one that genuinely is at genesis — a claim nobody
// could check is worse than no claim.

// TestAGraphStatesThePositionItIsAsOf is the claim: the built graph carries the log position
// of the snapshot it was seeded from.
func TestAGraphStatesThePositionItIsAsOf(t *testing.T) {
	src := &fakeSource{
		position: 4711,
		keys:     []uint64{10, 20},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0),
			el(0, 10, 0, 0),
		},
	}
	g, err := Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Position() != 4711 {
		t.Errorf("Position() = %d, want the source's 4711", g.Position())
	}
}

// TestABuildRefusesASourceThatCannotStateItsPosition keeps the claim from being silently
// absent. A source whose position read fails has not produced a snapshot anybody can reason
// about — the tailer would not know where to resume, and the picture would not know what to
// say it is as of — so the build fails rather than publishing a graph with a position of zero
// that reads like genesis.
func TestABuildRefusesASourceThatCannotStateItsPosition(t *testing.T) {
	src := &fakeSource{
		positionErr: true,
		keys:        []uint64{10},
		values:      []*model.ElementInstanceValue{el(0, 0, 0, 0)},
	}
	if _, err := Build(src); err == nil {
		t.Fatal("Build published a graph from a source that could not state its position")
	}
	// BuildOrdinals is the other entry point and owes the same refusal, because the ordinal
	// map is the persisted half (§3): one persisted at an unknown position is a map nobody
	// can reconcile with a log.
	if _, err := BuildOrdinals(src); err == nil {
		t.Fatal("BuildOrdinals published an ordinal map from a source with no position")
	}
}

// TestAnEmptyStoreStillStatesAPosition is the case that would otherwise be the hole: a graph
// with no nodes is a legitimate answer on a fresh server, and it still has to say when it is
// true — otherwise a tailer resuming from it would start at genesis and re-apply a log the
// seed already covered.
func TestAnEmptyStoreStillStatesAPosition(t *testing.T) {
	g, err := Build(&fakeSource{position: 99})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Ordinals.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", g.Ordinals.Len())
	}
	if g.Position() != 99 {
		t.Errorf("Position() = %d, want 99 — an empty graph is still as of a moment", g.Position())
	}
}

// TestThePositionClaimHoldsOnEngineWrittenState is what makes the claim worth carrying. A
// position is decoration unless the graph genuinely contains what the log held at that
// position and nothing that arrived after it — which is the whole of what a tailer resuming
// there relies on: if the seed already contained a later record, the tailer re-applies it; if
// it is missing an earlier one, nothing ever supplies it.
func TestThePositionClaimHoldsOnEngineWrittenState(t *testing.T) {
	p, store := engineFixture(t)
	cp := parkingWorkload(t)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// A first cohort, then the snapshot: this is the seed.
	const first = 8
	for range first {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	view := store.ReadView()
	defer view.Close()
	seeded, err := Build(view)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want, err := view.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if seeded.Position() != want || want == 0 {
		t.Fatalf("Position() = %d, want the view's %d (and not genesis)", seeded.Position(), want)
	}
	nodesAtSeed := seeded.Ordinals.Len()
	if nodesAtSeed == 0 {
		t.Fatal("the engine parked nothing, so this measures the position of an empty graph")
	}

	// A second cohort arrives *after* the snapshot. The store moves on; the view does not.
	const second = 8
	for range second {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	live := store.ReadView()
	defer live.Close()
	now, err := live.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if now <= want {
		t.Fatalf("the log did not advance: %d then %d", want, now)
	}

	// The seeded projection is unchanged — it is a snapshot, not a view that drifts — and it
	// is exactly as stale as the two positions say.
	if got := seeded.Ordinals.Len(); got != nodesAtSeed {
		t.Errorf("the seeded graph grew from %d nodes to %d; it is not a snapshot", nodesAtSeed, got)
	}
	if seeded.Position() != want {
		t.Errorf("the seeded graph's position moved to %d", seeded.Position())
	}
	// And none of the second cohort's nodes are in it. Their keys are all above every key
	// the seed holds, because the engine mints keys from an increasing counter — which is
	// also why the tailer can append new nodes at the end of the ordinal map without
	// renumbering, and is worth pinning here rather than assumed later.
	highest := seeded.Ordinals.Key(uint32(nodesAtSeed - 1))
	newer := 0
	if err := live.ActiveElementInstances(func(key uint64, _ *model.ElementInstanceValue) error {
		if key > highest {
			newer++
			if _, ok := seeded.Ordinals.Ordinal(key); ok {
				t.Errorf("key %#x arrived after the snapshot and is in it anyway", key)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if newer == 0 {
		t.Error("no element instance arrived after the snapshot, so the exclusion is untested")
	}
	t.Logf("seed at position %d with %d nodes; store now at %d with %d newer element instances",
		want, nodesAtSeed, now, newer)
}
