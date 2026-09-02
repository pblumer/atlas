package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/model"
)

// The local half of Panorama's observation projection (ADR-0189 §6, P4b).
//
// Every fact here is read from this server's own state while the request is being
// served. Nothing is cached and nothing is polled, which is what lets the document
// carry one timestamp for the whole of itself and why *stale* is still not a state
// this build can produce — staleness needs a source outside the process and a
// freshness contract to exceed, and both arrive with the remote slice.
//
// The scope rule is the one every other Panorama read follows: a fact about a
// resource this caller may not see is not collected, so the observation projection
// cannot become a way around a sharing scope (ADR-0071). A binding to such a
// resource then reports unbound with the same words as a binding to a resource
// that is genuinely absent — which is the correct disclosure, because the two are
// exactly what this caller is allowed to be unable to tell apart.
//
// Run-loop goroutine only.

// collectFacts gathers what this server can currently say about the resources a
// model may bind to, in two phases.
//
// Phase one runs on the run loop and reads this server's own state. Phase two runs
// off it and asks the peers, because a remote call must not hold the single writer
// (I3) — one unresponsive target would otherwise stall every request on this
// server, which is a far worse failure than an incomplete architecture view.
//
// Splitting it this way is why [panorama.FactsResolver] is called off the loop and
// takes its own turn: the boundary belongs where the network is, not at the
// handler.
func (s *Server) collectFacts(r *http.Request) (panorama.Facts, error) {
	var (
		facts panorama.Facts
		peers []remoteTarget
		err   error
		ran   bool
	)
	s.do(func() { ran = true; facts, peers, err = s.collectLocalFacts(r) })
	if !ran {
		return panorama.Facts{}, panorama.ErrShuttingDown
	}
	if err != nil {
		return panorama.Facts{}, err
	}
	// Off the loop from here. A peer that is down is a row that says so, never an
	// error that empties the document.
	s.observeRemoteNodes(r.Context(), peers, facts)
	return facts, nil
}

// collectLocalFacts is phase one: everything readable from this server's own
// state, plus the peers phase two will ask and the credentials to present. The
// credentials are resolved here because reading the vault is a run-loop read; they
// never leave this process except as an Authorization header.
//
// Run-loop goroutine only.
func (s *Server) collectLocalFacts(r *http.Request) (panorama.Facts, []remoteTarget, error) {
	projs, err := s.projectsByID()
	if err != nil {
		return panorama.Facts{}, nil, err
	}
	parked, partial, err := s.incidentsByDefinition()
	if err != nil {
		return panorama.Facts{}, nil, err
	}
	live, err := s.instancesByDefinition()
	if err != nil {
		return panorama.Facts{}, nil, err
	}

	facts := panorama.Facts{
		Applications: map[string]panorama.Fact{},
		Processes:    map[string]panorama.Fact{},
		Connectors:   map[string]panorama.Fact{},
		Runtimes:     map[string]panorama.Fact{},
		Targets:      map[string]panorama.Fact{},
		Releases:     map[string]panorama.Fact{},
		// JobTypes stays absent, and the reason is no longer the one that used to be
		// written here. It said a job type is not registered as a resource, which is
		// untrue — the engine keeps a table of them, and the binding catalog beside
		// this now resolves against it. What is missing is a decision, not a lookup:
		// a job type is a *kind of work*, not a thing that can be well or unwell, and
		// what an observation of one would assert is its own question. The engine can
		// say how many are queued, whether it runs the kind itself and how many jobs
		// of it are parked behind an incident — but "healthy" for a kind of work,
		// where no worker having polled this run is not evidence that none exists
		// (the worker registry does not survive a restart), is a mapping somebody has
		// to choose. Until then the map is absent, which reports "not produced here"
		// rather than "nothing is wrong".
	}

	// Processes, and the per-application totals they roll up into. The map holds
	// values rather than pointers so an application with nothing deployed reads as
	// the zero record — which is a real state, not a missing one.
	totals := map[string]appTotals{}
	for pid := range s.versions {
		d := s.latestDeploymentByProcessID(pid)
		if d == nil {
			continue
		}
		if !s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs) {
			continue
		}
		state, reason := processStatus(parked[d.Key])
		facts.Processes[pid] = panorama.Fact{
			Source: panorama.SourceDeployments, State: state, Reason: reason,
			Detail: map[string]string{
				"version":       strconv.FormatInt(int64(d.Version), 10),
				"instances":     strconv.Itoa(live[d.Key]),
				"parkedTokens":  strconv.Itoa(parked[d.Key]),
				"definitionKey": strconv.FormatUint(d.Key, 10),
			},
		}
		if d.ProjectID == "" {
			continue
		}
		t := totals[d.ProjectID]
		t.processes++
		t.instances += live[d.Key]
		t.incidents += parked[d.Key]
		totals[d.ProjectID] = t
	}

	for _, p := range projs {
		if !s.canViewArtifact(r, p.ID, p.OwnerID, projs) {
			continue
		}
		t := totals[p.ID]
		facts.Applications[p.ID] = applicationFact(t.processes, t.instances, t.incidents)
	}

	if err := s.collectWorkerFacts(r, facts.Connectors); err != nil {
		return panorama.Facts{}, nil, err
	}
	peers, err := s.collectRuntimeAndTargetFacts(facts)
	if err != nil {
		return panorama.Facts{}, nil, err
	}
	if err := s.collectReleaseFacts(r, projs, facts.Releases); err != nil {
		return panorama.Facts{}, nil, err
	}

	// A truncated incident scan makes every "no work is parked" a floor rather than
	// a verdict, so it is said on the observations it affects rather than buried in
	// a document-level flag nobody reads.
	if partial {
		for id, fact := range facts.Processes {
			if fact.State == panorama.StateHealthy {
				fact.Reason += " Counting parked work stopped at its bound, so this is a floor."
				facts.Processes[id] = fact
			}
		}
	}
	return facts, peers, nil
}

// appTotals is what one application's deployed processes add up to.
type appTotals struct{ processes, instances, incidents int }

// applicationFact turns an application's totals into one observation.
//
// An application with nothing deployed is *not ready* rather than healthy: the
// model says this application exists and the instance has nothing of it running,
// which is the desired-versus-observed gap the whole projection is for. It is not
// critical — nothing is broken, something is simply absent — but calling it
// healthy would be the view vouching for an application that cannot do any work.
func applicationFact(processes, instances, incidents int) panorama.Fact {
	detail := map[string]string{
		"processes":    strconv.Itoa(processes),
		"instances":    strconv.Itoa(instances),
		"parkedTokens": strconv.Itoa(incidents),
	}
	switch {
	case processes == 0:
		return panorama.Fact{
			Source: panorama.SourceDeployments, State: panorama.StateNotReady,
			Reason: "Nothing of this application is deployed here, so it can do no work.",
			Detail: detail,
		}
	case incidents > 0:
		return panorama.Fact{
			Source: panorama.SourceInstances, State: panorama.StateDegraded,
			Reason: fmt.Sprintf("%d token(s) are parked behind an unresolved incident across %d process(es).",
				incidents, processes),
			Detail: detail,
		}
	default:
		return panorama.Fact{
			Source: panorama.SourceDeployments, State: panorama.StateHealthy,
			Reason: fmt.Sprintf("%d process(es) deployed, %d live instance(s), nothing parked.",
				processes, instances),
			Detail: detail,
		}
	}
}

// collectWorkerFacts observes the configured workers, reusing exactly the
// derivation the landscape mesh uses (ADR-0211 §4). Two surfaces answering the
// same question differently is worse than either answer alone.
func (s *Server) collectWorkerFacts(r *http.Request, into map[string]panorama.Fact) error {
	confWorkers, err := s.connectors.LoadAll()
	if err != nil {
		return err
	}
	held, polled := s.workerHoldings()
	principal := httpapi.PrincipalFrom(r.Context())
	for _, w := range confWorkers {
		if scopeRank(connectorRole(w, principal, s.authEnabled)) < scopeRank(ScopeRoleViewer) {
			continue
		}
		state, reason := s.workerStatus(w.Kind, w.Name, polled, held)
		// Name and Worker Type only. The record also holds an endpoint and a
		// credential reference, and neither belongs in a document a modeler opens
		// (ADR-0211 §10, I6).
		into[w.ID] = panorama.Fact{
			Source: panorama.SourceWorkers, State: state, Reason: reason,
			Detail: map[string]string{"name": w.Name, "workerType": w.Kind},
		}
	}
	return nil
}

// collectRuntimeAndTargetFacts observes this node and returns the peers phase two
// will ask about the rest.
//
// Run-loop goroutine only: it reads the node identity, the target store and the
// vault.
func (s *Server) collectRuntimeAndTargetFacts(facts panorama.Facts) ([]remoteTarget, error) {
	node, err := s.nodeIdentity()
	if err != nil {
		return nil, err
	}
	// This server is answering, so it is healthy by the only evidence that matters
	// for a runtime: it is here. Any *other* runtime id is absent from the map and
	// reports unbound, which is true — reaching one needs a call this build does
	// not make.
	facts.Runtimes[node.ID] = panorama.Fact{
		Source: panorama.SourceNode, State: panorama.StateHealthy,
		Reason: "This is the server answering the request.",
		Detail: map[string]string{
			"name":      nodeDisplayName(node),
			"version":   Version,
			"partition": strconv.FormatInt(int64(s.proc.Partition()), 10),
		},
	}

	targets, err := s.targets.LoadAll()
	if err != nil {
		return nil, err
	}
	peers := make([]remoteTarget, 0, len(targets))
	for _, t := range targets {
		// A placeholder that phase two overwrites. It is written here anyway so a
		// target always has a row: if the fan-out is cut short — a cancelled
		// request, a context that expired — the document still lists the target and
		// says nothing is known about it, rather than dropping it and reading as an
		// architecture with one fewer peer than it has.
		facts.Targets[t.ID] = panorama.Fact{
			Source: panorama.SourceRemote, State: panorama.StateUnbound,
			Reason: "This target was not asked.",
			Detail: map[string]string{"target": t.Name},
		}
		// The credential is resolved on the loop, because reading the vault is a
		// loop read. It travels no further than the Authorization header phase two
		// sets, and reaches no payload, no log line and no error message.
		peers = append(peers, remoteTarget{target: t, credential: s.resolveConnectorSecret(t.CredentialRef)})
	}
	return peers, nil
}

// collectReleaseFacts is the desired-versus-observed comparison ADR-0189 §6 names
// in its own words. A release records the exact artifact versions that shipped, so
// this server can say whether what shipped is what is running — the one question a
// drawing of an architecture can never answer about itself.
func (s *Server) collectReleaseFacts(r *http.Request, projs map[string]project,
	into map[string]panorama.Fact) error {
	for _, p := range projs {
		if !s.canViewArtifact(r, p.ID, p.OwnerID, projs) {
			continue
		}
		releases, err := s.releases.forApplication(p.ID)
		if err != nil {
			return err
		}
		for _, rel := range releases {
			into[rel.ID] = s.releaseFact(rel)
		}
	}
	return nil
}

// releaseFact compares one release's members against what is deployed now.
//
// Three answers, and the middle one is the point of the whole projection:
//
//   - every member is still the current version — this release is what is running;
//   - some member has been superseded — the instance has moved on from what this
//     release shipped, which is drift the model cannot see and the release record
//     cannot see either, because neither knows the other's half;
//   - a member is not deployed at all — the release describes something this
//     server cannot run.
//
// Superseded is *degraded* rather than critical: a newer version running is
// normal, and the finding is that this release is no longer what an element bound
// to it describes. Missing is not-ready: nothing is broken, something is absent.
func (s *Server) releaseFact(rel applicationRelease) panorama.Fact {
	detail := map[string]string{
		"version": strconv.FormatInt(int64(rel.Version), 10),
		"members": strconv.Itoa(len(rel.Members)),
	}
	var superseded, absent int
	for _, m := range rel.Members {
		if m.Kind != "process" {
			// Only processes carry a deployed version to compare against today.
			// Counting a form or a decision as matched would overstate what was
			// checked; leaving it out of both counts is the honest arithmetic.
			continue
		}
		switch d := s.latestDeploymentByProcessID(m.Ref); {
		case d == nil:
			absent++
		case d.Version != m.ArtifactVer:
			superseded++
		}
	}
	detail["superseded"] = strconv.Itoa(superseded)
	detail["absent"] = strconv.Itoa(absent)
	switch {
	case absent > 0:
		return panorama.Fact{
			Source: panorama.SourceReleases, State: panorama.StateNotReady,
			Reason: fmt.Sprintf("%d process(es) this release shipped are not deployed here.", absent),
			Detail: detail,
		}
	case superseded > 0:
		return panorama.Fact{
			Source: panorama.SourceReleases, State: panorama.StateDegraded,
			Reason: fmt.Sprintf("%d process(es) have moved on from the version this release shipped.", superseded),
			Detail: detail,
		}
	default:
		return panorama.Fact{
			Source: panorama.SourceReleases, State: panorama.StateHealthy,
			Reason: "Everything this release shipped is what is deployed here.",
			Detail: detail,
		}
	}
}

// instancesByDefinition counts live process instances per definition. One scan for
// the whole document: the alternative is a scan per bound element, and a model
// binding a hundred processes would then walk the instance table a hundred times
// on the run loop, which is the single writer every other request is waiting on.
//
// Run-loop goroutine only, via its caller.
func (s *Server) instancesByDefinition() (map[uint64]int, error) {
	out := map[uint64]int{}
	err := s.store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
		out[v.ProcessDefKey]++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
