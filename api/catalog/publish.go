package catalog

import (
	"fmt"
	"sort"
)

// Publishing a catalogue is where the work happens.
//
// Everything provable about a catalogue is proved here, once, so that ordering
// against the resulting release never walks a graph, resolves a binding or
// discovers a missing translation. That is I5 applied to a catalogue: a cycle is a
// modelling error, and it must surface for the person who published it rather than
// as an incident for whoever orders at 23:00.
//
// Two separate graphs are checked, because they answer different questions and
// conflating them would refuse ordinary catalogues. Structure (composition and
// aggregation) says what belongs to what; precedence (requires) says what must
// exist first. A part that is also a precondition of its own whole is perfectly
// ordinary — a workplace contains an account and cannot be provisioned before one
// exists — and is a cycle only if the two edge kinds are read as one.

// Problem is one reason a catalogue cannot be published. Publish returns every
// problem it finds rather than the first: an author who fixes one refusal per
// attempt is an author who eventually works around the gate.
type Problem struct {
	// Catalog and Item name where the problem is, either or both empty when it
	// belongs to the whole input (a cycle, a rank tie).
	Catalog string `json:"catalog,omitempty"`
	Item    string `json:"item,omitempty"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	switch {
	case p.Item != "":
		return fmt.Sprintf("item %s: %s", p.Item, p.Message)
	case p.Catalog != "":
		return fmt.Sprintf("catalogue %s: %s", p.Catalog, p.Message)
	default:
		return p.Message
	}
}

// Input is the whole of what a publish is computed from. It is passed rather than
// read so the computation is a pure function of its argument: the same input
// produces the same release and the same problems, which is what lets a release be
// diffed and a refusal be reproduced.
type Input struct {
	Catalogs []Catalog
	Items    []Item
	Edges    []Edge
}

// Release is a frozen, published catalogue: what may be ordered, in what order it
// is fulfilled, and what it grants without asking anybody.
type Release struct {
	// ID, CatalogID and CreatedAt identify the release. They are assigned by
	// whoever publishes it; [Publish] computes the rest.
	ID        string `json:"id,omitempty"`
	CatalogID string `json:"catalogId,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	// Items are the item definitions as they stood when this was published, sorted
	// by id, and they are copies rather than references.
	//
	// This is the whole of the snapshot rule. An order names one release; if the
	// release pointed at rows that keep changing, an approval pending for a week
	// could be approving something else by the time it is granted. Only value data
	// is copied, so an edit to the catalogue afterwards cannot reach in here.
	Items []Item `json:"items,omitempty"`
	// Waves is every item in the input, grouped into rounds: nothing in a wave
	// depends on anything in the same wave, and everything it does depend on is in
	// an earlier one. An order names one release and follows this schedule; it
	// never recomputes it.
	//
	// A flat sequence was the first shape and could not carry the failure
	// behaviour the portal requires: a failing line must stop only the lines that
	// depend on it, and a list has already discarded the reason each item sits
	// where it does. Waves keep it — a failure stops its own successors, and the
	// rest of its wave and every independent branch continue.
	Waves [][]string `json:"waves"`
	// Requires holds each item's direct preconditions, sorted, with no entry for an
	// item that has none.
	//
	// Waves are the right unit to *run* in and the wrong unit to *start on*. At a
	// wave boundary all that is known is "the previous wave is done", which cannot
	// distinguish a line whose precondition failed from one whose precondition is
	// in the same wave and succeeded — so a wave-wide barrier either starts a line
	// whose precondition is missing, or holds one whose preconditions are all
	// there. Both are wrong, so the edges travel with the schedule and [Blocked] is
	// what the fulfilment process asks when a line fails.
	Requires map[string][]string `json:"requires,omitempty"`
	// Includes and Options are the structure: what a product is made of, and what
	// is offered alongside it. Both are direct edges, sorted, with no entry for a
	// product made of nothing.
	//
	// They are kept apart because they mean opposite things to a basket. An
	// inclusion is a consequence of ordering the whole — integral, never
	// deselectable — and an option is an offer. Merging them would order a second
	// screen for everybody who orders a workplace.
	Includes map[string][]string `json:"includes,omitempty"`
	Options  map[string][]string `json:"options,omitempty"`
	// WithoutApproval names every item orderable with no approval at all, sorted.
	// It is a standing list rather than a report somebody has to think to run,
	// because the role that defines approval rules is the role that publishes them.
	WithoutApproval []string `json:"withoutApproval"`
}

// Publish validates a catalogue set and computes its release. A non-empty problem
// list means nothing was published; the returned Release is then zero.
func Publish(in Input) (Release, []Problem) {
	var problems []Problem
	add := func(p Problem) { problems = append(problems, p) }

	byID := make(map[string]Item, len(in.Items))
	for _, it := range in.Items {
		byID[it.ID] = it
	}

	checkCatalogs(in, byID, add)
	checkItems(in, add)
	checkEdges(in, byID, add)

	// Both graphs must be acyclic. Structure is checked over containment alone;
	// precedence over requires alone.
	if ids, ok := cycleIn(in.Edges, byID, EdgeKind.Structural); !ok {
		add(Problem{Message: "structural cycle: " + join(ids)})
	}
	if ids, ok := cycleIn(in.Edges, byID, func(k EdgeKind) bool { return k == EdgeRequires }); !ok {
		add(Problem{Message: "precedence cycle: " + join(ids)})
	}

	if len(problems) > 0 {
		sortProblems(problems)
		return Release{}, problems
	}

	structure := structuralEdges(in)
	return Release{
		Items:           freeze(in.Items),
		Includes:        structure[EdgeComposition],
		Options:         structure[EdgeAggregation],
		Waves:           schedule(in),
		Requires:        preconditions(in),
		WithoutApproval: itemsWithoutApproval(in.Items),
	}, nil
}

// checkCatalogs holds what a catalogue owes independently of its items: a unique
// rank, and item references that resolve.
func checkCatalogs(in Input, byID map[string]Item, add func(Problem)) {
	seenRank := map[int]string{}
	cats := append([]Catalog(nil), in.Catalogs...)
	sort.Slice(cats, func(a, b int) bool { return cats[a].ID < cats[b].ID })

	for _, c := range cats {
		if other, taken := seenRank[c.Rank]; taken {
			// A tie is a coin toss dressed as a rule: the one-catalogue-per-user
			// resolution would silently fall back to an id nobody chose.
			add(Problem{Catalog: c.ID,
				Message: fmt.Sprintf("rank %d is already held by catalogue %s", c.Rank, other)})
		} else {
			seenRank[c.Rank] = c.ID
		}

		for _, id := range c.Items {
			it, known := byID[id]
			if !known {
				add(Problem{Catalog: c.ID, Message: "unknown item " + id})
				continue
			}
			for _, lang := range c.Languages {
				if it.Texts[lang] == "" {
					add(Problem{Catalog: c.ID, Item: id,
						Message: "no text for declared language " + lang})
				}
			}
		}
	}
}

// checkItems holds what an item owes to be orderable at all.
func checkItems(in Input, add func(Problem)) {
	items := append([]Item(nil), in.Items...)
	sort.Slice(items, func(a, b int) bool { return items[a].ID < items[b].ID })

	for _, it := range items {
		if it.State != StateActive {
			add(Problem{Item: it.ID, Message: "state is " + string(it.State) + ", not active"})
		}
		if it.ProvisionProcess == "" {
			add(Problem{Item: it.ID, Message: "no provision process bound"})
		}
		if it.DeprovisionProcess == "" {
			// A catalogue that can only grant is not a lifecycle.
			add(Problem{Item: it.ID, Message: "no deprovision process bound"})
		}
		switch it.Approval.Kind {
		case KindFixed, KindRole:
			if it.Approval.Ref == "" {
				add(Problem{Item: it.ID,
					Message: "approval kind " + string(it.Approval.Kind) + " needs a ref"})
			}
		}
		if it.Lifecycle.From != 0 && it.Lifecycle.Until != 0 && it.Lifecycle.Until <= it.Lifecycle.From {
			add(Problem{Item: it.ID, Message: "orderable window ends before it begins"})
		}
	}
}

// checkEdges holds that every edge lands on an item that exists. An edge into
// nothing would silently drop a precondition, which is the one failure the
// precomputed order must not have.
func checkEdges(in Input, byID map[string]Item, add func(Problem)) {
	edges := append([]Edge(nil), in.Edges...)
	sort.Slice(edges, func(a, b int) bool {
		if edges[a].From != edges[b].From {
			return edges[a].From < edges[b].From
		}
		return edges[a].To < edges[b].To
	})
	for _, e := range edges {
		if _, ok := byID[e.From]; !ok {
			add(Problem{Message: "edge from unknown item " + e.From})
		}
		if _, ok := byID[e.To]; !ok {
			add(Problem{Message: "edge to unknown item " + e.To})
		}
	}
}

// cycleIn reports whether the sub-graph selected by keep is acyclic, naming the
// items still entangled when it is not. Edges touching an unknown item are
// skipped — checkEdges already reported those, and following them here would
// report the same defect a second time in less useful words.
func cycleIn(edges []Edge, byID map[string]Item, keep func(EdgeKind) bool) (stuck []string, acyclic bool) {
	out := map[string][]string{}
	indeg := map[string]int{}
	nodes := map[string]bool{}

	for _, e := range edges {
		if !keep(e.Kind) {
			continue
		}
		if _, ok := byID[e.From]; !ok {
			continue
		}
		if _, ok := byID[e.To]; !ok {
			continue
		}
		out[e.From] = append(out[e.From], e.To)
		indeg[e.To]++
		nodes[e.From], nodes[e.To] = true, true
	}

	queue := make([]string, 0, len(nodes))
	for n := range nodes {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	sort.Strings(queue)

	removed := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		removed++
		next := append([]string(nil), out[n]...)
		sort.Strings(next)
		for _, m := range next {
			if indeg[m]--; indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
		sort.Strings(queue)
	}
	if removed == len(nodes) {
		return nil, true
	}
	for n := range nodes {
		if indeg[n] > 0 {
			stuck = append(stuck, n)
		}
	}
	sort.Strings(stuck)
	return stuck, false
}

// schedule groups every item into waves: a wave holds items that depend on
// nothing in the same wave, and on nothing in any later one.
//
// An item's wave is one past the latest of its preconditions — not one past the
// first satisfied, which is the mistake a plain breadth-first walk makes when an
// item waits on two things scheduled at different depths. Computing it as a
// longest path is what makes "everything this needs has already run" true of the
// wave boundary, which is the property the fulfilment process relies on to start a
// whole wave at once.
//
// Within a wave items are sorted, and the waves themselves come out in depth
// order, so the schedule is deterministic: one that reordered between two
// publishes of the same input would make a diff of two releases unreadable and
// would fulfil the same order differently twice.
//
// The input is known acyclic here — Publish returns before reaching this on any
// problem — so the relaxation below terminates.
func schedule(in Input) [][]string {
	dependents := map[string][]string{}
	indeg := map[string]int{}
	depth := map[string]int{}

	all := make([]string, 0, len(in.Items))
	for _, it := range in.Items {
		all = append(all, it.ID)
		indeg[it.ID] = 0
	}
	sort.Strings(all)

	for _, e := range in.Edges {
		if e.Kind != EdgeRequires {
			continue
		}
		// The dependent waits for its precondition, so the edge points To -> From.
		dependents[e.To] = append(dependents[e.To], e.From)
		indeg[e.From]++
	}

	ready := make([]string, 0, len(all))
	for _, id := range all {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}

	deepest := -1
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		if depth[id] > deepest {
			deepest = depth[id]
		}
		for _, dep := range dependents[id] {
			if d := depth[id] + 1; d > depth[dep] {
				depth[dep] = d
			}
			if indeg[dep]--; indeg[dep] == 0 {
				ready = append(ready, dep)
			}
		}
	}

	if deepest < 0 {
		return nil
	}
	waves := make([][]string, deepest+1)
	for _, id := range all {
		waves[depth[id]] = append(waves[depth[id]], id)
	}
	return waves
}

// Expand resolves a selection into everything it actually orders: the chosen
// products, plus every product any of them is made of, all the way down.
//
// Options are not pulled in — an aggregation is an offer, and including it
// automatically is how somebody ends up with a second screen they never asked
// for. Each product appears once however many selected products contain it,
// which is the deduplication the same service in two bundles needs.
//
// Anything the release does not carry is ignored rather than invented: a request
// naming a product that was withdrawn, or never existed, must not become a line
// that nothing can provision.
func (r Release) Expand(selected []string) []string {
	if len(selected) == 0 {
		return nil
	}
	carried := make(map[string]bool, len(r.Items))
	for _, it := range r.Items {
		carried[it.ID] = true
	}

	out := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if out[id] || !carried[id] {
			return
		}
		out[id] = true
		for _, part := range r.Includes[id] {
			walk(part)
		}
	}
	for _, id := range selected {
		walk(id)
	}

	ids := make([]string, 0, len(out))
	for id := range out {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// structuralEdges collects the composition and aggregation edges by kind.
func structuralEdges(in Input) map[EdgeKind]map[string][]string {
	out := map[EdgeKind]map[string][]string{
		EdgeComposition: {}, EdgeAggregation: {},
	}
	for _, e := range in.Edges {
		if !e.Kind.Structural() {
			continue
		}
		out[e.Kind][e.From] = append(out[e.Kind][e.From], e.To)
	}
	for _, byParent := range out {
		for _, parts := range byParent {
			sort.Strings(parts)
		}
	}
	for kind, byParent := range out {
		if len(byParent) == 0 {
			out[kind] = nil
		}
	}
	return out
}

// Blocked reports every item that cannot run because one of the given items
// failed, transitively, sorted and each named once.
//
// The failed items themselves are not in the result: they have a failure of their
// own to report, and listing them as blocked would send somebody looking in the
// wrong place. An unknown id blocks nothing rather than erroring — a fulfilment
// process asking about a line that is not in this release has a different problem,
// and inventing one here would hide it.
func (r Release) Blocked(failed ...string) []string {
	if len(failed) == 0 || len(r.Requires) == 0 {
		return nil
	}

	// Invert once: precondition -> the items waiting on it.
	waiting := map[string][]string{}
	for item, needs := range r.Requires {
		for _, need := range needs {
			waiting[need] = append(waiting[need], item)
		}
	}

	down := map[string]bool{}
	for _, f := range failed {
		down[f] = true
	}

	blocked := map[string]bool{}
	queue := append([]string(nil), failed...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range waiting[cur] {
			if blocked[next] || down[next] {
				continue
			}
			blocked[next] = true
			queue = append(queue, next)
		}
	}

	out := make([]string, 0, len(blocked))
	for id := range blocked {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// preconditions collects each item's direct preconditions from the edges.
func preconditions(in Input) map[string][]string {
	req := map[string][]string{}
	for _, e := range in.Edges {
		if e.Kind != EdgeRequires {
			continue
		}
		req[e.From] = append(req[e.From], e.To)
	}
	if len(req) == 0 {
		return nil
	}
	for _, needs := range req {
		sort.Strings(needs)
	}
	return req
}

// freeze copies the items into the release, sorted by id, deep enough that a later
// edit to the catalogue cannot reach into a published release through a shared map
// or slice.
func freeze(items []Item) []Item {
	if len(items) == 0 {
		return nil
	}
	out := make([]Item, len(items))
	for i, it := range items {
		it.Texts = copyTexts(it.Texts)
		if len(it.Variants) > 0 {
			vs := make([]Variant, len(it.Variants))
			for j, v := range it.Variants {
				v.Texts = copyTexts(v.Texts)
				vs[j] = v
			}
			it.Variants = vs
		}
		out[i] = it
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

func copyTexts(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// itemsWithoutApproval names every item that provisions with nobody asked.
func itemsWithoutApproval(items []Item) []string {
	var free []string
	for _, it := range items {
		if it.Approval.Kind == KindNone {
			free = append(free, it.ID)
		}
	}
	sort.Strings(free)
	return free
}

// sortProblems puts the list in one stable order so that two attempts at the same
// broken catalogue can be diffed.
func sortProblems(ps []Problem) {
	sort.SliceStable(ps, func(a, b int) bool {
		if ps[a].Catalog != ps[b].Catalog {
			return ps[a].Catalog < ps[b].Catalog
		}
		if ps[a].Item != ps[b].Item {
			return ps[a].Item < ps[b].Item
		}
		return ps[a].Message < ps[b].Message
	})
}

func join(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		out += id
	}
	return out
}
