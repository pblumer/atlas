package rungraph

// W2 of ADR-0404 §5: "One pass computes membership; a query is a lookup."
//
// The pass is [Graph.Components] — union-find over the CSR, which W1 built. What this file
// adds is the half of that sentence W1 did not have: the queries, answered against the
// membership rather than by walking the graph again.
//
// **The acceptance measurement corrected what the membership *is*.** §5 says connected
// components "for this topology *is* the instance-family decomposition". On engine-written
// state that holds for a nested shape and fails for a flat one: two service tasks on parallel
// top-level branches of one instance park with **no edge between them**, because the only
// parent they share is the process instance, and a process instance is not an element instance
// and therefore not a node of this graph. Sixty-four such instances decompose into a hundred
// and twenty-eight components, not sixty-four (component_test.go logs both shapes).
//
// So a component is the **reference-connected** group: everything a walk from one node would
// reach, which is exactly what impact analysis asks and is not the same question as "which
// instance is this". That second question needs no union-find at all — `ProcessInstanceKey` is
// a field on the element instance the store already hands over. Naming this API after instance families
// would have made every caller believe it answered the cheaper question, wrongly.

// Membership answers which component a node belongs to.
//
// A component's *name* is the key of its smallest ordinal. That is W1's label choice used for
// what it was chosen for: the smallest ordinal is stable under a rebuild that sees the same
// graph, so a component keeps its name across rebuilds and a saved view (§9) keeps its
// meaning.
//
// Two kinds of question live here and they cost different things, which the method comments
// state rather than leave to be discovered:
//
//   - **Lookups.** [Membership.Of] and [Membership.SameComponent] are a binary search over the
//     ordinal map and one array read. Nothing touches the CSR. This is the query §5 promises
//     as a lookup, and the one an impact analysis asks a million times.
//   - **Passes.** [Membership.Members], [Membership.Size] and [Membership.Count] read the
//     whole label array once. They are O(n) and say so.
type Membership struct {
	ords *Ordinals
	// labels is one label per ordinal, aliasing what Components returned. See
	// [Graph.Membership] for why it is aliased rather than copied.
	labels []uint32
}

// Membership runs the membership pass and returns the lookup surface over it.
//
// It holds the label array [Graph.Components] returned rather than building an index of its
// own, and that is a budget decision rather than laziness. ADR-0404 §2 budgets *one*
// union-find array — 420 MB at 110 million nodes — and Components already flattens it so a
// lookup is a single read. A Membership that grouped members by component for O(1)
// enumeration would add a second array of the same size, doubling the structure the record
// sized.
//
// What that costs is enumeration: [Membership.Members] scans instead of slicing. The trade is
// the right way round because of which query has to be cheap *at that size*. §9 makes the
// narrow scope the entry and the whole graph the exception, so enumerating a component happens
// on a graph of thousands of nodes, where a scan is free; while the lookup — the query that
// runs at whole-graph scale — is O(1) already. Paying 420 MB to make the rare query faster on
// the rare graph is the wrong purchase, and it is one nobody could take back later.
func (g *Graph) Membership() *Membership {
	return &Membership{ords: g.Ordinals, labels: g.Components()}
}

// Len is how many nodes the membership covers.
func (m *Membership) Len() int { return len(m.labels) }

// Labels is the raw label-per-ordinal array, for a caller doing its own pass over it — the
// cloud's group-by (§5) is one. It aliases the membership's own array: a caller that writes to
// it changes what every lookup here answers.
func (m *Membership) Labels() []uint32 { return m.labels }

// Of is the component of the node with this key: a binary search and one array read, touching
// no part of the CSR. ok is false for a key the graph does not hold, which includes every key
// written after the snapshot the graph was built from.
func (m *Membership) Of(key uint64) (component uint64, ok bool) {
	ord, ok := m.ords.Ordinal(key)
	if !ok {
		return 0, false
	}
	return m.ords.Key(m.labels[ord]), true
}

// SameComponent reports whether two keys are reference-connected — the question an impact
// analysis asks, answered by two lookups and no walk.
//
// A key the graph does not hold is in no component, so it is not in another key's component
// and not in another absent key's either. Answering true for two unknown keys would be the
// worse failure: an analysis over stale keys would report the whole estate as related.
func (m *Membership) SameComponent(a, b uint64) bool {
	ca, ok := m.Of(a)
	if !ok {
		return false
	}
	cb, ok := m.Of(b)
	if !ok {
		return false
	}
	return ca == cb
}

// Members is every member key of the component this key belongs to, in ascending key order —
// nil for a key the graph does not hold.
//
// One pass over the label array, for the reason [Graph.Membership] gives. Ascending key order
// comes free: ordinals are the scan position of an ascending scan, so ascending ordinal *is*
// ascending key, and the caller's next move — showing the members, or reading them back out of
// the store — wants an order it did not have to impose.
//
// Deliberately not derived from the component's ordinal range. W1 measured that a component
// occupies a contiguous range only when instances arrive sequentially; under concurrent
// arrival the span grows with the concurrency, so a range-based answer would be right on a
// quiet installation and silently wrong on a busy one.
func (m *Membership) Members(key uint64) []uint64 {
	label, ok := m.labelOf(key)
	if !ok {
		return nil
	}
	out := make([]uint64, 0, 8)
	for ord, l := range m.labels {
		if l == label {
			out = append(out, m.ords.Key(uint32(ord)))
		}
	}
	return out
}

// Size is how many members the component has, counted without building the list. It is what a
// cloud asks per component, and an allocation per component is what §9's budget cannot afford
// at whole-graph scale. 0 for a key the graph does not hold.
func (m *Membership) Size(key uint64) int {
	label, ok := m.labelOf(key)
	if !ok {
		return 0
	}
	n := 0
	for _, l := range m.labels {
		if l == label {
			n++
		}
	}
	return n
}

// Count is how many components the graph decomposes into. One pass and no allocation: a node
// is its component's representative exactly when its label is its own ordinal, which holds
// because the label is the smallest ordinal in the component.
func (m *Membership) Count() int {
	n := 0
	for ord, l := range m.labels {
		if l == uint32(ord) {
			n++
		}
	}
	return n
}

// labelOf is the ordinal-space label of the component this key belongs to — the form the two
// passes above compare against, so neither has to translate twice.
func (m *Membership) labelOf(key uint64) (uint32, bool) {
	ord, ok := m.ords.Ordinal(key)
	if !ok {
		return 0, false
	}
	return m.labels[ord], true
}
