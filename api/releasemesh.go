package api

import (
	"cmp"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
)

// The release lens (ADR-0495 §4, slice F): one release's recorded architecture drawn
// with Panorama's starmap renderer instead of the live landscape. The route answers the
// graph the starmap already reads (nodes, edges, restricted, observedAt, status), built
// from the release's snapshot as this reader may see it, so the renderer's show/hide,
// path drill and saved views work on it unchanged; what it adds is the kinds a snapshot
// records and the live landscape does not draw.
//
// It is a separate route rather than a parameter of /api/v1/panorama/mesh, because that
// one is also a peer contract (ADR-0402) and its graph means "this server's resources
// now". This one means "what this release recorded", and the optional live overlay
// lays the current state of the release's own processes and workers over it, never
// anything the release did not record.

// Node kinds the lens adds to the starmap's vocabulary. The kinds both pictures draw
// (application, process, definition, worker, decision, unresolved, restricted,
// catalog, product) keep the live mesh's spelling and meaning.
const (
	lensKindMessage        = "message"
	lensKindSignal         = "signal"
	lensKindJobType        = "jobType"
	lensKindFeed           = "feed"
	lensKindEndpoint       = "endpoint"
	lensKindCapability     = "capability"
	lensKindValueStream    = "valueStream"
	lensKindRole           = "role"
	lensKindDataObject     = "dataObject"
	lensKindBusinessObject = "businessObject"
)

// Edge kinds the lens adds; contains, calls, uses and offers keep the live mesh's.
const (
	// lensEdgeReceives runs from a message or signal to a process that receives it.
	lensEdgeReceives = "receives"
	// lensEdgeThrows runs from whatever throws a message or signal to its name: a
	// process, a peer in another application, an inbound subscription's Worker; and
	// from the application to the event feed its incidents go to.
	lensEdgeThrows = "throws"
	// lensEdgeAccesses runs from a process to the data it declares, and from a Worker
	// or job type to the personal data it can read in plaintext.
	lensEdgeAccesses = "accesses"
	// lensEdgeRealizes runs from a data object to the class it keeps, and from a
	// process or Worker to the capability or product it realises.
	lensEdgeRealizes = "realizes"
	// lensEdgeAssigned runs from a role to a process whose tasks or lanes name it.
	lensEdgeAssigned = "assigned"
	// lensEdgeServes runs from a capability to a value stream a stage of which uses it.
	lensEdgeServes = "serves"
	// lensEdgeReaches runs from a Worker to a system it connects to.
	lensEdgeReaches = "reaches"
)

// releaseMeshResp is the starmap graph with the release it was drawn from.
type releaseMeshResp struct {
	panorama.Graph
	Lens releaseLens `json:"lens"`
}

// releaseLens says which release a lens graph shows, when it was recorded, which
// layers it records, and whether the live overlay is laid over it.
type releaseLens struct {
	ApplicationID string             `json:"applicationId"`
	Application   string             `json:"application"`
	ReleaseID     string             `json:"releaseId"`
	Version       int32              `json:"version"`
	TakenAt       int64              `json:"takenAt"`
	Recorded      []archsketch.Layer `json:"recorded"`
	Overlay       bool               `json:"overlay"`
	// OverlayWithheld is set when the overlay was asked for by a caller who is not an
	// operator: the graph is answered as recorded, and says why the state is missing.
	OverlayWithheld bool `json:"overlayWithheld,omitempty"`
}

// liveState is the observation the overlay lays on one process or Worker node, read
// exactly as the live landscape reads it.
type liveState struct {
	state, reason string
	incidents     int
	oldest        int64
	sites         []panorama.IncidentSite
	runtime       *panorama.Runtime
}

// releaseLive is the overlay: the current state of the release's process definitions,
// by deployment key, and of the configured Workers, by name.
type releaseLive struct {
	processes map[uint64]liveState
	workers   map[string]liveState
	partial   bool
}

// handleReleaseMesh answers GET /api/v1/applications/{id}/releases/{version}/mesh: the
// release's snapshot as a starmap graph, under the application's sharing scope and the
// architecture routes' redaction (ADR-0495 §5). ?overlay=1 lays the live state of the
// release's definitions and Workers over it, for an operator: incidents and instance
// counts are what the operator routes serve (ADR-0495 §5's runtime overlay), so anyone
// else is answered the graph as recorded, with the overlay marked withheld rather than
// refused — the lens asks for it by default, and a refusal would cost them the picture.
func (s *Server) handleReleaseMesh(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	version64, err := strconv.ParseInt(r.PathValue("version"), 10, 32)
	if err != nil || version64 <= 0 {
		httpapi.Error(w, http.StatusBadRequest, "invalid release version")
		return
	}
	asked := r.URL.Query().Get("overlay") == "1"
	overlay := asked && mayReach(httpapi.PrincipalFrom(r.Context()), RoleOperator)
	proj, code, msg := s.authorizeProject(r, appID, ScopeRoleViewer)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var live *releaseLive
	rel, code, msg := s.releaseArchitecture(r, appID, int32(version64), nil, func(rel *applicationRelease) (err error) {
		if overlay {
			live, err = s.releaseLive(r, rel)
		}
		return err
	})
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}

	g := releaseGraph(rel.Architecture, rel.Members, appID, proj.Name, live)
	g.ObservedAt = rel.Architecture.TakenAt
	if live != nil {
		// The structure is as old as the snapshot and the state is a moment old; the
		// stamp dates the state, because that is what an overlay is opened for, and the
		// lens carries the recording moment beside it.
		g.ObservedAt = time.Now().Unix()
	}
	httpapi.JSON(w, http.StatusOK, releaseMeshResp{
		Graph: g,
		Lens: releaseLens{
			ApplicationID: appID, Application: proj.Name, ReleaseID: rel.ID, Version: rel.Version,
			TakenAt: rel.Architecture.TakenAt, Recorded: rel.Architecture.Recorded().Layers(), Overlay: live != nil,
			OverlayWithheld: asked && !overlay,
		},
	})
}

// releaseLive reads the overlay for one release. Run-loop goroutine only.
//
// A process is read by the deployment key the release recorded, so the overlay shows
// the version this release shipped and not whatever is newest under the same process
// id; a version no longer deployed is unbound, because nothing runs in it to observe
// and "healthy" would be a claim about nothing. A Worker's state goes only to a reader
// who may see its configuration, the same rule that withholds its endpoint (ADR-0495
// §5): the reason names what is wrong with its configuration.
func (s *Server) releaseLive(r *http.Request, rel *applicationRelease) (*releaseLive, error) {
	parked, partial, err := s.incidentsByDefinition()
	if err != nil {
		return nil, fmt.Errorf("read incidents: %w", err)
	}
	live := &releaseLive{processes: map[uint64]liveState{}, workers: map[string]liveState{}, partial: partial}
	for _, m := range rel.Members {
		if m.Kind != "process" || m.Key == 0 {
			continue
		}
		if _, deployed := s.deployments[m.Key]; !deployed {
			live.processes[m.Key] = liveState{state: panorama.StateUnbound,
				reason: "This version is no longer deployed on this server, so nothing runs in it to observe."}
			continue
		}
		tally := parked[m.Key]
		state, reason := processStatus(tally.Count)
		live.processes[m.Key] = liveState{state: state, reason: reason, incidents: tally.Count,
			oldest: tally.OldestRaisedAt, sites: tally.Sites, runtime: s.processRuntime(m.Key)}
	}

	confWorkers, err := s.connectors.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("read workers: %w", err)
	}
	mayViewWorker := s.workerViewer(r)
	held, polled := s.workerHoldings()
	for _, c := range confWorkers {
		if !mayViewWorker(c.ID) {
			live.workers[c.Name] = liveState{state: panorama.StateUnbound,
				reason: "Its state is shown to whoever may see this worker's configuration."}
			continue
		}
		state, reason := s.workerStatus(c.Kind, c.Name, polled, held)
		live.workers[c.Name] = liveState{state: state, reason: reason}
	}
	return live, nil
}

// lensGraph builds one lens graph: nodes in the order they are first met, each once,
// and edges deduplicated on from, to and kind, as the live mesh deduplicates them.
type lensGraph struct {
	g          panorama.Graph
	at         map[string]int
	edges      map[[3]string]bool
	restricted map[string]string
	app        string
	// procs maps a process id the release shipped to its node.
	procs map[string]string
	// keys maps a process node to the deployment key the release recorded for it.
	keys map[string]uint64
}

func (b *lensGraph) node(n panorama.Node) string {
	if _, ok := b.at[n.ID]; ok {
		return n.ID
	}
	n.Provenance = panorama.ProvenanceDerived
	b.at[n.ID] = len(b.g.Nodes)
	b.g.Nodes = append(b.g.Nodes, n)
	return n.ID
}

func (b *lensGraph) edge(from, to, kind string) {
	if from == "" || to == "" {
		return
	}
	k := [3]string{from, to, kind}
	if b.edges[k] {
		return
	}
	b.edges[k] = true
	b.g.Edges = append(b.g.Edges, panorama.Edge{From: from, To: to, Kind: kind})
}

// placeholder is a restricted node standing for what this reader may not see, one per
// key: the key says what it stands in for, and the node discloses nothing but its kind.
func (b *lensGraph) placeholder(key string) string {
	if id, ok := b.restricted[key]; ok {
		return id
	}
	id := fmt.Sprintf("%s:%d", panorama.KindRestricted, len(b.restricted)+1)
	b.restricted[key] = id
	return b.node(panorama.Node{ID: id, Kind: panorama.KindRestricted})
}

// proc is the node of a process the release shipped, or empty for one it did not, so
// an edge to it is left out rather than drawn to nothing.
func (b *lensGraph) proc(processID string) string { return b.procs[processID] }

func (b *lensGraph) application(id, name string) string {
	if id == "" {
		return ""
	}
	return b.node(panorama.Node{ID: panorama.KindApplication + ":" + id, Kind: panorama.KindApplication, Name: cmp.Or(name, id)})
}

// definition is a process the release did not ship — a call target or a peer at the
// other end of a name — in the application that held it, when one did.
func (b *lensGraph) definition(processID, appID, appName string) string {
	return b.node(panorama.Node{ID: panorama.KindDefinition + ":" + processID, Kind: panorama.KindDefinition,
		Name: processID, ProcessID: processID, Application: b.contained(appID, appName, panorama.KindDefinition+":"+processID)})
}

// contained puts a node into an application's group and returns that application's
// node, or empty when there is none.
func (b *lensGraph) contained(appID, appName, nodeID string) string {
	app := b.application(appID, appName)
	b.edge(app, nodeID, panorama.EdgeContains)
	return app
}

func (b *lensGraph) worker(name, workerType string) string {
	id := b.node(panorama.Node{ID: panorama.KindWorker + ":" + name, Kind: panorama.KindWorker, Name: name, WorkerType: workerType})
	if n := &b.g.Nodes[b.at[id]]; n.WorkerType == "" {
		n.WorkerType = workerType
	}
	return id
}

func (b *lensGraph) name(kind, name string) string {
	k := lensKindMessage
	if kind == archsketch.NameSignal {
		k = lensKindSignal
	}
	return b.node(panorama.Node{ID: k + ":" + name, Kind: k, Name: name})
}

// endpoint is a system a Worker reaches, by scheme, host and port — the whole of what
// a snapshot records of an address — or empty for one this reader may not see or that
// was not readable as an address.
func (b *lensGraph) endpoint(e *archsketch.Endpoint) string {
	if e == nil || e.Withheld || e.Unparseable || e.Host == "" {
		return ""
	}
	addr := e.String()
	return b.node(panorama.Node{ID: lensKindEndpoint + ":" + addr, Kind: lensKindEndpoint, Name: addr})
}

func (b *lensGraph) recipient(kind, name string) string {
	switch kind {
	case archsketch.DependencyWorker:
		return b.worker(name, "")
	case archsketch.DependencyRuntimeWorker:
		return b.node(panorama.Node{ID: panorama.KindUnresolved + ":" + panorama.KindWorker + ":" + name, Kind: panorama.KindUnresolved, Name: name})
	case archsketch.DependencyJobType:
		return b.node(panorama.Node{ID: "jobtype:" + name, Kind: lensKindJobType, Name: name})
	}
	return ""
}

// releaseGraph draws one release's snapshot, already redacted for its reader, as a
// starmap graph, with the overlay laid over it when live is set. It reads nothing but
// its arguments.
//
// Every layer the snapshot records is drawn; the starmap's own show/hide narrows the
// picture, so the graph does not take a layer selection. A process node is keyed by the
// deployment key the release recorded, which is what the live mesh keys it by and what
// its drill link opens in Operations.
func releaseGraph(snap *archsketch.Snapshot, members []releaseMember, appID, appName string, live *releaseLive) panorama.Graph {
	b := &lensGraph{
		g:          panorama.Graph{Nodes: []panorama.Node{}, Edges: []panorama.Edge{}},
		at:         map[string]int{},
		edges:      map[[3]string]bool{},
		restricted: map[string]string{},
		procs:      map[string]string{},
		keys:       map[string]uint64{},
	}
	b.app = b.application(appID, appName)

	keyOf := map[string]uint64{}
	for _, m := range members {
		if m.Kind == "process" {
			keyOf[m.Ref+"\x00"+strconv.Itoa(int(m.ArtifactVer))] = m.Key
		}
	}
	for _, p := range snap.Processes {
		node := panorama.Node{Kind: panorama.KindProcess, Name: cmp.Or(p.Name, p.ProcessID), ProcessID: p.ProcessID,
			Version: p.Version, Application: b.app}
		key := keyOf[p.ProcessID+"\x00"+strconv.Itoa(int(p.Version))]
		if key != 0 {
			node.ID = fmt.Sprintf("%s:%d", panorama.KindProcess, key)
		} else {
			// A member recorded without its deployment key has nothing to open in
			// Operations, so it is drawn as the definition it is.
			node.Kind, node.ID = panorama.KindDefinition, fmt.Sprintf("%s:%s:%d", panorama.KindDefinition, p.ProcessID, p.Version)
		}
		id := b.node(node)
		b.edge(b.app, id, panorama.EdgeContains)
		b.procs[p.ProcessID] = id
		if key != 0 {
			b.keys[id] = key
		}
	}
	for _, m := range members {
		if m.Kind == "decision" {
			id := b.node(panorama.Node{ID: panorama.KindDecision + ":" + m.Ref, Kind: panorama.KindDecision, Name: m.Ref, Application: b.app})
			b.edge(b.app, id, panorama.EdgeContains)
		}
	}

	// The application layer: the ways in, the ways out, and the coupling.
	for _, e := range snap.EntryPoints {
		msg := b.name(archsketch.NameMessage, e.Name)
		for _, u := range e.UsedBy {
			b.edge(msg, b.proc(u.ProcessID), lensEdgeReceives)
		}
	}
	for _, d := range snap.Dependencies {
		var to, kind string
		switch d.Kind {
		case archsketch.DependencyWorker:
			to, kind = b.worker(d.Name, d.WorkerType), panorama.EdgeUses
		case archsketch.DependencyRuntimeWorker, archsketch.DependencyJobType:
			to, kind = b.recipient(d.Kind, d.Name), panorama.EdgeUses
		case archsketch.DependencyDecision:
			to, kind = b.node(panorama.Node{ID: panorama.KindDecision + ":" + d.Name, Kind: panorama.KindDecision, Name: d.Name}), panorama.EdgeUses
		case archsketch.DependencyCall:
			kind = panorama.EdgeCalls
			if d.TargetWithheld {
				to = b.placeholder("call:" + d.Name)
			} else {
				to = b.definition(d.Name, d.TargetApplicationID, d.TargetApplication)
			}
		default:
			continue
		}
		for _, u := range d.UsedBy {
			b.edge(b.proc(u.ProcessID), to, kind)
		}
	}
	if c := snap.Coupling; c != nil {
		for _, n := range c.Names {
			id := b.name(n.Kind, n.Name)
			for _, u := range n.UsedBy {
				if n.Direction == archsketch.DirectionIn {
					b.edge(id, b.proc(u.ProcessID), lensEdgeReceives)
				} else {
					b.edge(b.proc(u.ProcessID), id, lensEdgeThrows)
				}
			}
		}
		for _, p := range c.Peers {
			id := b.name(p.Kind, p.Name)
			var peer string
			switch {
			case p.Withheld:
				// One placeholder per name and direction: a withheld peer keeps nothing
				// that would tell two of them apart.
				peer = b.placeholder("peer:" + p.Direction + ":" + p.Kind + ":" + p.Name)
			case p.ProcessID != "":
				appName := p.Application
				if appName == "" && p.System {
					appName = "Atlas"
				}
				peer = b.definition(p.ProcessID, p.ApplicationID, appName)
			default:
				continue
			}
			if p.Direction == archsketch.DirectionIn {
				b.edge(peer, id, lensEdgeThrows)
			} else {
				b.edge(id, peer, lensEdgeReceives)
			}
		}
		if c.Feed.Subscriptions > 0 || len(c.Feed.Targets) > 0 {
			feed := b.node(panorama.Node{ID: lensKindFeed + ":events", Kind: lensKindFeed, Name: "Event feed"})
			b.edge(b.app, feed, lensEdgeThrows)
			for i, t := range c.Feed.Targets {
				if t.Withheld || t.Worker == "" {
					b.edge(feed, b.placeholder("feed:"+strconv.Itoa(i)), panorama.EdgeUses)
					continue
				}
				wid := b.worker(t.Worker, "")
				b.edge(feed, wid, panorama.EdgeUses)
				b.edge(wid, b.endpoint(t.Endpoint), lensEdgeReaches)
			}
		}
	}

	// The technology layer: what each Worker reaches, and the inbound subscriptions
	// that publish a message the release receives.
	if p := snap.Physical; p != nil {
		for _, w := range p.Workers {
			wid := b.worker(w.Name, w.WorkerType)
			for _, e := range []*archsketch.Endpoint{w.Endpoint, w.MailboxEndpoint, w.ScannerEndpoint} {
				b.edge(wid, b.endpoint(e), lensEdgeReaches)
			}
		}
		for _, in := range p.Inbound {
			if in.Worker == "" {
				continue
			}
			wid := b.worker(in.Worker, in.WorkerType)
			b.edge(wid, b.name(archsketch.NameMessage, in.MessageName), lensEdgeThrows)
			b.edge(wid, b.endpoint(in.Endpoint), lensEdgeReaches)
		}
	}

	// The business layer: the roles that do the work and the products it delivers.
	if bz := snap.Business; bz != nil {
		for _, r := range bz.Roles {
			id := b.node(panorama.Node{ID: "role:" + r.Kind + ":" + r.Name, Kind: lensKindRole, Name: r.Name})
			for _, u := range r.UsedBy {
				b.edge(id, b.proc(u.ProcessID), lensEdgeAssigned)
			}
		}
		for _, p := range bz.Products {
			cat := b.node(panorama.Node{ID: panorama.KindCatalog + ":" + p.CatalogID, Kind: panorama.KindCatalog, Name: cmp.Or(p.CatalogName, p.CatalogID)})
			id := b.node(panorama.Node{ID: panorama.KindProduct + ":" + p.ID, Kind: panorama.KindProduct, Name: p.Name,
				Catalog: cat, ProductState: p.State})
			b.edge(cat, id, panorama.EdgeOffers)
			for _, bind := range p.Bindings {
				b.edge(b.proc(bind.ProcessID), id, lensEdgeRealizes)
			}
		}
	}

	// The strategy layer: the capabilities the release realises and the value streams
	// that use them.
	if st := snap.Strategy; st != nil {
		for _, c := range st.Capabilities {
			id := b.node(panorama.Node{ID: lensKindCapability + ":" + c.Key, Kind: lensKindCapability, Name: c.Name})
			for _, by := range c.RealizedBy {
				switch by.Kind {
				case archsketch.RealizedByProcess:
					b.edge(b.proc(by.ProcessID), id, lensEdgeRealizes)
				case archsketch.RealizedByWorker:
					b.edge(b.worker(by.Worker, ""), id, lensEdgeRealizes)
				}
			}
		}
		for _, v := range st.ValueStreams {
			id := b.node(panorama.Node{ID: "valuestream:" + v.Key, Kind: lensKindValueStream, Name: v.Name})
			for _, stage := range v.Stages {
				for _, key := range stage.Capabilities {
					if capID := lensKindCapability + ":" + key; b.has(capID) {
						b.edge(capID, id, lensEdgeServes)
					}
				}
			}
		}
	}

	// The data layer. A data object is named within its process, a store and a class
	// across the application; personal data is one declaration per process, drawn as
	// the data it declares and never as a value.
	if d := snap.Data; d != nil {
		class := func(name string) string {
			if name == "" {
				return ""
			}
			return b.node(panorama.Node{ID: "businessobject:" + name, Kind: lensKindBusinessObject, Name: name})
		}
		for _, c := range d.Classes {
			class(c.Name)
		}
		for _, o := range d.Objects {
			id := b.node(panorama.Node{ID: "dataobject:" + o.ProcessID + ":" + o.Name, Kind: lensKindDataObject, Name: o.Name})
			b.edge(b.proc(o.ProcessID), id, lensEdgeAccesses)
			b.edge(id, class(o.Class), lensEdgeRealizes)
		}
		for _, st := range d.Stores {
			// Its own prefix rather than a reserved process id under the objects' one:
			// a process may well be called "store".
			id := b.node(panorama.Node{ID: "datastore:" + st.Name, Kind: lensKindDataObject, Name: st.Name})
			for _, u := range st.UsedBy {
				b.edge(b.proc(u.ProcessID), id, lensEdgeAccesses)
			}
			b.edge(id, class(st.Class), lensEdgeRealizes)
		}
		for _, p := range d.Personal {
			id := b.node(panorama.Node{ID: "personaldata:" + p.ProcessID, Kind: lensKindDataObject, Name: "Personal data of " + p.ProcessID})
			b.edge(b.proc(p.ProcessID), id, lensEdgeAccesses)
			for _, to := range p.HandedTo {
				b.edge(b.recipient(to.Kind, to.Name), id, lensEdgeAccesses)
			}
		}
	}

	if live != nil {
		for i := range b.g.Nodes {
			n := &b.g.Nodes[i]
			var (
				st liveState
				ok bool
			)
			switch n.Kind {
			case panorama.KindProcess:
				st, ok = live.processes[b.keys[n.ID]]
			case panorama.KindWorker:
				st, ok = live.workers[n.Name]
				if !ok {
					st, ok = liveState{state: panorama.StateNotReady,
						reason: "No worker of this name is configured on this server now, so a task handed to it would fail at run time."}, true
				}
			}
			if ok {
				n.State, n.Reason, n.Incidents, n.OldestIncident, n.Sites, n.Runtime = st.state, st.reason, st.incidents, st.oldest, st.sites, st.runtime
			}
		}
	}
	b.g.Restricted = len(b.restricted)
	panorama.ApplyStatus(&b.g, live != nil && live.partial)
	b.g.Status.Unavailable = lensUnavailable(live != nil)
	return b.g
}

// lensUnavailable is what a lens graph cannot say, in its own terms. The landscape's
// answer speaks of deployment targets, and configuring one would not make a release's
// picture report anything: no target is part of a release. Without the overlay
// nothing is observed at all; with it, the release's processes and Workers are read
// from this server's own state as the request is served, so nothing on the lens can be
// unreachable or stale.
func lensUnavailable(overlay bool) []panorama.UnavailableState {
	if !overlay {
		const reason = "Nothing on this picture is observed: without the live overlay it is the release as it was recorded."
		out := []panorama.UnavailableState{}
		for _, st := range []string{panorama.StateHealthy, panorama.StateDegraded, panorama.StateNotReady, panorama.StateUnreachable, panorama.StateStale} {
			out = append(out, panorama.UnavailableState{State: st, Reason: reason})
		}
		return out
	}
	const reason = "The live overlay reads the release's own processes and Workers from this server's state when the request is served: " +
		"nothing on it is reached over the network, and no observation on it has a freshness contract to exceed."
	return []panorama.UnavailableState{{State: panorama.StateUnreachable, Reason: reason}, {State: panorama.StateStale, Reason: reason}}
}

func (b *lensGraph) has(id string) bool { _, ok := b.at[id]; return ok }
