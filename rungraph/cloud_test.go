package rungraph

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// W3 of ADR-0404 §5: *"The cloud is an aggregation, not a clustering."*
//
// The plan has W3 depending on W2 **and a renderer beyond SVG**, and the renderer is a
// frontend decision nobody has taken. What does not depend on it is the number the renderer
// would draw, and building that first settles a question that would otherwise be decided
// implicitly inside a renderer — the most expensive place to correct it: **which axis the
// cloud groups by.** Three candidates exist and they are different questions:
//
//   - the **component** (W2) answers reachability — what is connected to what;
//   - the **instance** is a field on the value the store already hands over, so it needs no
//     union-find at all;
//   - the **cloud** answers sameness — how much of this graph is of one kind.
//
// §5 names five dimensions the nodes "already carry": definition, element, worker, incident
// state, time bucket. Measured against `model.ElementInstanceValue`, the node carries
// **two** of them — `ProcessDefKey` and `ElementId` — plus `BpmnElementType`, which is a
// refinement of the second. Worker lives on a job, incident state on an incident, and there
// is no timestamp on the value at all, so those three are a join and a per-node array of
// 4 bytes (420 MB at 110 M nodes, the same budget unit whose doubling §5's own membership
// decision refused). This package therefore offers the free axes and nothing else; the rest
// is a cost for the record to weigh, not a default to ship.

// TestACloudIsAnAggregationAndNeedsNoGraph is the finding that makes W3 affordable on
// installations where §9's budget refuses the walk: a group-by is not a graph operation. It
// needs one scan of the store and a map whose size is the number of *cells*, not of nodes —
// no ordinals, no CSR, no union-find, none of the 2,448 MB the structure costs.
func TestACloudIsAnAggregationAndNeedsNoGraph(t *testing.T) {
	// Ten thousand nodes over three definitions.
	src := &fakeSource{position: 7}
	for i := range 10_000 {
		src.keys = append(src.keys, uint64(i+1))
		src.values = append(src.values, &model.ElementInstanceValue{
			ProcessDefKey:      uint64(i%3 + 1),
			ProcessInstanceKey: uint64(i + 1),
		})
	}
	c, err := BuildCloud(src, ByDefinition)
	if err != nil {
		t.Fatalf("BuildCloud: %v", err)
	}
	if c.Len() != 3 {
		t.Errorf("Len() = %d, want 3 — a cloud is sized by its cells, not by its nodes", c.Len())
	}
	if c.Nodes() != 10_000 {
		t.Errorf("Nodes() = %d, want 10000", c.Nodes())
	}
	total := 0
	for _, cell := range c.Cells() {
		total += cell.Nodes
	}
	if total != c.Nodes() {
		t.Errorf("cells sum to %d but the cloud says %d nodes", total, c.Nodes())
	}
}

// TestACloudSaysWhatItIsDenseInAndAsOfWhen is §5's own rendering rule applied to the data
// rather than to the picture: *"a density without a named reference is a picture of nothing
// … it must say dense in what, and as of when."* A renderer cannot add either of those
// honestly if the number does not carry them, so they are fields and not documentation.
func TestACloudSaysWhatItIsDenseInAndAsOfWhen(t *testing.T) {
	src := &fakeSource{
		position: 4711,
		keys:     []uint64{10, 20},
		values: []*model.ElementInstanceValue{
			{ProcessDefKey: 1, ElementId: 1},
			{ProcessDefKey: 1, ElementId: 2},
		},
	}
	c, err := BuildCloud(src, ByDefinition)
	if err != nil {
		t.Fatalf("BuildCloud: %v", err)
	}
	if c.Axis != ByDefinition {
		t.Errorf("Axis = %v, want ByDefinition", c.Axis)
	}
	if c.Position != 4711 {
		t.Errorf("Position = %d, want the source's 4711", c.Position)
	}
}

// TestACloudRefusesASourceThatCannotStateItsPosition keeps the "as of" from being absent
// rather than wrong, for the same reason W4 made it required of a graph: a cloud claiming
// "as of 0" is indistinguishable from one genuinely at genesis, and a claim nobody can
// check is worse than no claim.
func TestACloudRefusesASourceThatCannotStateItsPosition(t *testing.T) {
	src := &fakeSource{positionErr: true, keys: []uint64{1}, values: []*model.ElementInstanceValue{{ProcessDefKey: 1}}}
	if _, err := BuildCloud(src, ByDefinition); err == nil {
		t.Fatal("BuildCloud published a density from a source that could not say when it is true")
	}
}

// TestAnElementCellIsScopedToItsDefinition is a correctness property, not a convenience.
// ElementId is an **index into the compiled graph**, not a global identifier, so element 1
// of one process and element 1 of another are different elements that happen to share a
// number. Grouping by the number alone would merge them and report a density over a cell
// that does not exist.
func TestAnElementCellIsScopedToItsDefinition(t *testing.T) {
	src := &fakeSource{
		position: 1,
		keys:     []uint64{1, 2},
		values: []*model.ElementInstanceValue{
			{ProcessDefKey: 100, ElementId: 1},
			{ProcessDefKey: 200, ElementId: 1},
		},
	}
	c, err := BuildCloud(src, ByElement)
	if err != nil {
		t.Fatalf("BuildCloud: %v", err)
	}
	if c.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 — element 1 of two definitions is two elements", c.Len())
	}
	for _, cell := range c.Cells() {
		if cell.Nodes != 1 {
			t.Errorf("cell %+v holds %d nodes, want 1", cell, cell.Nodes)
		}
		if cell.Definition != 100 && cell.Definition != 200 {
			t.Errorf("cell names definition %d, want 100 or 200", cell.Definition)
		}
	}
}

// TestCellsComeBackInAStableOrder is what makes a saved view keep its meaning. §5 chose
// components over a Louvain partition partly because they are "stable across rebuilds,
// which is what makes W3's saved views keep their meaning" — a cloud whose cells reshuffle
// between rebuilds breaks the same promise, and a Go map's iteration order is deliberately
// random, so the order has to be imposed rather than inherited.
func TestCellsComeBackInAStableOrder(t *testing.T) {
	src := &fakeSource{position: 1}
	for i, def := range []uint64{9, 3, 7, 3, 9, 1} {
		src.keys = append(src.keys, uint64(i+1))
		src.values = append(src.values, &model.ElementInstanceValue{ProcessDefKey: def})
	}
	first, err := BuildCloud(src, ByDefinition)
	if err != nil {
		t.Fatalf("BuildCloud: %v", err)
	}
	want := []uint64{1, 3, 7, 9}
	got := make([]uint64, 0, len(want))
	for _, c := range first.Cells() {
		got = append(got, c.Definition)
	}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("cells = %v, want %v ascending", got, want)
		}
	}
	// And again, over the same source: same order, same counts.
	again, err := BuildCloud(src, ByDefinition)
	if err != nil {
		t.Fatalf("BuildCloud (again): %v", err)
	}
	for i, c := range again.Cells() {
		if c != first.Cells()[i] {
			t.Errorf("cell %d = %+v on the second build, %+v on the first", i, c, first.Cells()[i])
		}
	}
}

// TestAnEmptyStoreIsACloudWithNoCells is the fresh-server case, and it must still say when
// it is true: a renderer showing "nothing, as of position 99" is honest, one showing
// "nothing" is not distinguishable from a failed read.
func TestAnEmptyStoreIsACloudWithNoCells(t *testing.T) {
	c, err := BuildCloud(&fakeSource{position: 99}, ByDefinition)
	if err != nil {
		t.Fatalf("BuildCloud: %v", err)
	}
	if c.Len() != 0 || c.Nodes() != 0 {
		t.Errorf("Len() = %d, Nodes() = %d, want 0 and 0", c.Len(), c.Nodes())
	}
	if c.Position != 99 {
		t.Errorf("Position = %d, want 99 — an empty cloud is still as of a moment", c.Position)
	}
}

// TestAFailedScanIsNotAThinCloud keeps a partial read from rendering as a real density. A
// cloud is a count, so a scan that stopped early produces numbers that are all plausible
// and all too small, with nothing in the shape of the answer to say so.
func TestAFailedScanIsNotAThinCloud(t *testing.T) {
	src := &fakeSource{
		position: 1,
		failAt:   2,
		keys:     []uint64{1, 2, 3},
		values: []*model.ElementInstanceValue{
			{ProcessDefKey: 1}, {ProcessDefKey: 1}, {ProcessDefKey: 1},
		},
	}
	if _, err := BuildCloud(src, ByDefinition); err == nil {
		t.Fatal("BuildCloud returned a density from a scan that failed part-way")
	}
}

// TestAnUnknownAxisIsRefused keeps a zero value or a future constant from silently grouping
// by whatever the switch's default happens to be — which would report a density over an
// axis the caller did not ask for.
func TestAnUnknownAxisIsRefused(t *testing.T) {
	src := &fakeSource{position: 1, keys: []uint64{1}, values: []*model.ElementInstanceValue{{ProcessDefKey: 1}}}
	if _, err := BuildCloud(src, Axis(99)); err == nil {
		t.Fatal("BuildCloud accepted an axis it does not know")
	}
}

// TestTheElementTypeAxisCountsAcrossDefinitions covers the third free axis, which answers a
// different question from the other two: not "which process is this" but "what kind of work
// is the estate sitting in" — service tasks against timers against user tasks — and it is
// the one axis that is deliberately *not* scoped to a definition, because the comparison
// across definitions is the point.
func TestTheElementTypeAxisCountsAcrossDefinitions(t *testing.T) {
	src := &fakeSource{
		position: 5,
		keys:     []uint64{1, 2, 3},
		values: []*model.ElementInstanceValue{
			{ProcessDefKey: 100, BpmnElementType: 7},
			{ProcessDefKey: 200, BpmnElementType: 7},
			{ProcessDefKey: 200, BpmnElementType: 3},
		},
	}
	c, err := BuildCloud(src, ByElementType)
	if err != nil {
		t.Fatalf("BuildCloud: %v", err)
	}
	if c.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (types 3 and 7)", c.Len())
	}
	got := map[uint8]int{}
	for _, cell := range c.Cells() {
		got[cell.ElementType] = cell.Nodes
		if cell.Definition != 0 {
			t.Errorf("cell names definition %d; this axis crosses definitions", cell.Definition)
		}
	}
	if got[7] != 2 || got[3] != 1 {
		t.Errorf("counts = %v, want type 7 twice and type 3 once", got)
	}
}

// TestAnAxisNamesItself is not cosmetic: the axis appears in the refusal a human reads, and
// "Axis(99)" next to "definition" is the difference between a message that says what went
// wrong and one that says a number.
func TestAnAxisNamesItself(t *testing.T) {
	for _, c := range []struct {
		a    Axis
		want string
	}{
		{ByDefinition, "definition"},
		{ByElement, "element"},
		{ByElementType, "element type"},
		{Axis(0), "Axis(0)"},
	} {
		if got := c.a.String(); got != c.want {
			t.Errorf("Axis(%d).String() = %q, want %q", uint8(c.a), got, c.want)
		}
	}
}

// TestTheCloudAgreesWithTheStoreItCounted is the acceptance measurement, and it checks the
// aggregation against two things it did not come from: the store's own scan, and the
// projection built from the same view.
//
// The second is the point of the whole design. A cloud built without ordinals, without a CSR
// and without a component pass must nonetheless count exactly the nodes the projection
// holds — otherwise "the cloud needs no graph" would be a saving bought with a different
// answer.
func TestTheCloudAgreesWithTheStoreItCounted(t *testing.T) {
	p, store := engineFixture(t)
	cp := parkingWorkload(t)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	const instances = 5
	for range instances {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	view := store.ReadView()
	defer view.Close()

	// Ground truth, counted from the store by hand.
	wantNodes := 0
	perElement := map[int32]int{}
	if err := view.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
		wantNodes++
		perElement[v.ElementId]++
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if wantNodes == 0 {
		t.Fatal("the engine parked nothing, so the counts below are all zero")
	}

	byDef, err := BuildCloud(view, ByDefinition)
	if err != nil {
		t.Fatalf("BuildCloud(ByDefinition): %v", err)
	}
	if byDef.Nodes() != wantNodes {
		t.Errorf("cloud counted %d nodes, the store holds %d", byDef.Nodes(), wantNodes)
	}
	if byDef.Len() != 1 {
		t.Errorf("Len() = %d, want 1 — one definition is deployed", byDef.Len())
	}
	if got := byDef.Cells()[0].Definition; got != cp.Key {
		t.Errorf("the cell names definition %d, want the deployed %d", got, cp.Key)
	}

	byElement, err := BuildCloud(view, ByElement)
	if err != nil {
		t.Fatalf("BuildCloud(ByElement): %v", err)
	}
	if byElement.Len() != len(perElement) {
		t.Errorf("Len() = %d, want %d distinct elements", byElement.Len(), len(perElement))
	}
	for _, cell := range byElement.Cells() {
		if want := perElement[cell.Element]; cell.Nodes != want {
			t.Errorf("element %d: cloud says %d nodes, the store says %d", cell.Element, cell.Nodes, want)
		}
	}
	if byElement.Len() < 2 {
		t.Error("the workload parked one kind of element only, so the element axis is untested")
	}

	// And against the projection built from the same view: same nodes, same position, no
	// structure needed to get there.
	g, err := Build(view)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if byDef.Nodes() != g.Ordinals.Len() {
		t.Errorf("cloud counted %d nodes, the projection holds %d", byDef.Nodes(), g.Ordinals.Len())
	}
	if byDef.Position != g.Position() {
		t.Errorf("cloud is as of %d, the projection as of %d", byDef.Position, g.Position())
	}
	t.Logf("%d nodes as of position %d: 1 definition cell, %d element cells",
		byDef.Nodes(), byDef.Position, byElement.Len())
}
