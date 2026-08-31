package panorama

import (
	"fmt"
	"sort"
)

// The landscape mesh (ADR-0211) is Panorama's derived altitude: a whole-instance
// graph computed from resources Atlas already holds, rather than one a person drew.
// Its edges are facts the server can point at — a call activity *is* a dependency —
// which is what lets Panorama say something on an instance where nobody has modeled
// anything yet.
//
// DeriveGraph is deliberately a pure function over [Landscape]. Reading the stores,
// resolving which deployment a call actually reaches, and deciding what this caller
// may see all happen on the API run loop before it is called; the graph shape itself
// is then deterministic and testable without an HTTP request or a loop turn. Nothing
// here touches the WAL, applyToState, a processor, or recovery.

// Node kinds. The mesh keeps these namespaced by prefix in [Node.ID] so a renderer
// can route a click without parsing the kind separately.
const (
	KindApplication = "application"
	KindProcess     = "process"
	// KindRestricted is a resource that exists but which this caller may not see.
	// It stands in for a real node so the edge to it survives (ADR-0211 §3).
	KindRestricted = "restricted"
	// KindUnresolved is a call target that no deployment on this server provides.
	// Distinct from restricted on purpose: "not here" and "not yours to see" are
	// different findings, and an operator chasing a broken call needs to tell them
	// apart.
	KindUnresolved = "unresolved"
)

// Edge kinds.
const (
	EdgeContains = "contains"
	EdgeCalls    = "calls"
)

// Provenance values. Everything this slice derives is [ProvenanceDerived]; modeled
// and both arrive with the ArchiMate overlay in P2.5b, which needs P3 bindings.
const (
	ProvenanceDerived = "derived"
)

// Application is one Atlas process application as the mesh sees it. CanView is
// resolved by the server against the caller's sharing scope (ADR-0071) before the
// graph is derived — the mesh never decides access itself, it only honors it.
type Application struct {
	ID      string
	Name    string
	CanView bool
}

// Call is one call activity, already resolved by the server to the deployment it
// actually reaches. TargetKey is zero when nothing on this server provides the
// called process. Resolution belongs to the server because a call's effective
// target depends on deploy-time facts and call overrides (ADR-0076), not on
// anything the compiled model carries.
type Call struct {
	ElementID       string
	CalledProcessID string
	TargetKey       uint64
}

// Process is one deployed process, with the call activities it makes.
type Process struct {
	Key           uint64
	ProcessID     string
	Name          string
	Version       int32
	ApplicationID string
	CanView       bool
	Calls         []Call
}

// Landscape is everything the mesh derives from, already filtered for this caller.
type Landscape struct {
	Applications []Application
	Processes    []Process
}

// Options tunes one derivation.
type Options struct {
	// MaxNodes is the size budget (ADR-0211 §7). Zero means unlimited. Over budget
	// the graph collapses to applications and says so, rather than returning a graph
	// the browser cannot lay out — or, worse, a truncated one that looks complete.
	MaxNodes int
}

// Node is one vertex. A restricted node carries no Name, no process id, and no
// application: its kind is the whole of what it discloses.
type Node struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	Provenance string `json:"provenance"`
	// Application is the owning application's node id, for grouping. Empty on
	// application, restricted, and unresolved nodes.
	Application string `json:"application,omitempty"`
	// ProcessID and Version identify a process node well enough to navigate to the
	// Operations view (L2) without a second lookup.
	ProcessID string `json:"processId,omitempty"`
	Version   int32  `json:"version,omitempty"`
	// Children is how many nodes a collapsed application stands for. Set only when
	// the graph is clustered.
	Children int `json:"children,omitempty"`
}

// Edge is one directed relationship between two nodes.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// Graph is one derived mesh.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	// Restricted is how many placeholder nodes stand in for resources this caller
	// may not see. The legend states it, so a filtered picture says that it is
	// filtered instead of looking complete.
	Restricted int `json:"restricted"`
	// Clustered reports that the graph exceeded its size budget and collapsed to
	// applications.
	Clustered bool `json:"clustered"`
}

func applicationNodeID(id string) string  { return KindApplication + ":" + id }
func processNodeID(key uint64) string     { return fmt.Sprintf("%s:%d", KindProcess, key) }
func unresolvedNodeID(pid string) string  { return KindUnresolved + ":" + pid }
func restrictedNodeID(ordinal int) string { return fmt.Sprintf("%s:%d", KindRestricted, ordinal) }

// DeriveGraph computes the mesh for one caller's landscape.
func DeriveGraph(land Landscape, opts Options) Graph {
	visibleApps := map[string]Application{}
	for _, a := range land.Applications {
		if a.CanView {
			visibleApps[a.ID] = a
		}
	}

	// Index every process by key, visible or not: an invisible one is still needed
	// to answer "does this call target exist?" — which is what separates a restricted
	// placeholder from an unresolved one.
	byKey := make(map[uint64]Process, len(land.Processes))
	var visible []Process
	for _, p := range land.Processes {
		byKey[p.Key] = p
		if p.CanView {
			visible = append(visible, p)
		}
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].Key < visible[j].Key })

	g := Graph{Nodes: []Node{}, Edges: []Edge{}}

	appIDs := make([]string, 0, len(visibleApps))
	for id := range visibleApps {
		appIDs = append(appIDs, id)
	}
	sort.Strings(appIDs)
	for _, id := range appIDs {
		g.Nodes = append(g.Nodes, Node{
			ID: applicationNodeID(id), Kind: KindApplication,
			Name: visibleApps[id].Name, Provenance: ProvenanceDerived,
		})
	}

	// Restricted placeholders are keyed internally by the hidden target so two calls
	// to one target share a placeholder and two targets never merge into one — either
	// mistake invents or erases a dependency. The key never leaves this function; the
	// response carries an opaque per-response ordinal instead.
	restricted := map[uint64]string{}
	restrictedOrdinal := func(key uint64) string {
		if id, ok := restricted[key]; ok {
			return id
		}
		id := restrictedNodeID(len(restricted) + 1)
		restricted[key] = id
		return id
	}
	unresolved := map[string]bool{}
	// Edges are deduplicated: three call activities to one target are one dependency.
	seenEdge := map[Edge]bool{}
	addEdge := func(e Edge) {
		if !seenEdge[e] {
			seenEdge[e] = true
			g.Edges = append(g.Edges, e)
		}
	}

	for _, p := range visible {
		node := Node{
			ID: processNodeID(p.Key), Kind: KindProcess, Name: p.Name,
			Provenance: ProvenanceDerived, ProcessID: p.ProcessID, Version: p.Version,
		}
		if _, ok := visibleApps[p.ApplicationID]; ok {
			node.Application = applicationNodeID(p.ApplicationID)
			addEdge(Edge{From: node.Application, To: node.ID, Kind: EdgeContains})
		}
		g.Nodes = append(g.Nodes, node)
	}

	// Calls are walked in a second pass so every visible process node already exists;
	// a placeholder is minted only for a target that is genuinely not among them.
	for _, p := range visible {
		for _, c := range p.Calls {
			target, ok := byKey[c.TargetKey]
			switch {
			case c.TargetKey != 0 && ok && target.CanView:
				addEdge(Edge{From: processNodeID(p.Key), To: processNodeID(target.Key), Kind: EdgeCalls})
			case c.TargetKey != 0 && ok:
				addEdge(Edge{From: processNodeID(p.Key), To: restrictedOrdinal(target.Key), Kind: EdgeCalls})
			default:
				// No deployment provides the called process — or the resolved key is
				// not in the landscape at all, which is the same finding from the
				// caller's side. The called process id is safe to name: it is in this
				// caller's own model, which they can already read.
				unresolved[c.CalledProcessID] = true
				addEdge(Edge{From: processNodeID(p.Key), To: unresolvedNodeID(c.CalledProcessID), Kind: EdgeCalls})
			}
		}
	}

	ordinals := make([]uint64, 0, len(restricted))
	for key := range restricted {
		ordinals = append(ordinals, key)
	}
	sort.Slice(ordinals, func(i, j int) bool { return restricted[ordinals[i]] < restricted[ordinals[j]] })
	for _, key := range ordinals {
		g.Nodes = append(g.Nodes, Node{
			ID: restricted[key], Kind: KindRestricted, Provenance: ProvenanceDerived,
		})
	}
	g.Restricted = len(restricted)

	pids := make([]string, 0, len(unresolved))
	for pid := range unresolved {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	for _, pid := range pids {
		g.Nodes = append(g.Nodes, Node{
			ID: unresolvedNodeID(pid), Kind: KindUnresolved, Name: pid,
			Provenance: ProvenanceDerived,
		})
	}

	sort.SliceStable(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		if g.Edges[i].To != g.Edges[j].To {
			return g.Edges[i].To < g.Edges[j].To
		}
		return g.Edges[i].Kind < g.Edges[j].Kind
	})

	if opts.MaxNodes > 0 && len(g.Nodes) > opts.MaxNodes {
		return cluster(g, visible, appIDs, visibleApps)
	}
	return g
}

// cluster collapses an over-budget graph to its applications, recording how many
// nodes each one stands for. It answers with less rather than with a picture the
// browser cannot lay out, and Clustered says which of the two happened.
func cluster(full Graph, visible []Process, appIDs []string, apps map[string]Application) Graph {
	children := map[string]int{}
	for _, p := range visible {
		if _, ok := apps[p.ApplicationID]; ok {
			children[p.ApplicationID]++
		}
	}
	out := Graph{
		Nodes: []Node{}, Edges: []Edge{},
		Restricted: full.Restricted, Clustered: true,
	}
	for _, id := range appIDs {
		out.Nodes = append(out.Nodes, Node{
			ID: applicationNodeID(id), Kind: KindApplication, Name: apps[id].Name,
			Provenance: ProvenanceDerived, Children: children[id],
		})
	}
	return out
}
