package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/compiler"
)

// meshMaxNodes is the landscape mesh's size budget (ADR-0211 §7). Over it the
// graph collapses to applications and says so, rather than handing the browser a
// graph it cannot lay out.
//
// The number is measured, not guessed. The browser-side layout is an all-pairs
// force simulation, so its cost is quadratic in node count; timed in Chromium
// (e2e/panorama-mesh.spec.mjs, "stays inside its size budget"):
//
//	200 nodes / 370 edges   148 ms layout,  486 ms to first paint
//	400 nodes / 740 edges   438 ms layout, 1036 ms to first paint
//	800 nodes / 1480 edges 1385 ms layout, 2203 ms to first paint
//
// 400 is the last size that still paints in about a second on a warm machine.
// Raising it is a decision about the layout algorithm — Barnes-Hut, or the
// server-side pipeline in api/layout (ADR-0124/0127) — not about this constant.
const meshMaxNodes = 400

// meshCacheTTL is how long one reading of this server's *structure* answers for.
//
// The Starmap is derived on the run loop, which is the single writer (I3), and a
// landscape costs a directory listing and a JSON decode per record across four
// sidecar stores plus a walk of every compiled process. One reader paid that; twenty
// readers paid it twenty times, and twenty tabs on a large estate is not a
// hypothetical — it is one operations team with the Starmap open, competing for the
// loop that executes process instances.
//
// Thirty seconds is not a guess. It is the floor the view itself re-reads on
// (REFRESH_FLOOR in panorama-mesh.js), and a TTL shorter than that bounds nothing:
// readers do not poll in step, so twenty of them at random phases would miss a
// five-second entry almost every time. At the view's own floor, the cost of the
// structure stops depending on how many people are looking at it, which is the whole
// property worth having.
const meshCacheTTL = 30 * time.Second

// meshFacts is one reading of what this server *is*: its applications, its deployed
// processes and what each one calls, its workers, its drafts, its peers.
//
// What is cached is structure, and nothing else. Two things are deliberately left
// out, for two different reasons, and the split is what makes a cache safe here at
// all:
//
//   - **Status is never cached.** Which processes have work parked, how much, how
//     long it has been standing, how many instances are live, which workers have
//     polled — every one of those is read fresh on every request. They are the
//     answer an operator opens this view for, and they are engine point reads rather
//     than disk: bounded by design (maxStatusIncidentScan) and cheap beside a
//     directory listing. A status view that made trouble wait out a TTL would be
//     saving the wrong cost, and a reload that could not tell the truth is worse than
//     a slow one.
//   - **Visibility is never cached.** Every CanView is decided on the request, from
//     the request, in collectLandscape below. A cache holding a *filtered* landscape
//     would be one principal's view served to another the moment a key collided or a
//     scope changed, and there is no cache key that makes that safe — so nothing
//     here holds one.
//
// The owner of each thing is carried beside it rather than on the landscape types,
// because an owner id is not the landscape's business: everything on a
// [panorama.Application] is a candidate for the wire.
type meshFacts struct {
	// at is when this structure was read. It dates the answer built from it — the
	// oldest fact in that answer, which is what a freshness stamp has to be.
	at        time.Time
	projs     map[string]project
	overrides map[string]callOverride
	workers   []connector
	byName    map[string]connector
	decisions []panorama.Decision
	procs     []structuralProcess
	drafts    []structuralDraft
	peers     []remoteTarget
	targets   []panorama.Target
}

// structuralProcess is one deployed process as the compiled graph describes it:
// everything about it that changes only when somebody deploys.
type structuralProcess struct {
	key        uint64
	processID  string
	name       string
	version    int32
	projectID  string
	deployedBy string
	calls      []panorama.Call
	workers    []panorama.WorkerUse
	decisions  []string
}

// structuralDraft is one saved-but-undeployed diagram, and who governs it.
type structuralDraft struct {
	processID string
	name      string
	projectID string
	ownerID   string
}

// meshCollection is the cache: one entry per shape of the question, which is only
// whether drafts were asked for. Loop-owned state — every reader and every writer of
// it runs inside the run-loop turn collectLandscape is called on — so it needs no
// lock, and the loop is also the single-flight: two readers arriving together are two
// turns, and the second finds what the first left.
type meshCollection struct {
	deployed *meshFacts
	drafted  *meshFacts
}

// forgetLandscape drops what has been read, so the next caller pays for a fresh look.
// Run-loop goroutine only.
//
// It is called from the changes that alter the *shape* of the landscape — a
// deployment arriving or being removed, a call override written or dropped — and that
// narrowness is deliberate rather than an oversight. A general invalidation protocol
// is one every future writer has to remember, and the writer who forgets it produces
// a landscape that is silently wrong; a TTL everybody is subject to cannot be
// forgotten. These get a hook because they are the changes a person makes and then
// immediately goes looking for on the picture. A new application or worker appears
// within the TTL, and the picture says how old it is while it waits.
func (s *Server) forgetLandscape() {
	s.landscapes = meshCollection{}
}

// landscapeFacts is the structural reading above, cached. Run-loop goroutine only.
func (s *Server) landscapeFacts(withDrafts bool, now time.Time) (*meshFacts, error) {
	held := s.landscapes.deployed
	if withDrafts {
		held = s.landscapes.drafted
	}
	if held != nil && now.Sub(held.at) < meshCacheTTL {
		return held, nil
	}
	fresh, err := s.readStructure(withDrafts, now)
	if err != nil {
		return nil, err
	}
	if withDrafts {
		s.landscapes.drafted = fresh
	} else {
		s.landscapes.deployed = fresh
	}
	return fresh, nil
}

// readStructure reads what this server *is*, as the cached half of a landscape
// (ADR-0211 §1). Run-loop goroutine only: it reads the project store, the
// call-override store, the worker store, the deployment registry and the target
// store, and walks every compiled process for what it calls and what it uses.
//
// It decides nothing about health and nothing about who may look — see [meshFacts]
// for why both are left to the request.
//
// It does not enumerate every deployed version either: the landscape is the current
// state, so one node per process id at its current version, which is also what keeps
// a server with a long deploy history inside the size budget.
func (s *Server) readStructure(withDrafts bool, now time.Time) (*meshFacts, error) {
	projs, err := s.projectsByID()
	if err != nil {
		return nil, err
	}
	overrides, err := s.callOverrides.LoadAll()
	if err != nil {
		return nil, err
	}
	ovByPID := make(map[string]callOverride, len(overrides))
	for _, rec := range overrides {
		ovByPID[rec.CalledProcessID] = rec
	}

	// Configured workers still live in the worker store, whose record still spells
	// the Worker Type Kind — names ADR-0203 leaves in place until the packages move.
	// What the mesh emits is Worker vocabulary.
	confWorkers, err := s.connectors.LoadAll()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]connector, len(confWorkers))
	for _, w := range confWorkers {
		byName[w.Name] = w
	}

	facts := &meshFacts{
		at: now, projs: projs, overrides: ovByPID,
		workers: confWorkers, byName: byName,
	}

	// Deployed decisions are engine-wide rather than owned by any application
	// (ADR-0034), so there is no scope to apply and CanView is simply true. Saying
	// that here is better than leaving a reader to wonder which filter was forgotten.
	for _, d := range s.dmnRegistry.DeployedDecisions() {
		facts.decisions = append(facts.decisions, panorama.Decision{
			ID: d.ID, Name: d.Name, CanView: true,
		})
	}

	for pid := range s.versions {
		d := s.latestDeploymentByProcessID(pid)
		if d == nil || d.cp == nil {
			continue
		}
		proc := structuralProcess{
			key: d.Key, processID: d.ProcessID, name: d.Name, version: d.Version,
			projectID: d.ProjectID, deployedBy: d.DeployedBy,
			workers:   workerUses(d.cp, byName),
			decisions: d.cp.BusinessRuleDecisions(),
		}
		for _, ref := range d.cp.CallActivities() {
			call := panorama.Call{ElementID: ref.ElementId, CalledProcessID: ref.CalledProcessId}
			// Resolution mirrors the call-activity management view exactly, overrides
			// included: an edge that ignored a redirect or a pin would draw a
			// dependency the engine would not take (ADR-0076/0105).
			var ovPtr *callOverride
			if ov, ok := ovByPID[ref.CalledProcessId]; ok {
				ovCopy := ov
				ovPtr = &ovCopy
			}
			if target := s.resolveEffectiveTarget(ref.CalledProcessId, ovPtr); target != nil {
				call.TargetKey = target.Key
			}
			proc.calls = append(proc.calls, call)
		}
		facts.procs = append(facts.procs, proc)
	}

	// Drafts, only when the caller asked for them (ADR-0211 §7). It is the one thing
	// that changes what is *read* rather than who may see it, which is why it is the
	// cache's only key: two shapes of the question, two entries.
	//
	// A draft whose process id is already deployed is skipped: it is the editable
	// copy of a process that is on the picture already, and drawing both would put a
	// twin beside every node without saying anything true about either.
	//
	// LoadAll reads and decodes every draft file, XML included, on the run loop. That
	// is the same read the Modeler's own draft list already makes on every visit, so
	// it is a cost this server pays either way rather than a new one — and it is paid
	// only by a caller who asked for drafts, which is the other half of why the
	// parameter exists. Nothing of the XML reaches the landscape: a [panorama.Draft]
	// carries an id, a name and a folder, and the derivation has no field to put a
	// model in even if it wanted one.
	if withDrafts {
		deployed := make(map[string]bool, len(facts.procs))
		for _, p := range facts.procs {
			deployed[p.processID] = true
		}
		saved, err := s.drafts.LoadAll()
		if err != nil {
			return nil, err
		}
		for _, d := range saved {
			if deployed[d.ProcessID] {
				continue
			}
			facts.drafts = append(facts.drafts, structuralDraft{
				processID: d.ProcessID, name: d.Name,
				projectID: d.ProjectID, ownerID: d.OwnerID,
			})
		}
	}

	// The peers, named here and asked off the loop. Naming them on the loop is what
	// the two halves are for: the target list and the credential each one presents
	// are loop reads, and the call that uses them is the one thing that must never
	// hold the single writer (I3).
	targets, err := s.targets.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		// A row every target gets whether or not the fan-out reaches it. If the
		// request is cancelled before the answers come back, the landscape still
		// draws the peer and says it was not asked — which is a different thing from
		// unreachable, and from a landscape quietly one peer short.
		facts.targets = append(facts.targets, panorama.Target{
			ID: t.ID, Name: t.Name,
			State:  panorama.StateUnbound,
			Reason: "This target was not asked.",
		})
		// Resolved on the loop because reading the vault is a loop read. It travels
		// no further than the Authorization header the fan-out sets, and reaches no
		// payload, no log line and no error message.
		facts.peers = append(facts.peers, remoteTarget{
			target: t, credential: s.resolveConnectorSecret(t.CredentialRef),
		})
	}
	return facts, nil
}

// collectLandscape is the landscape this caller gets: the structure above, which may
// have been read a few seconds ago, plus the health of it, which never was.
//
// Run-loop goroutine only. Everything it does itself is an engine read or a pure
// decision over what is already in memory — no disk — which is what makes running it
// on every request affordable, and what makes the answer's *status* always current.
//
// It builds every slice rather than writing through the cached ones. The facts are
// shared by every reader inside the TTL, so setting CanView or a state on them would
// be both a data race and the leak the split exists to prevent: the next caller would
// inherit the last one's answer.
//
// It invents no visibility rule. Applications defer to their sharing scope
// (ADR-0071), a deployment to its project falling back to its deployer, a draft to
// its project falling back to its owner, and a worker to connectorRole — exactly the
// rules every other listing applies.
func (s *Server) collectLandscape(r *http.Request) (panorama.Landscape, panorama.ReachOut, error) {
	facts, err := s.landscapeFacts(r.URL.Query().Get("drafts") == "1", time.Now())
	if err != nil {
		return panorama.Landscape{}, nil, err
	}

	// Parked work is counted once for the whole landscape rather than per process:
	// the alternative is one scan per definition, which on an instance with a few
	// hundred processes is the difference between a view and an outage.
	parked, partial, err := s.incidentsByDefinition()
	if err != nil {
		return panorama.Landscape{}, nil, err
	}

	principal := httpapi.PrincipalFrom(r.Context())
	held, polled := s.workerHoldings()
	land := panorama.Landscape{
		PartialStatus: partial,
		// Dated by the oldest fact in it. The health below was read a moment ago, the
		// structure it hangs on may be half a minute old, and a stamp that claimed the
		// younger of the two would be the picture promising a freshness nobody
		// measured (ADR-0211 §10).
		ObservedAt: facts.at.Unix(),
		Decisions:  facts.decisions,
		Targets:    append([]panorama.Target(nil), facts.targets...),
	}
	for _, w := range facts.workers {
		state, reason := s.workerStatus(w.Kind, w.Name, polled, held)
		land.Workers = append(land.Workers, panorama.Worker{
			ID: w.ID, Name: w.Name, Type: w.Kind,
			CanView: scopeRank(connectorRole(w, principal, s.authEnabled)) >= scopeRank(ScopeRoleViewer),
			// Carried so the derivation can be tested for never emitting them.
			Endpoint: w.Endpoint, CredentialsRef: w.CredentialsRef,
			State: state, Reason: reason,
		})
	}
	for _, p := range facts.projs {
		land.Applications = append(land.Applications, panorama.Application{
			ID: p.ID, Name: p.Name,
			CanView: s.canViewArtifact(r, p.ID, p.OwnerID, facts.projs),
		})
	}
	for _, p := range facts.procs {
		tally := parked[p.key]
		state, reason := processStatus(tally.Count)
		land.Processes = append(land.Processes, panorama.Process{
			Key: p.key, ProcessID: p.processID, Name: p.name, Version: p.version,
			ApplicationID: p.projectID,
			CanView:       s.canViewArtifact(r, p.projectID, p.deployedBy, facts.projs),
			State:         state, Reason: reason,
			Incidents: tally.Count, OldestIncident: tally.OldestRaisedAt, Sites: tally.Sites,
			Runtime:   s.processRuntime(p.key),
			Calls:     p.calls,
			Workers:   p.workers,
			Decisions: p.decisions,
		})
	}
	for _, d := range facts.drafts {
		land.Drafts = append(land.Drafts, panorama.Draft{
			ProcessID: d.processID, Name: d.name, ApplicationID: d.projectID,
			// The same rule the draft list applies: membership inherits from the
			// artifact's project, falling back to its owner (ADR-0071).
			CanView: s.canViewArtifact(r, d.projectID, d.ownerID, facts.projs),
		})
	}

	peers := facts.peers
	return land, func(ctx context.Context, out *panorama.Landscape) {
		s.observeLandscapeTargets(ctx, peers, out)
	}, nil
}

// observeLandscapeTargets asks every peer who it is and folds the answer into the
// landscape's target rows.
//
// Off the run loop. It shares [remoteFacts] with the observation projection rather
// than deciding for itself what a silent peer means, so the landscape and the model
// overlay cannot come to disagree about whether a target is stale or unreachable —
// they read the same cache through the same rules.
func (s *Server) observeLandscapeTargets(ctx context.Context, peers []remoteTarget,
	land *panorama.Landscape) {
	if len(peers) == 0 {
		return
	}
	live := make(map[string]bool, len(peers))
	for _, p := range peers {
		live[p.target.ID] = true
	}
	s.remoteNodes.retain(live)

	answers := make([]panorama.Fact, len(peers))
	var wg sync.WaitGroup
	gate := make(chan struct{}, maxRemoteNodeConcurrency)
	for i, peer := range peers {
		wg.Add(1)
		go func(i int, peer remoteTarget) {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			obs := s.remoteNodeObservation(ctx, peer)
			// Written to its own slot rather than through a shared map: one index per
			// peer needs no lock, and a peer that is down must not be able to affect
			// what any other peer's row says.
			answers[i], _, _ = remoteFacts(peer.target, obs, time.Now())
		}(i, peer)
	}
	wg.Wait()

	byID := make(map[string]panorama.Fact, len(peers))
	for i, peer := range peers {
		byID[peer.target.ID] = answers[i]
	}
	for i := range land.Targets {
		if fact, ok := byID[land.Targets[i].ID]; ok {
			land.Targets[i].State, land.Targets[i].Reason = fact.State, fact.Reason
		}
	}
}

// workerUses resolves a process's worker references — the names its tasks state in
// connector="…" — against the configured workers, mirroring the deploy-time check in
// connectorWarnings, including the two references that are deliberately *not*
// findings there, because treating them as findings here would put false "not
// configured" nodes on the landscape:
//
//   - a reference whose job type no managed Worker Type claims is not a worker
//     reference at all (a local decision names its worker field the same way); and
//   - a name authored as a FEEL expression (entra, ADR-0172) names no fixed worker —
//     which one it reaches is known only at call time, so there is nothing on this
//     server to resolve it against.
//
// Run-loop goroutine only, via its caller.
func workerUses(cp *compiler.CompiledProcess, byName map[string]connector) []panorama.WorkerUse {
	var out []panorama.WorkerUse
	for _, ref := range cp.ConnectorRefs() {
		if connectorKindOfJobType(ref.JobType) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ref.Connector), "=") {
			continue
		}
		use := panorama.WorkerUse{ElementID: ref.ElementId, Name: ref.Connector}
		if rec, ok := byName[ref.Connector]; ok {
			use.TargetID = rec.ID
		}
		out = append(out, use)
	}
	return out
}

// processRuntime is one definition's instance tally for the Starmap (ADR-0083's
// summary columns), or nil when the engine could not report it.
//
// Three point reads of counters the engine already maintains, not a scan: the whole
// reason instance counts can be on every node of a four-hundred-node picture is that
// asking costs O(1) per definition. Counting instances instead would be one scan
// each, which is the mistake the parked-work tally is collected once to avoid.
//
// A read that fails returns nil rather than zero. "Nothing has ever run here" and
// "the counter could not be read" are different facts, and a picture that showed the
// second as the first would report a quiet estate on no evidence.
func (s *Server) processRuntime(defKey uint64) *panorama.Runtime {
	running, err := s.store.DefInstanceCount(defKey)
	if err != nil {
		return nil
	}
	finished, err := s.store.DefCompletedCount(defKey)
	if err != nil {
		return nil
	}
	// The timestamp is the one field allowed to be missing on its own: a definition
	// nobody has ever started has no last activity, and that is an answer rather than
	// a failure.
	last, err := s.store.DefLastActivity(defKey)
	if err != nil {
		last = 0
	}
	return &panorama.Runtime{Running: running, Finished: finished, LastActivity: last}
}
