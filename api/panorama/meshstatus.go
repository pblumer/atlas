package panorama

// Severity on the landscape mesh (ADR-0211 §4, P2.5c).
//
// The seven observation states of ADR-0189 §6 remain the model and the contract.
// The mesh does not replace them with a traffic light; it aggregates them into
// three severity classes for zoom-out legibility, under the one mapping below, and
// carries the state it came from on every node so nothing is lost on the way.
//
// What this server can actually observe is a smaller set than the seven, and the
// payload says which — see [unobservable]. A picture that quietly reported only
// what it could see would render an instance with no observation source at all as
// uniformly healthy, which is the single most damaging thing a status view can do.

// Observation states (ADR-0189 §6). StateUnmodeled is deliberately absent: Atlas
// finding a resource no model mentions is already reported, per node, as
// provenance (ADR-0211 §2), and repeating it as a status would make every node of
// an unmodeled instance carry a finding.
const (
	StateHealthy     = "healthy"
	StateDegraded    = "degraded"
	StateNotReady    = "not-ready"
	StateUnreachable = "unreachable"
	StateStale       = "stale"
	// StateUnbound is ADR-0189 §6's "unbound/unknown": there is no observation to
	// have, or none applies. It is the default, and it is not a finding.
	StateUnbound = "unbound"
)

// Severity classes (ADR-0211 §4).
const (
	SeverityOK        = "ok"
	SeverityAttention = "attention"
	SeverityCritical  = "critical"
	// SeverityUnknown is the neutral rendering, not a fourth level of badness. Most
	// nodes in a young landscape are unbound, and coloring them as a problem makes
	// the whole mesh a problem.
	SeverityUnknown = "unknown"
)

// severityOfState is the mapping, in one place, as ADR-0211 §4 requires. Two of its
// rows are fixed by that record and the rest are decided here:
//
//   - unreachable and stale map to attention, never to critical. "I do not know" and
//     "it is broken" are different findings, and a view that rendered them alike
//     loses its credibility on the first network fault — exactly when it is being
//     relied on.
//   - degraded maps to attention: the resource works, and some of the work inside it
//     did not. A parked token is a token, not an outage.
//   - not-ready maps to critical: the dependency itself cannot do work, so
//     everything that needs it is stopped and stays stopped until somebody acts.
//     That is what "it is broken" means here.
var severityOfState = map[string]string{
	StateHealthy:     SeverityOK,
	StateDegraded:    SeverityAttention,
	StateNotReady:    SeverityCritical,
	StateUnreachable: SeverityAttention,
	StateStale:       SeverityAttention,
	StateUnbound:     SeverityUnknown,
}

// severityOf maps a state onto its class, defaulting unknown states to neutral: a
// state this build does not recognize is a fact it cannot interpret, and guessing a
// severity for it would be inventing one.
func severityOf(state string) string {
	if class, ok := severityOfState[state]; ok {
		return class
	}
	return SeverityUnknown
}

// severityRank orders the classes for worst-of aggregation. Unknown ranks below ok
// on purpose: a parent whose children are all unobserved is unobserved, not well.
var severityRank = map[string]int{
	SeverityUnknown:   0,
	SeverityOK:        1,
	SeverityAttention: 2,
	SeverityCritical:  3,
}

// UnavailableState is one observation state this build cannot produce, with the
// reason it cannot. It is in the payload rather than in the documentation because
// the consumer of a status picture is the one who needs to know what the absence of
// a finding is worth.
type UnavailableState struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// unobservableWithoutPeers is what a mesh that drew no deployment target cannot say.
//
// Both entries need a source outside this process, and a landscape with no peer on
// it contacted nothing: every other node is read from this server's own state while
// the request is being served, so it can neither fail to be reached nor go out of
// date. Those two states are then not "nothing is wrong" — they are questions this
// particular picture never asked, and the payload says so rather than letting an
// absence read as an answer.
//
// It is a *property of one response*, not of the build. A landscape that drew a peer
// can produce both, and declaring otherwise beside a stale target node would be the
// payload contradicting its own picture — see [unobservableFor].
var unobservableWithoutPeers = []UnavailableState{
	{
		State: StateUnreachable,
		Reason: "Nothing on this landscape is reached over the network — no deployment target " +
			"is drawn here — so it cannot report that something could not be reached. " +
			"Configure a deployment target and this landscape reports it, as the " +
			"observation projection over a model's bindings already does (ADR-0189 §6).",
	},
	{
		State: StateStale,
		Reason: "Every fact here is read from this server's own state when the request is " +
			"served, so no observation on this landscape has a freshness contract to " +
			"exceed. Only a peer's answer holds one: configure a deployment target and " +
			"this landscape reports it, as the observation projection over a model's " +
			"bindings already does (ADR-0189 §6).",
	},
}

// unobservableFor names what *this* graph cannot say, from what it actually drew.
//
// Derived rather than fixed, because it stopped being a property of the build the
// moment the landscape gained a kind that is asked over the network. A payload that
// declared "unreachable cannot happen here" beside a target node reporting exactly
// that would be a contract nobody could rely on again.
func unobservableFor(g *Graph) []UnavailableState {
	for _, n := range g.Nodes {
		if n.Kind == KindTarget {
			// A peer is drawn, so both states are producible on this landscape. The
			// empty list is the same claim [unobservableInDocument] makes, and it is
			// worth making out loud: there is nothing here this picture cannot see.
			return []UnavailableState{}
		}
	}
	return unobservableWithoutPeers
}

// unobservableInDocument is what an *observation document* cannot produce. It is
// empty: the document reaches peers, so every one of ADR-0189 §6's states is
// reachable in it. Stated as an explicit empty list rather than left nil, because
// "there is nothing this cannot see" is a claim worth making out loud — and one a
// reader can hold the code to.
var unobservableInDocument = []UnavailableState{}

// Status summarizes the severity of one derived mesh.
type Status struct {
	OK        int `json:"ok"`
	Attention int `json:"attention"`
	Critical  int `json:"critical"`
	// Unknown counts the nodes with no applicable observation. It is stated rather
	// than folded into ok, because "nothing is wrong here" and "nothing here is
	// watched" are the two answers a status view must never merge.
	Unknown int `json:"unknown"`
	// Partial reports that the incident scan hit its bound before it finished, so
	// some parked work went uncounted and a node's ok is a floor rather than a
	// verdict.
	Partial bool `json:"partial,omitempty"`
	// Unavailable names the observation states this build cannot produce at all.
	Unavailable []UnavailableState `json:"unavailable"`
}

// applyStatus sets each node's severity from the state the server observed, then
// aggregates worst-of up the containment edges (ADR-0211 §4).
//
// Aggregation follows containment only — an application takes the worst of its own
// processes — and deliberately not dependency. Propagating along calls and uses
// would make a node's color a function of transitive reachability, so one unserved
// worker would repaint most of a few-hundred-node landscape; a mesh that turns
// mostly red on a single fault is the mesh nobody believes on the second one. The
// dependency direction has its own answer already: impact analysis (§6) names
// exactly what a failing node stops, on demand and with its path.
func applyStatus(g *Graph, partial bool) {
	at := make(map[string]int, len(g.Nodes))
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.State == "" {
			n.State = StateUnbound
		}
		n.Severity = severityOf(n.State)
		at[n.ID] = i
	}

	// Worst-of per container, resolved in one pass over the containment edges. The
	// mesh nests exactly one level (application contains process), so no fixpoint is
	// needed; the parent is looked up rather than assumed, so an edge whose ends did
	// not both survive a filter is skipped instead of crashing.
	type inherited struct {
		from   string
		reason string
		class  string
	}
	worst := map[string]inherited{}
	for _, e := range g.Edges {
		if e.Kind != EdgeContains {
			continue
		}
		child, ok := at[e.To]
		if !ok {
			continue
		}
		c := g.Nodes[child]
		got := inherited{from: c.ID, reason: c.Reason, class: c.Severity}
		if have, seen := worst[e.From]; seen && severityRank[have.class] >= severityRank[got.class] {
			continue
		}
		worst[e.From] = got
	}
	for _, parent := range sortedKeys(worst) {
		index, ok := at[parent]
		if !ok {
			continue
		}
		n, got := &g.Nodes[index], worst[parent]
		if severityRank[got.class] <= severityRank[n.Severity] {
			continue
		}
		// Attribution is not optional (ADR-0211 §4). An unattributed red parent says
		// something is wrong somewhere, which is not actionable and trains an
		// operator to ignore the color.
		n.Severity, n.SeverityFrom, n.Reason = got.class, got.from, got.reason
	}

	g.Status = Status{Partial: partial, Unavailable: unobservableFor(g)}
	for _, n := range g.Nodes {
		switch n.Severity {
		case SeverityOK:
			g.Status.OK++
		case SeverityAttention:
			g.Status.Attention++
		case SeverityCritical:
			g.Status.Critical++
		default:
			g.Status.Unknown++
		}
	}
}
