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
	// KindWorker is one configured Worker — a target and identity of a Worker Type
	// (ADR-0203). The store behind it is still the connector store and the model
	// still names it with connector="…"; those are the contracts that cannot move
	// yet, and this is new surface, so it says Worker.
	KindWorker   = "worker"
	KindDecision = "decision"
	// KindRestricted is a resource that exists but which this caller may not see.
	// It stands in for a real node so the edge to it survives (ADR-0211 §3).
	KindRestricted = "restricted"
	// KindUnresolved is a dependency nothing on this server provides: a call target
	// with no deployment, or a worker name nobody configured. Distinct from
	// restricted on purpose — "not here" and "not yours to see" are different
	// findings, and an operator chasing a broken dependency needs to tell them apart.
	KindUnresolved = "unresolved"
)

// Edge kinds.
const (
	EdgeContains = "contains"
	EdgeCalls    = "calls"
	// EdgeUses is a process depending on something that is not a process: a
	// configured worker, or a decision it delegates to.
	EdgeUses = "uses"
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

// WorkerUse is one model reference to a configured worker, already resolved by the
// server. A model names a worker by name and never carries an endpoint or a secret
// (ADR-0036/0041), so nothing inside it can tell whether that name is configured
// anywhere — which is exactly why the references are enumerable from outside
// (ADR-0158), and why the mesh can answer it. TargetID is empty when no worker by
// that name is configured.
type WorkerUse struct {
	ElementID string
	Name      string
	TargetID  string
}

// Worker is one configured worker as the mesh sees it — a target and identity of a
// Worker Type (ADR-0203), which is what Type names. Endpoint and CredentialsRef are
// carried so the derivation can be tested for *not* emitting them: a landscape
// picture is opened by anyone with modeler access, and an internal hostname is
// precisely what ADR-0211 §10 keeps out of what leaves the server. Neither field
// ever reaches a [Node].
type Worker struct {
	ID             string
	Name           string
	Type           string
	CanView        bool
	Endpoint       string
	CredentialsRef string
}

// Decision is one local DMN decision a business-rule task delegates to. A remote
// decision, evaluated by a worker rather than in this engine, is deliberately not
// one of these: it arrives as a [WorkerUse] instead, which is where its dependency
// actually points.
type Decision struct {
	ID      string
	Name    string
	CanView bool
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
	Workers       []WorkerUse
	Decisions     []string
}

// Landscape is everything the mesh derives from, already filtered for this caller.
type Landscape struct {
	Applications []Application
	Processes    []Process
	Workers      []Worker
	Decisions    []Decision
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
	// WorkerType is a worker node's Worker Type ("rest", "mail", …). Never its
	// endpoint and never its credential reference — see [Worker].
	WorkerType string `json:"workerType,omitempty"`
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
func workerNodeID(id string) string       { return KindWorker + ":" + id }
func decisionNodeID(id string) string     { return KindDecision + ":" + id }
func restrictedNodeID(ordinal int) string { return fmt.Sprintf("%s:%d", KindRestricted, ordinal) }

// unresolvedNodeID names what is missing *and* what kind of thing it is. A BPMN
// process id and a worker name can be the same string while being two entirely
// different findings, so the kind is part of the identity rather than a label on it.
func unresolvedNodeID(kind, name string) string { return KindUnresolved + ":" + kind + ":" + name }

// sortedKeys returns a map's keys in a stable order. Every collection the graph
// emits goes through it: map iteration order is random in Go, and a mesh that
// reorders between two identical requests cannot be diffed and redraws on noise.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

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

	// Restricted placeholders are keyed internally by the hidden resource — kind and
	// id together — so repeated references to one resource share a placeholder, two
	// resources never merge into one, and a hidden process is never confused with a
	// hidden worker. Any of those mistakes invents or erases a dependency. The key
	// never leaves this function; the response carries an opaque per-response ordinal.
	restricted := map[string]string{}
	restrictedOrdinal := func(key string) string {
		if id, ok := restricted[key]; ok {
			return id
		}
		id := restrictedNodeID(len(restricted) + 1)
		restricted[key] = id
		return id
	}
	// unresolved is keyed by its node id, which already carries the kind.
	unresolved := map[string]string{}
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

	// Dependencies are walked in a second pass so every visible process node already
	// exists; a placeholder is minted only for a target genuinely not among them.
	//
	// Workers and decisions become nodes only where a process references them. The
	// mesh is the dependency picture, not an inventory: a configured worker nothing
	// uses is not a landscape edge, and putting it on screen would bury the ones
	// that are.
	workers := map[string]Worker{}
	for _, w := range land.Workers {
		workers[w.ID] = w
	}
	decisions := map[string]Decision{}
	for _, d := range land.Decisions {
		decisions[d.ID] = d
	}
	usedWorkers := map[string]bool{}
	usedDecisions := map[string]bool{}

	for _, p := range visible {
		from := processNodeID(p.Key)
		for _, c := range p.Calls {
			target, ok := byKey[c.TargetKey]
			switch {
			case c.TargetKey != 0 && ok && target.CanView:
				addEdge(Edge{From: from, To: processNodeID(target.Key), Kind: EdgeCalls})
			case c.TargetKey != 0 && ok:
				addEdge(Edge{From: from, To: restrictedOrdinal(processNodeID(target.Key)), Kind: EdgeCalls})
			default:
				// No deployment provides the called process — or the resolved key is
				// not in the landscape at all, which is the same finding from the
				// caller's side. The called process id is safe to name: it is in this
				// caller's own model, which they can already read.
				id := unresolvedNodeID(KindProcess, c.CalledProcessID)
				unresolved[id] = c.CalledProcessID
				addEdge(Edge{From: from, To: id, Kind: EdgeCalls})
			}
		}
		for _, u := range p.Workers {
			target, ok := workers[u.TargetID]
			switch {
			case u.TargetID != "" && ok && target.CanView:
				usedWorkers[target.ID] = true
				addEdge(Edge{From: from, To: workerNodeID(target.ID), Kind: EdgeUses})
			case u.TargetID != "" && ok:
				addEdge(Edge{From: from, To: restrictedOrdinal(workerNodeID(target.ID)), Kind: EdgeUses})
			default:
				// The model asks for a worker nobody configured, so the task would
				// fail at run time. This is the question a model cannot answer about
				// itself, and the name is safe: it is in this caller's own model.
				id := unresolvedNodeID(KindWorker, u.Name)
				unresolved[id] = u.Name
				addEdge(Edge{From: from, To: id, Kind: EdgeUses})
			}
		}
		for _, ref := range p.Decisions {
			target, ok := decisions[ref]
			switch {
			case ok && target.CanView:
				usedDecisions[target.ID] = true
				addEdge(Edge{From: from, To: decisionNodeID(target.ID), Kind: EdgeUses})
			case ok:
				addEdge(Edge{From: from, To: restrictedOrdinal(decisionNodeID(target.ID)), Kind: EdgeUses})
			default:
				id := unresolvedNodeID(KindDecision, ref)
				unresolved[id] = ref
				addEdge(Edge{From: from, To: id, Kind: EdgeUses})
			}
		}
	}

	for _, id := range sortedKeys(usedWorkers) {
		w := workers[id]
		// Name and Worker Type only. The record also holds an endpoint and a
		// credential reference; a landscape picture is opened by anyone with modeler
		// access, and neither belongs in one (ADR-0211 §10, I6).
		g.Nodes = append(g.Nodes, Node{
			ID: workerNodeID(w.ID), Kind: KindWorker, Name: w.Name,
			Provenance: ProvenanceDerived, WorkerType: w.Type,
		})
	}
	for _, id := range sortedKeys(usedDecisions) {
		d := decisions[id]
		g.Nodes = append(g.Nodes, Node{
			ID: decisionNodeID(d.ID), Kind: KindDecision, Name: d.Name,
			Provenance: ProvenanceDerived,
		})
	}

	placeholders := sortedKeys(restricted)
	sort.Slice(placeholders, func(i, j int) bool {
		return restricted[placeholders[i]] < restricted[placeholders[j]]
	})
	for _, key := range placeholders {
		g.Nodes = append(g.Nodes, Node{
			ID: restricted[key], Kind: KindRestricted, Provenance: ProvenanceDerived,
		})
	}
	g.Restricted = len(restricted)

	for _, id := range sortedKeys(unresolved) {
		g.Nodes = append(g.Nodes, Node{
			ID: id, Kind: KindUnresolved, Name: unresolved[id],
			Provenance: ProvenanceDerived,
		})
	}

	// The kind tiebreak keeps the comparator total. With today's three kinds it is
	// unreachable — contains leaves an application, calls reaches a process, and uses
	// reaches something that is not one, so no two edges share both endpoints — and
	// it is deliberately not covered by a contrived test. It is here because the next
	// edge kinds (releases, targets) will make it reachable, and a comparator that is
	// only total by accident sorts unstably the day that happens.
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
