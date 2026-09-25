// Package rungraph builds the run graph's CSR projection: the second surface of
// [ADR-0404], never mixed with the structural starmap and never authoritative.
//
// The run graph is a forest. Millions of components of about thirty nodes each — an
// element instance and the scopes, boundary events and race groups around it — with an
// average degree of 4.8. That topology is what decides the layout: there is nothing deep
// to traverse, so a whole-graph pass is millions of independent tiny walks, and what
// makes those walks affordable is not cleverness but locality.
//
// Three rules from the record are in this code rather than in prose around it:
//
//   - **The projection is disposable.** It is never a source of truth, never backed up,
//     and a lost or stale one is rebuilt. That is what keeps it from being the second
//     store ADR-0179 refused.
//   - **The build is all-or-nothing.** A partially built CSR answers wrongly in a way no
//     reader can see, so a failed build returns nothing at all and leaves whatever
//     projection already existed in place.
//   - **Ordinals are the scan position.** See [BuildOrdinals].
//
// [ADR-0404]: ../docs/adr/0404-the-whole-graph-can-be-walked.md
package rungraph

import (
	"fmt"
	"math"
	"sort"

	"github.com/pblumer/atlas/model"
)

// Source is the consistent view a build reads from: ADR-0404 §4 seeds from a snapshot of
// the state store rather than from genesis, because the exporter's cursor is valid only
// within one process run and a compacted log no longer holds genesis at all (ADR-0131).
//
// `*state.ReadView` satisfies this, which is the intended caller: take the view on the
// run loop that owns the store, build off the loop, close the view when the build is done
// (ADR-0239, ADR-0382). The interface is narrow on purpose — a builder that could reach
// the live store would be a builder that could read two halves of different states.
type Source interface {
	ActiveElementInstances(fn func(key uint64, v *model.ElementInstanceValue) error) error
	// LastAppliedPosition is the log position the snapshot is as of — the highest position
	// folded into the state it shows. §4 keeps the projection current "from the tailer,
	// starting at the snapshot's position", so a source that cannot say which position that
	// is cannot seed a projection anybody could resume from; and §5 forbids a picture that
	// cannot say as of when it is true. It is part of the interface rather than an optional
	// assertion for that reason: a graph claiming "as of 0" would be indistinguishable from
	// one genuinely at genesis, and a claim nobody can check is worse than no claim.
	LastAppliedPosition() (uint64, error)
}

// Ordinals is the persisted key→ordinal map ADR-0404 §3 requires.
//
// It is the sorted key array and nothing else: the ordinal *is* the index. That removes
// the second structure a hash map would be — one that has to be kept consistent with the
// keys and rebuilt identically on every restart — and it makes the inverse free, which
// matters because a walk's result is ordinals and a reader needs keys.
//
// §3 insists the map is persisted rather than rederived: without that, every restart
// renumbers every node and any saved finding, exported picture or bookmarked walk refers
// to nothing. A flat ascending uint64 array is the persistable shape, and it is also the
// mmap-able one §10's dial wants.
type Ordinals struct {
	// Position is the log position the snapshot these keys came from was as of (§4). The
	// map is the persisted half of the projection, so it is the half that has to record
	// when it is true: one persisted at an unknown position cannot be reconciled with a
	// log, and a tailer could only resume from genesis.
	Position uint64
	keys     []uint64 // ascending; index is the ordinal
}

// Len is how many nodes the map holds.
func (o *Ordinals) Len() int { return len(o.keys) }

// Ordinal is the dense id for a key, and reports whether the key is a node at all. The
// second return is not ceremony: three of the four references on an element instance can
// name something that is not in this graph, and an edge invented for one of those would
// land on an unrelated node.
func (o *Ordinals) Ordinal(key uint64) (uint32, bool) {
	i := sort.Search(len(o.keys), func(i int) bool { return o.keys[i] >= key })
	if i == len(o.keys) || o.keys[i] != key {
		return 0, false
	}
	return uint32(i), true
}

// Key is the inverse. It panics on an out-of-range ordinal, which is a programming error
// rather than a data condition: every ordinal in a graph came from this map.
func (o *Ordinals) Key(ordinal uint32) uint64 { return o.keys[ordinal] }

// BuildOrdinals assigns an ordinal to every element instance, in key order.
//
// "In key order" is the whole point and it costs nothing here. Keys are written
// big-endian (`state/keys.go`, appendBE64), so a prefix scan of the column family
// arrives ascending and the ordinal is simply the scan position. What that buys is
// §10's locality: keys are minted in increasing order, so one instance's element
// instances are numerically close, so a component's nodes are contiguous in ordinal
// space, so its adjacency lists are contiguous in the targets array. A walk of one
// component then reads a few sequential pages instead of scattering — and §10's
// measurement is why that is worth designing for rather than hoping for: a scattered
// read under a memory cap, without MADV_RANDOM, ran 74× slower than a sequential one.
//
// Because the whole property rests on the scan being ascending, this refuses a scan that
// is not. A descending or unordered stream would produce a map whose binary search is
// wrong and whose ordinals carry no locality, and it would do so silently.
func BuildOrdinals(src Source) (*Ordinals, error) {
	// The position first, and a failure here fails the build: §4 resumes the tailer at the
	// snapshot's position, so an ordinal map persisted at an unknown one is a map nobody can
	// reconcile with a log.
	pos, err := src.LastAppliedPosition()
	if err != nil {
		return nil, fmt.Errorf("rungraph: read the snapshot's log position: %w", err)
	}
	var keys []uint64
	var last uint64
	var seen bool
	err = src.ActiveElementInstances(func(key uint64, _ *model.ElementInstanceValue) error {
		if seen && key <= last {
			return fmt.Errorf("element instance scan is not ascending: %#x after %#x", key, last)
		}
		last, seen = key, true
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Ordinals{Position: pos, keys: keys}, nil
}

// Graph is the CSR projection: an offset per node and a concatenated array of neighbour
// ordinals (ADR-0404 §2). Two flat, pointer-free arrays, which is exactly what lets §10
// decide *where* they live — heap, or mmapped files — without changing anything about the
// structure or the walk.
type Graph struct {
	// Ordinals is the map the arrays below are indexed by. Held rather than passed
	// alongside, because an offsets array and an ordinal map from different builds are a
	// graph whose every answer is plausible and wrong.
	Ordinals *Ordinals
	// Offsets has (n+1) entries: node i's neighbours are Targets[Offsets[i]:Offsets[i+1]].
	// The extra entry is what makes the last node's slice expressible without a special
	// case, and it is present even at n=0.
	Offsets []uint32
	// Targets holds every edge **twice**, once in each endpoint's list. An undirected CSR
	// is what impact analysis needs: "what does this reach" and "what reaches this" are
	// both asked, and a one-directional store answers only one of them. This doubling is
	// what corrected the record's own size table upward — 2 × m, not m.
	Targets []uint32
	// Edges counts edges, not entries in Targets. len(Targets) is 2 × Edges.
	Edges int
}

// Position is the log position this projection is as of: the highest position folded into
// the snapshot it was seeded from (§4). It is what a tailer resumes at, and what a picture
// drawn from this graph has to name as its "as of" (§5).
//
// Read through the ordinal map rather than copied beside it, for the reason the map itself
// is held rather than passed alongside: two fields that can disagree are a graph whose every
// answer is plausible and wrong.
func (g *Graph) Position() uint64 { return g.Ordinals.Position }

// Neighbours is node ord's adjacency list. The slice aliases Targets: a walk reads it and
// does not own it.
func (g *Graph) Neighbours(ord uint32) []uint32 {
	return g.Targets[g.Offsets[ord]:g.Offsets[ord+1]]
}

// Build projects the source into a CSR.
//
// Three passes over the view, which is the honest cost and not an oversight. The ordinals
// have to be complete before any edge can be counted, because an edge's far endpoint may
// have a higher key than the element that names it; and the offsets have to be complete
// before any target can be placed, because CSR is a packed layout with no room to grow a
// list. Count-then-fill is what every CSR build does, here and in Neo4j's GDS.
//
// It returns (nil, err) on any failure, never a partial graph — see the package comment.
func Build(src Source) (*Graph, error) {
	ords, err := BuildOrdinals(src)
	if err != nil {
		return nil, err
	}
	n := ords.Len()

	// Pass 2: degree per node. Both endpoints, since the layout is undirected.
	degree := make([]uint32, n)
	edges := 0
	seen, err := eachEdge(src, ords, func(from, to uint32) {
		degree[from]++
		degree[to]++
		edges++
	})
	if err != nil {
		return nil, err
	}
	// eachEdge reports an unknown key as an error, so a pass that saw *more* keys than the
	// ordinal pass cannot get here. One that saw fewer can, and it would build a graph
	// quietly missing whole components — so the count is checked rather than assumed. On a
	// Pebble snapshot this cannot differ; if it ever does, the view was not consistent and
	// the caller must not get a graph.
	if seen != n {
		return nil, fmt.Errorf("count pass saw %d element instances, ordinal pass saw %d: the view is not consistent", seen, n)
	}

	// Prefix sum. Offsets[i] ends up as the start of node i's list, and the running
	// cursor below consumes it — which is why fill re-derives the starts into a copy
	// rather than mutating what a reader will use.
	offsets := make([]uint32, n+1)
	var total uint64
	for i, d := range degree {
		offsets[i] = uint32(total)
		total += uint64(d)
	}
	// The offsets are uint32, which ADR-0404 §2's size table assumes and which is ample at
	// the scale it projects — 2 × 266 M entries is an eighth of the range. Accumulating in
	// uint64 and refusing above the limit is still worth the two lines: a wrap would not
	// fail, it would produce offsets that point into the wrong lists, and every answer
	// after that would be plausible and wrong.
	if total > math.MaxUint32 {
		return nil, fmt.Errorf("run graph has %d adjacency entries, more than a uint32 offset can address", total)
	}
	offsets[n] = uint32(total)

	// Pass 3: place each edge at both ends. cursor[i] walks node i's slot in Targets.
	//
	// The degree array is reused as the cursor rather than a third array being allocated:
	// its contents are spent once the prefix sum above has consumed them, and at 110 M
	// nodes each of these is 440 MB — the difference between the size table in ADR-0404 §2
	// and a third of a gigabyte over it.
	targets := make([]uint32, total)
	cursor := degree
	copy(cursor, offsets[:n])
	seen, err = eachEdge(src, ords, func(from, to uint32) {
		targets[cursor[from]] = to
		cursor[from]++
		targets[cursor[to]] = from
		cursor[to]++
	})
	if err != nil {
		return nil, err
	}
	if seen != n {
		return nil, fmt.Errorf("fill pass saw %d element instances, ordinal pass saw %d: the view is not consistent", seen, n)
	}

	// The two passes must have seen the same edges. They read the same snapshot, so a
	// disagreement means the view was not consistent after all — which would leave zeros
	// in Targets, and a zero there reads as ordinal 0: a real node, and the wrong one.
	// Checked rather than trusted, because the failure is invisible downstream.
	for i := range n {
		if cursor[i] != offsets[i+1] {
			return nil, fmt.Errorf("count and fill disagree at ordinal %d: filled to %d, expected %d", i, cursor[i], offsets[i+1])
		}
	}

	return &Graph{Ordinals: ords, Offsets: offsets, Targets: targets, Edges: edges}, nil
}

// eachEdge calls fn for every edge the scan yields, as a pair of ordinals.
//
// An element instance carries four key references and only three of them are ever an
// edge in *this* graph:
//
//   - FlowScopeKey — the enclosing scope, and 0 at the process root;
//   - AttachedToKey — a boundary event's host activity, and 0 on every other element;
//   - EventGatewayKey — the event gateway that armed a catch event, and 0 otherwise.
//
// ProcessInstanceKey is deliberately not one: a process instance lives in a different
// column family and is not in this ordinal map, so an edge to it would land on whichever
// element happens to hold that ordinal. TokenID and ParentTokenID are likewise not
// edges — they are a token id space rather than element keys, so token lineage is a
// different relation, and drawing it here would join nodes that are not adjacent.
//
// Every reference is resolved through the map and skipped when it names nothing, which is
// what makes a dangling reference — a retention boundary cutting a scope chain — a
// missing edge rather than a wrong one.
func eachEdge(src Source, ords *Ordinals, fn func(from, to uint32)) (seen int, err error) {
	err = src.ActiveElementInstances(func(key uint64, v *model.ElementInstanceValue) error {
		seen++
		from, ok := ords.Ordinal(key)
		if !ok {
			// The scan yielded a key the ordinal pass did not. Same snapshot, so this
			// cannot happen; if it does, the view was not consistent and the caller must
			// not get a graph.
			return fmt.Errorf("element instance %#x has no ordinal", key)
		}
		for _, ref := range [...]uint64{v.FlowScopeKey, v.AttachedToKey, v.EventGatewayKey} {
			if ref == 0 {
				continue
			}
			to, ok := ords.Ordinal(ref)
			if !ok {
				continue
			}
			fn(from, to)
		}
		return nil
	})
	return seen, err
}

// Components labels every node with its connected component, in one pass over the CSR by
// union-find (ADR-0404 §5). The run graph is a forest of millions of small components, so
// "what is this element connected to" becomes a label lookup afterwards rather than a walk —
// which is what [Graph.Membership] is over this array.
//
// This comment used to say the pass *is* the instance-family decomposition, repeating §5. W2's
// acceptance measurement on engine-written state disproved it: two service tasks on parallel
// top-level branches of one instance share only the process instance as a parent, and a
// process instance is not an element instance, so nothing joins them and one instance becomes
// two components (rungraph/component_test.go). A component is the **reference-connected**
// group, which is the grouping impact analysis wants; the instance grouping is a field on the
// element instance and needs none of this.
//
// The return is one label per ordinal, and the label is the smallest ordinal in the
// component. That choice is not cosmetic: it makes the label stable under a rebuild that
// sees the same graph, and it makes a component's label the start of its ordinal range
// wherever the locality §10 relies on actually holds.
//
// Path halving plus union by smaller label, which is what keeps this linear enough to run
// over a hundred million nodes without a second structure. Rank is deliberately not
// tracked: the label has to be the minimum ordinal, so the union direction is already
// determined and a rank array would only fight it.
func (g *Graph) Components() []uint32 {
	n := g.Ordinals.Len()
	parent := make([]uint32, n)
	for i := range parent {
		parent[i] = uint32(i)
	}
	var find func(uint32) uint32
	find = func(x uint32) uint32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]] // halve the path on the way up
			x = parent[x]
		}
		return x
	}
	for i := range n {
		for _, nb := range g.Neighbours(uint32(i)) {
			a, b := find(uint32(i)), find(nb)
			if a == b {
				continue
			}
			if a < b {
				parent[b] = a
			} else {
				parent[a] = b
			}
		}
	}
	// Flatten in place and return the same array, so a caller's lookup is one read rather
	// than a chase. Deliberately not a second array: ADR-0404 §2 budgets *one* 420 MB
	// union-find array at 110 M nodes, and returning a copy would quietly double it.
	for i := range n {
		parent[i] = find(uint32(i))
	}
	return parent
}

// Locality measures the property ADR-0404 §10 rests on: that a component's nodes are
// contiguous in ordinal space, so its adjacency lists are contiguous in Targets and a
// walk of one component reads sequential pages.
//
// Span is (max ordinal − min ordinal + 1) summed over components, divided by the node
// count. 1.0 is perfect — every component occupies an unbroken ordinal range. 2.0 means
// components are interleaved two deep, and a large number means the ordinals carry no
// locality at all and §10's mmap dial would be paying the 74× scattered-read penalty its
// own measurement found.
//
// It is a method rather than a test helper because the claim is a property of the built
// graph and an operator choosing where the CSR lives has the same question a test does.
func (g *Graph) Locality() (span float64, components int) {
	n := g.Ordinals.Len()
	if n == 0 {
		return 0, 0
	}
	labels := g.Components()

	// A component's label is the *smallest* ordinal in it, by construction: the union
	// below always points the larger label at the smaller. So the low end of every
	// component's range is the label itself and only the high end has to be found — which
	// is what lets this run over one flat array instead of a map with an entry per
	// component. At 110 M nodes that map would be the largest structure in the process,
	// and larger than the CSR it is measuring.
	high := make([]uint32, n)
	for ord := range n {
		l := labels[ord]
		if uint32(ord) > high[l] {
			high[l] = uint32(ord)
		}
	}
	var total uint64
	components = 0
	for ord := range n {
		if labels[ord] != uint32(ord) {
			continue // not its component's label, so not its representative
		}
		components++
		total += uint64(high[ord]-uint32(ord)) + 1
	}
	return float64(total) / float64(n), components
}
