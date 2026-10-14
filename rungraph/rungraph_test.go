package rungraph

import (
	"errors"
	"testing"

	"github.com/pblumer/atlas/model"
)

// W1 of ADR-0404: the ordinal map and the CSR, built from a consistent view of the
// state store.
//
// Two things in the record are load-bearing and neither is obvious, so both are pinned
// here rather than left to the implementation to get right by accident.
//
// **Ordinals are assigned in key order** (§3, §10). Not for tidiness: a component's
// nodes are then contiguous in ordinal space, so its adjacency lists are contiguous in
// the targets array, so a walk of one component reads a few sequential pages instead of
// scattering. §10's measurement is what makes that matter — a scattered read under a
// memory cap, without MADV_RANDOM, was 74× slower than a sequential one. The key
// encoding gives it for free: keys are written big-endian (`state/keys.go`,
// appendBE64), so a prefix scan already arrives ascending, and the ordinal is the scan
// position. That is a property of the store's key layout, so it is asserted rather than
// assumed.
//
// **The build is all-or-nothing** (§"the build is all-or-nothing"). A partially built
// CSR that answers is worse than none, because its answers are wrong in a way no reader
// can see. A failed build must leave nothing behind for anyone to read.

// fakeSource is a scan over element instances handed in directly, in the order the
// store would yield them. It is not a convenience: the real source is a Pebble prefix
// scan, and what these tests are about is what the builder does with an ordered stream,
// not how the stream is produced.
type fakeSource struct {
	keys   []uint64
	values []*model.ElementInstanceValue
	failAt int // 1-based index to fail on; 0 never fails
	// position is what the snapshot says it is as of (§4); positionErr makes the read fail,
	// which a build has to refuse rather than paper over.
	position    uint64
	positionErr bool
}

func (f *fakeSource) LastAppliedPosition() (uint64, error) {
	if f.positionErr {
		return 0, errors.New("position unavailable")
	}
	return f.position, nil
}

func (f *fakeSource) ActiveElementInstances(fn func(key uint64, v *model.ElementInstanceValue) error) error {
	for i := range f.keys {
		if f.failAt != 0 && i+1 == f.failAt {
			return errors.New("scan failed")
		}
		if err := fn(f.keys[i], f.values[i]); err != nil {
			return err
		}
	}
	return nil
}

// el builds one element instance with the three key-to-key references that are real
// edges between element instances. Deliberately not TokenID or ParentTokenID: those are
// a token id space, not element keys, so a token lineage is a different relation and
// drawing it as an element edge would join nodes that are not adjacent.
func el(instance, scope, attachedTo, gateway uint64) *model.ElementInstanceValue {
	return &model.ElementInstanceValue{
		ProcessInstanceKey: instance,
		FlowScopeKey:       scope,
		AttachedToKey:      attachedTo,
		EventGatewayKey:    gateway,
	}
}

// TestOrdinalsAreTheScanPosition pins §3's map onto the one thing that makes §10's
// locality claim possible. The map is the sorted key array and the ordinal is the
// index, so there is no second structure to keep consistent and the inverse is free.
func TestOrdinalsAreTheScanPosition(t *testing.T) {
	// Sparse, ascending — what `[16 bit partition][48 bit counter]` keys look like.
	src := &fakeSource{
		keys: []uint64{0x0001_000000000005, 0x0001_000000000009, 0x0002_000000000001},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0), el(0, 0, 0, 0), el(0, 0, 0, 0),
		},
	}

	ords, err := BuildOrdinals(src)
	if err != nil {
		t.Fatalf("BuildOrdinals: %v", err)
	}
	if got := ords.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	for want, key := range src.keys {
		got, ok := ords.Ordinal(key)
		if !ok {
			t.Fatalf("key %#x has no ordinal", key)
		}
		if int(got) != want {
			t.Errorf("Ordinal(%#x) = %d, want %d — ordinals are the ascending scan position", key, got, want)
		}
		if back := ords.Key(got); back != key {
			t.Errorf("Key(%d) = %#x, want %#x — the inverse must round-trip", got, back, key)
		}
	}
	if _, ok := ords.Ordinal(0xdead); ok {
		t.Error("a key that was never scanned has an ordinal")
	}
}

// TestOrdinalsRefuseAnOutOfOrderScan is the assertion that makes the locality property
// checkable instead of hoped for. The builder's correctness rests on the store yielding
// keys ascending; if that ever stops being true — a key layout change, a different
// column family, a merged iterator — the build must fail loudly rather than produce a
// map whose ordinals no longer carry locality and whose binary search is wrong.
func TestOrdinalsRefuseAnOutOfOrderScan(t *testing.T) {
	src := &fakeSource{
		keys:   []uint64{9, 5},
		values: []*model.ElementInstanceValue{el(0, 0, 0, 0), el(0, 0, 0, 0)},
	}
	if _, err := BuildOrdinals(src); err == nil {
		t.Fatal("BuildOrdinals accepted a descending scan; the ordinal map would be silently wrong")
	}
}

// TestCSRHoldsEveryEdgeInBothEndpoints is §2's undirected layout. Impact analysis walks
// both ways — "what does this reach" and "what reaches this" — so every edge lives in
// both endpoints' lists. That doubling is the reason the record's own size table was
// corrected upward, and a build that stored each edge once would look right in a
// one-directional test and be wrong for the question the graph exists to answer.
func TestCSRHoldsEveryEdgeInBothEndpoints(t *testing.T) {
	// Two element instances, the second scoped under the first: one edge.
	src := &fakeSource{
		keys: []uint64{10, 20},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0),
			el(0, 10, 0, 0), // FlowScopeKey → 10
		},
	}
	g, err := Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	a, _ := g.Ordinals.Ordinal(10)
	b, _ := g.Ordinals.Ordinal(20)

	if got := g.Neighbours(a); len(got) != 1 || got[0] != b {
		t.Errorf("Neighbours(%d) = %v, want [%d]", a, got, b)
	}
	if got := g.Neighbours(b); len(got) != 1 || got[0] != a {
		t.Errorf("Neighbours(%d) = %v, want [%d] — an undirected CSR holds the edge at both ends", b, got, a)
	}
	if g.Edges != 1 {
		t.Errorf("Edges = %d, want 1 — the count is edges, not list entries", g.Edges)
	}
}

// TestAReferenceToSomethingThatIsNotANodeIsNotAnEdge is the case a naive build gets
// wrong in a way that corrupts silently. Three of the four references on an element
// instance do not always name another element instance: FlowScopeKey is 0 at the process
// root, AttachedToKey and EventGatewayKey are 0 on almost every element, and
// ProcessInstanceKey names a process instance, which lives in a different column family
// and is not in this ordinal map at all.
//
// Emitting an edge for any of those would either index out of range or, worse, land on
// whichever element happens to hold that ordinal — an edge between two unrelated
// instances, which is exactly the kind of wrong answer no reader can see.
func TestAReferenceToSomethingThatIsNotANodeIsNotAnEdge(t *testing.T) {
	src := &fakeSource{
		keys: []uint64{10, 20},
		values: []*model.ElementInstanceValue{
			// A root element: no scope, no host, no gateway, and a process instance key
			// that is not an element key.
			el(999, 0, 0, 0),
			// Scoped under a key that was never scanned — a dangling reference, which a
			// retention boundary can produce.
			el(999, 777, 0, 0),
		},
	}
	g, err := Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Edges != 0 {
		t.Errorf("Edges = %d, want 0 — none of these references names a node in this graph", g.Edges)
	}
	for ord := range uint32(g.Ordinals.Len()) {
		if got := g.Neighbours(ord); len(got) != 0 {
			t.Errorf("Neighbours(%d) = %v, want empty", ord, got)
		}
	}
}

// TestAllThreeReferenceKindsBecomeEdges checks that the edge rule is not accidentally
// scope-only. A boundary event and its host, and a catch event and its event gateway,
// are adjacency the run graph needs: they are how an interrupting boundary reaches the
// activity it terminates and how a race group finds its members.
func TestAllThreeReferenceKindsBecomeEdges(t *testing.T) {
	src := &fakeSource{
		keys: []uint64{10, 20, 30, 40},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0),
			el(0, 10, 0, 0), // scope → 10
			el(0, 0, 10, 0), // boundary attached to 10
			el(0, 0, 0, 10), // armed by gateway 10
		},
	}
	g, err := Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Edges != 3 {
		t.Fatalf("Edges = %d, want 3 — scope, boundary host and event gateway are each an edge", g.Edges)
	}
	hub, _ := g.Ordinals.Ordinal(10)
	if got := g.Neighbours(hub); len(got) != 3 {
		t.Errorf("Neighbours of the hub = %v, want all three", got)
	}
}

// TestOneElementReferencingTheSameNodeTwiceIsTwoEdges states a decision rather than
// discovering one. An element instance can name the same neighbour through two
// references — a boundary event whose host is also its scope — and the build keeps both,
// because the offsets array is built from a count and a fill that must agree exactly. A
// fill that skipped a duplicate the count included would leave a zero in the targets
// array, which reads as ordinal 0: a real node, and the wrong one.
func TestOneElementReferencingTheSameNodeTwiceIsTwoEdges(t *testing.T) {
	src := &fakeSource{
		keys: []uint64{10, 20},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0),
			el(0, 10, 10, 0), // scope and host are the same node
		},
	}
	g, err := Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Edges != 2 {
		t.Errorf("Edges = %d, want 2 — two references are two edges, and count and fill must agree", g.Edges)
	}
	a, _ := g.Ordinals.Ordinal(10)
	if got := g.Neighbours(a); len(got) != 2 {
		t.Errorf("Neighbours(%d) = %v, want two entries", a, got)
	}
}

// TestAFailedBuildPublishesNothing is the all-or-nothing rule. The scan fails partway,
// which is what a closed view or a corrupt record does, and the caller must get an error
// and no graph rather than a graph of however much was read.
func TestAFailedBuildPublishesNothing(t *testing.T) {
	src := &fakeSource{
		keys: []uint64{10, 20, 30},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0), el(0, 10, 0, 0), el(0, 20, 0, 0),
		},
		failAt: 3,
	}
	g, err := Build(src)
	if err == nil {
		t.Fatal("Build returned no error on a failed scan")
	}
	if g != nil {
		t.Errorf("Build returned a graph alongside the error: %+v — a partial CSR answers wrongly and invisibly", g)
	}
}

// TestAnEmptyStoreBuildsAnEmptyGraph is the cold start. A server nobody has run anything
// on has no run graph, and that is a graph of nothing rather than an error — the same
// distinction the starmap makes for an unmodelled landscape.
func TestAnEmptyStoreBuildsAnEmptyGraph(t *testing.T) {
	g, err := Build(&fakeSource{})
	if err != nil {
		t.Fatalf("Build on an empty store: %v", err)
	}
	if g.Ordinals.Len() != 0 || g.Edges != 0 {
		t.Errorf("empty store built %d nodes and %d edges", g.Ordinals.Len(), g.Edges)
	}
	// The offsets array is n+1 long even at n=0, so a walk over no nodes is a loop that
	// runs zero times rather than an index error waiting for the first caller.
	if len(g.Offsets) != 1 || g.Offsets[0] != 0 {
		t.Errorf("Offsets = %v, want [0] — (n+1) entries even when n is 0", g.Offsets)
	}
}

// TestAComponentsLabelIsItsSmallestOrdinal pins the invariant [Graph.Locality] rests on.
//
// Locality needs each component's ordinal range, and it finds the low end for free by
// taking the label as the minimum — which is only true because the union always points
// the larger label at the smaller. If that direction ever flips, the metric would report
// ranges that are wrong without being obviously wrong, so the invariant is asserted here
// rather than left as a comment beside the code that assumes it.
func TestAComponentsLabelIsItsSmallestOrdinal(t *testing.T) {
	// Three components, deliberately built so the *later* key is scanned first within a
	// component: 30 is scoped under 40, so the union is asked to join a larger ordinal to
	// a smaller one in the direction that would break a naive implementation.
	src := &fakeSource{
		keys: []uint64{10, 20, 30, 40, 50},
		values: []*model.ElementInstanceValue{
			el(0, 0, 0, 0),  // 10, alone
			el(0, 0, 0, 0),  // 20 ─┐
			el(0, 40, 0, 0), // 30 → 40
			el(0, 20, 0, 0), // 40 → 20, joining {20,30,40}
			el(0, 0, 0, 0),  // 50, alone
		},
	}
	g, err := Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	labels := g.Components()

	// Group by label and check each label is its group's minimum.
	members := map[uint32][]uint32{}
	for ord, label := range labels {
		members[label] = append(members[label], uint32(ord))
	}
	if len(members) != 3 {
		t.Fatalf("components = %d, want 3: labels = %v", len(members), labels)
	}
	for label, group := range members {
		smallest := group[0]
		for _, ord := range group {
			if ord < smallest {
				smallest = ord
			}
		}
		if label != smallest {
			t.Errorf("component %v has label %d, want its smallest ordinal %d — Locality reads the label as the low end of the range",
				group, label, smallest)
		}
	}

	// And the metric agrees: {20,30,40} spans 3, the two singletons span 1 each, over 5
	// nodes.
	span, components := g.Locality()
	if components != 3 {
		t.Errorf("Locality components = %d, want 3", components)
	}
	if want := 5.0 / 5.0; span != want {
		t.Errorf("Locality span = %.4f, want %.4f — three contiguous ranges over five nodes", span, want)
	}
}

// shrinkingSource yields every key on the first scan and one fewer on each scan after,
// which is what an inconsistent view would look like: the ordinal pass sees a node the
// count pass no longer does.
type shrinkingSource struct {
	keys  []uint64
	scans int
}

func (s *shrinkingSource) LastAppliedPosition() (uint64, error) { return 1, nil }

func (s *shrinkingSource) ActiveElementInstances(fn func(key uint64, v *model.ElementInstanceValue) error) error {
	n := len(s.keys) - s.scans
	s.scans++
	if n < 0 {
		n = 0
	}
	for i := range n {
		if err := fn(s.keys[i], el(0, 0, 0, 0)); err != nil {
			return err
		}
	}
	return nil
}

// TestAnInconsistentViewIsRefusedRatherThanBuiltFrom covers what a Pebble snapshot makes
// impossible and what the code must not trust anyway.
//
// A build reads the view three times — ordinals, count, fill — and every offset it
// computes assumes the three agree. If a later pass sees fewer element instances, the
// graph that comes out is missing whole components while looking complete: its offsets are
// internally consistent, its lists are well formed, and the nodes it dropped simply have
// no neighbours. That is the invisible wrongness the all-or-nothing rule exists for, so it
// is an error rather than a smaller graph.
func TestAnInconsistentViewIsRefusedRatherThanBuiltFrom(t *testing.T) {
	g, err := Build(&shrinkingSource{keys: []uint64{10, 20, 30}})
	if err == nil {
		t.Fatal("Build accepted a view that shrank between passes")
	}
	if g != nil {
		t.Errorf("Build returned a graph alongside the error: %+v", g)
	}
}
