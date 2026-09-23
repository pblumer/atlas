package panorama

import "sort"

// The estate altitude of ADR-0402: one node per domain, an
// altitude *above* the landscape rather than the landscape repeated.
//
// Why above rather than beside: eight domains at the measured 400-node budget each is a
// hairball by arithmetic while every individual picture stays inside its budget (§2). So the
// estate's own budget is the number of domains, which is operator configuration, and a domain
// expands into the L0 mesh that already exists.
//
// This file is the assembly and nothing else — a pure function over what somebody else
// collected, in the shape `DeriveGraph` already has. The fetch belongs to the caller for the
// reason every derivation here keeps: a network call must not happen inside the single writer,
// and a picture must be derivable in a test without one.

// KindDomain is one Atlas installation in an estate. It is the only node kind at this
// altitude: an estate of domains is what the picture is, and anything smaller belongs to the
// landscape a domain expands into.
const KindDomain = "domain"

// EdgePromotes is the one estate-wide edge ADR-0402 §4 admits: an application join a
// promotion recorded. It is a fact rather than a drawing — `deploymentTarget.Bindings` holds
// it, written when a promotion succeeded — and it says a promotion *happened*, never that the
// application is still deployed there, which nothing on this side can know.
const EdgePromotes = "promotes"

// StateUnserved is ADR-0402 §3's fifth case: **the peer answered, and does not serve this
// view.** An older build whose descriptor advertises no `panorama.mesh` feature is neither
// unreachable nor stale, and folding it into either sends an operator to the wrong place —
// unreachable sends them to look at a network, stale implies there was once an answer. It is a
// version boundary, and the descriptor's derived feature list is what makes it distinguishable
// at all.
//
// It is not a fault: severityOf leaves it at SeverityUnknown, the neutral rendering, because a
// peer that is simply older is an ordinary state of affairs.
const StateUnserved = "unserved"

// EstateDomain is one domain as the collector found it — this runtime, or a peer.
//
// Everything here is either operator configuration or something the domain reported. Nothing
// is inferred: a field the peer did not report stays empty, so a reader can tell the
// difference between "nothing there" and "nobody asked".
type EstateDomain struct {
	// ID addresses the domain: the deployment target's id, or this runtime's own handle.
	//
	// Deliberately not the runtime id, although ADR-0401 §1 makes
	// `(runtimeId, key)` an entity's estate-wide name. A peer that never answered has no
	// runtime id to be named by, and §3 says such a peer is still a shape on the picture —
	// so the node is addressed by what an operator configured, which always exists, and
	// the runtime id travels beside it where the peer actually reported one.
	ID   string
	Name string
	// RuntimeID is what the domain said it was, and empty where it said nothing.
	RuntimeID string
	// Holds is how many nodes that domain's own landscape holds, where it answered. It is
	// the number that makes §2's budget legible to a reader: a domain standing for 400
	// nodes and one standing for four are the same size on this picture, and only this says
	// they are not.
	Holds int
	// State and Reason are the observation the collector resolved (ADR-0189 §6), including
	// [StateUnserved].
	State  string
	Reason string
	// Applications maps a local application id to the id the same application has on this
	// domain, as a promotion recorded it. Only peers have it, and its size is what the join
	// stands for.
	Applications map[string]string
	// DrawnBy names the credential whose reach produced this domain's answer, because §1
	// requires the picture to state whose credential drew each subgraph: a federated read is
	// as wide as the credential that made it, and an incompleteness that is stated is a
	// fact while one that is not is a discovery.
	//
	// Empty for this runtime, which was drawn for the reader by the reader's own rights.
	DrawnBy string
}

// DeriveEstate assembles the estate picture from this runtime and its peers.
//
// The local domain is first, because a reader starts where they are and an estate that draws
// its peers and omits itself is a picture of somebody else's estate. Peers follow in id order:
// map iteration must not reach the payload, or two reads differ for no reason a reader can
// act on.
func DeriveEstate(local EstateDomain, peers []EstateDomain) Graph {
	g := Graph{Nodes: []Node{}, Edges: []Edge{}, RuntimeID: local.RuntimeID}
	g.Nodes = append(g.Nodes, local.node())

	ordered := make([]EstateDomain, len(peers))
	copy(ordered, peers)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	for _, p := range ordered {
		g.Nodes = append(g.Nodes, p.node())
		// One join between two domains, however many applications were promoted along it:
		// an edge's identity is its endpoints and its kind, and the count is a fact *about*
		// that identity — the same rule the landscape's own edges keep (ADR-0400).
		if n := len(p.Applications); n > 0 {
			g.Edges = append(g.Edges, Edge{
				From: domainNodeID(local.ID), To: domainNodeID(p.ID),
				Kind: EdgePromotes, Promoted: n,
			})
		}
	}
	return g
}

func (d EstateDomain) node() Node {
	return Node{
		ID: domainNodeID(d.ID), Kind: KindDomain, Name: d.Name,
		// Derived, always: an estate is read off deployment targets and what they answered,
		// and no model declares one.
		Provenance: ProvenanceDerived,
		RuntimeID:  d.RuntimeID,
		Holds:      d.Holds,
		State:      d.State,
		Severity:   severityOf(d.State),
		Reason:     d.Reason,
		DrawnBy:    d.DrawnBy,
	}
}

func domainNodeID(id string) string { return KindDomain + ":" + id }
