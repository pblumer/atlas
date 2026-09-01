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

// Provenance says how a node is known (ADR-0211 §2). The three are the point of
// overlaying a model onto the mesh: without them you have two pictures and no
// relationship between them.
const (
	// ProvenanceDerived: Atlas has this resource and no model binds to it.
	ProvenanceDerived = "derived"
	// ProvenanceModeled: a model declares this and Atlas does not have it. This is
	// the half a drawn-only view can never show — the architecture says something
	// exists and the instance disagrees.
	ProvenanceModeled = "modeled"
	// ProvenanceBoth: Atlas has it and a model binds to it.
	ProvenanceBoth = "both"
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
	// State and Reason are this worker's observation, as on [Process].
	State  string
	Reason string
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
	// State is the observation state (ADR-0189 §6) the server read for this
	// process, and Reason is the sentence behind it. Empty means unbound: no
	// observation applies, which is not a finding.
	State  string
	Reason string
}

// Landscape is everything the mesh derives from, already filtered for this caller.
type Landscape struct {
	Applications []Application
	Processes    []Process
	Workers      []Worker
	Decisions    []Decision
	// PartialStatus reports that the server stopped counting parked work before it
	// had seen all of it. It travels with the landscape because only the collector
	// knows it, and it must reach the payload: without it a process the scan never
	// reached would be published as healthy on no evidence at all.
	PartialStatus bool
}

// ModelElement is one bound ArchiMate element: what the architect called it, and
// which Atlas ids its binding names (ADR-0189 §4).
type ModelElement struct {
	ElementID   string
	ElementType string
	Name        string
	Key         string
	Values      []string
}

// Overlay is what one Panorama model contributes to the mesh.
type Overlay struct {
	ModelID   string
	ModelName string
	Elements  []ModelElement
}

// overlayNodeID names a resource a model declares but Atlas does not have. It is
// namespaced away from the derived ids on purpose: a derived process node is keyed
// by deployment key while a binding names a BPMN process id, so reusing the derived
// form would risk two different things claiming one id.
func overlayNodeID(kind, value string) string { return ProvenanceModeled + ":" + kind + ":" + value }

// overlayKind maps a binding key onto the mesh node kind it can match. A key absent
// here binds something the mesh does not draw — a release, a deployment target, a
// runtime. That is not absence, it is a different altitude, and reporting it as
// absence would invent drift that is not there.
//
// The binding key keeps its ADR-0189 §4 spelling: atlas.connectorId is a wire
// contract carried inside documents that already exist, and the Worker rename
// (ADR-0203) is a vocabulary change in Atlas, not a licence to break them.
var overlayKind = map[string]string{
	KeyApplicationID: KindApplication,
	KeyProcessID:     KindProcess,
	KeyConnectorID:   KindWorker,
}

// Options tunes one derivation.
type Options struct {
	// Overlays are the Panorama models compared against this landscape. Empty
	// leaves the mesh exactly as the derivation alone made it.
	Overlays []Overlay
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
	// The ArchiMate element bound to this node, when a model binds one. ModelName is
	// carried beside Name rather than replacing it: the architect's name and the
	// Atlas name are allowed to differ, and the difference is informative.
	ModelElementID   string `json:"modelElementId,omitempty"`
	ModelElementType string `json:"modelElementType,omitempty"`
	ModelName        string `json:"modelName,omitempty"`
	// State is the observation state behind Severity (ADR-0189 §6), kept beside it
	// because the three classes are a reading aid and never a replacement: an
	// operator acting on a finding needs the state, not the color.
	State    string `json:"state"`
	Severity string `json:"severity"`
	// Reason is the sentence behind this node's severity, in the words of whatever
	// observed it. Empty on a node with nothing to report.
	Reason string `json:"reason,omitempty"`
	// SeverityFrom names the descendant a node inherited its severity from, and is
	// empty when the severity is the node's own. ADR-0211 §4 requires it: a red
	// parent that cannot say which child is red is not actionable.
	SeverityFrom string `json:"severityFrom,omitempty"`
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
	// The desired-versus-observed comparison, when a model was overlaid.
	// Modeled counts what a model declares and Atlas does not have; Unmodeled counts
	// what Atlas has and no model mentions.
	Modeled   int `json:"modeled"`
	Unmodeled int `json:"unmodeled"`
	// OutOfScope counts bindings to kinds this picture does not draw — releases,
	// deployment targets, runtimes. They are neither matched nor absent, and the
	// count keeps that visible instead of silently dropping them.
	OutOfScope int `json:"outOfScope"`
	// Status is the severity summary and, as importantly, the declaration of which
	// observation states this build cannot produce at all (ADR-0211 §4).
	Status Status `json:"status"`
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
			State: p.State, Reason: p.Reason,
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
			State: w.State, Reason: w.Reason,
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
		// No State, and therefore no severity beyond neutral. Whether a resource
		// outside this caller's access is healthy or broken is a fact about that
		// resource, and a placeholder that leaked it would turn the mesh into a
		// side channel around the sharing scope it exists to honor (ADR-0211 §3).
		g.Nodes = append(g.Nodes, Node{
			ID: restricted[key], Kind: KindRestricted, Provenance: ProvenanceDerived,
		})
	}
	g.Restricted = len(restricted)

	// Unresolved nodes carry no state either, and for a different reason: there is
	// no resource to observe. The finding is structural and the kind already states
	// it; giving it a severity as well would report one fact twice, in two channels
	// that could then disagree.
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
	applyOverlays(&g, opts.Overlays, visible)

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
		return cluster(g, visible, appIDs, visibleApps, land.PartialStatus)
	}
	applyStatus(&g, land.PartialStatus)
	return g
}

// applyOverlays compares the declared architecture against the derived landscape
// (ADR-0211 §11, P2.5b). It runs on the graph this caller can already see, so a
// binding to a resource outside their access finds nothing and is reported as
// modeled-but-absent — which is the honest answer to give them, and keeps the
// overlay from becoming a way to read a name a sharing scope withholds.
func applyOverlays(g *Graph, overlays []Overlay, visible []Process) {
	if len(overlays) == 0 {
		return
	}
	// A process is bound by BPMN process id while its derived node is keyed by
	// deployment key. Matching on the wrong one would report every modeled process
	// as absent, which reads as landscape-wide drift that is not there.
	nodeByProcessID := make(map[string]string, len(visible))
	for _, p := range visible {
		nodeByProcessID[p.ProcessID] = processNodeID(p.Key)
	}
	at := make(map[string]int, len(g.Nodes))
	for i, n := range g.Nodes {
		at[n.ID] = i
	}

	// A resource may be bound by more than one model; the first binding names it and
	// the rest confirm it, so a node is only ever marked once.
	var added []Node
	seenAdded := map[string]bool{}
	for _, overlay := range overlays {
		for _, element := range overlay.Elements {
			kind, comparable := overlayKind[element.Key]
			if !comparable {
				g.OutOfScope += len(element.Values)
				continue
			}
			for _, value := range element.Values {
				var id string
				switch kind {
				case KindApplication:
					id = applicationNodeID(value)
				case KindProcess:
					id = nodeByProcessID[value]
				case KindWorker:
					id = workerNodeID(value)
				}
				if index, found := at[id]; found && id != "" {
					node := &g.Nodes[index]
					node.Provenance = ProvenanceBoth
					if node.ModelElementID == "" {
						node.ModelElementID, node.ModelElementType = element.ElementID, element.ElementType
						node.ModelName = element.Name
					}
					continue
				}
				absent := overlayNodeID(kind, value)
				if seenAdded[absent] {
					continue
				}
				seenAdded[absent] = true
				added = append(added, Node{
					ID: absent, Kind: kind, Name: element.Name, Provenance: ProvenanceModeled,
					ModelElementID: element.ElementID, ModelElementType: element.ElementType,
					ModelName: element.Name,
				})
			}
		}
	}
	sort.SliceStable(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	g.Nodes = append(g.Nodes, added...)
	g.Modeled = len(added)

	// Unmodeled counts what Atlas has that nothing wrote down. Placeholders are not
	// counted: a restricted node stands for something whose model status this caller
	// cannot know, and an unresolved one is not a resource at all.
	for _, n := range g.Nodes {
		if n.Provenance != ProvenanceDerived {
			continue
		}
		if n.Kind == KindRestricted || n.Kind == KindUnresolved {
			continue
		}
		g.Unmodeled++
	}
}

// cluster collapses an over-budget graph to its applications, recording how many
// nodes each one stands for. It answers with less rather than with a picture the
// browser cannot lay out, and Clustered says which of the two happened.
func cluster(full Graph, visible []Process, appIDs []string, apps map[string]Application,
	partial bool) Graph {
	children := map[string]int{}
	// Severity survives the collapse: an application hiding a critical process
	// behind a count would be a worse picture than no picture. It is aggregated from
	// the processes themselves rather than from the full graph's nodes, because the
	// collapsed children are not in the result to be pointed at — which is also why
	// the reason says how many were collapsed instead of naming one.
	worst := map[string]Process{}
	for _, p := range visible {
		if _, ok := apps[p.ApplicationID]; !ok {
			continue
		}
		children[p.ApplicationID]++
		if have, seen := worst[p.ApplicationID]; !seen ||
			severityRank[severityOf(p.State)] > severityRank[severityOf(have.State)] {
			worst[p.ApplicationID] = p
		}
	}
	out := Graph{
		Nodes: []Node{}, Edges: []Edge{},
		Restricted: full.Restricted, Clustered: true,
	}
	for _, id := range appIDs {
		node := Node{
			ID: applicationNodeID(id), Kind: KindApplication, Name: apps[id].Name,
			Provenance: ProvenanceDerived, Children: children[id],
		}
		if p, ok := worst[id]; ok && p.State != "" {
			node.State = p.State
			node.Reason = fmt.Sprintf("worst of %d collapsed process(es): %s", children[id], p.Reason)
		}
		out.Nodes = append(out.Nodes, node)
	}
	applyStatus(&out, partial)
	return out
}
