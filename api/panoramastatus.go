package api

import (
	"fmt"

	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/model"
)

// What the landscape mesh can honestly say about the health of what it draws
// (ADR-0211 §4, P2.5c).
//
// ADR-0211 places severity after ADR-0189's P4 observation projection, because that
// is where an external source with a freshness contract comes from. P4 does not
// exist yet — but two of the states it will carry are things this engine already
// knows about itself without asking anyone: which processes have work parked, and
// which configured workers cannot do work. Those are reported now, and the two
// states that genuinely need P4 are declared unavailable in the payload rather than
// quietly left out (see panorama.unobservable). Reporting nothing until every state
// is reachable would withhold the answer an operator opens this view for; reporting
// a green mesh without saying what is unwatched would be worse than either.

// maxStatusIncidentScan bounds the incident scan one mesh request pays for. The
// mesh derives a whole instance on the run loop, so it must not be the request that
// walks an unbounded incident table because one process has thousands of tokens
// parked behind a broken worker. Hitting the bound is reported, not swallowed: it
// makes every process the scan did not reach a floor rather than a verdict, which
// is what panorama.Status.Partial says.
//
// It is deliberately the same order as the live overlay's bound
// (maxRuntimeIncidentScan): the two answer the same question at different
// altitudes, and a mesh that scanned further than the view an operator drills into
// would report a problem the drilldown then could not find.
const maxStatusIncidentScan = 2000

// incidentsByDefinition counts unresolved incidents per process definition, and
// reports whether it stopped early.
//
// An incident carries its process instance rather than its definition
// (model.IncidentValue), so each one costs a point lookup to attribute — and
// attribute it must: counting an incident against the wrong definition would put a
// finding on a process that is fine, which is the one mistake that makes an
// operator stop believing the color.
//
// Runs on the run-loop goroutine, via its caller.
func (s *Server) incidentsByDefinition() (map[uint64]int, bool, error) {
	out := map[uint64]int{}
	scanned, partial := 0, false
	err := unlessTruncated(s.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		if scanned++; scanned > maxStatusIncidentScan {
			partial = true
			return errListTruncated
		}
		pi, ok, err := s.store.ProcessInstance(v.ProcessInstanceKey)
		if err != nil {
			return err
		}
		if !ok {
			// The instance is gone while its incident is still indexed. Nothing to
			// attribute it to, and guessing would be worse than dropping it.
			return nil
		}
		out[pi.ProcessDefKey]++
		return nil
	}))
	if err != nil {
		return nil, false, err
	}
	return out, partial, nil
}

// processStatus turns a definition's parked-work count into an observation.
//
// Zero parked is reported as healthy rather than as unknown, and that is a real
// claim: the engine holds every incident it ever raised, so it can answer this one
// about itself without asking anything outside the process. A definition nothing has
// ever run is healthy for the same reason — nothing about it is failing.
func processStatus(parked int) (state, reason string) {
	if parked == 0 {
		return panorama.StateHealthy, "No work is parked in this process."
	}
	return panorama.StateDegraded, fmt.Sprintf(
		"%d token(s) are parked behind an unresolved incident.", parked)
}

// workerStatus says whether one configured worker can do work, from what this
// server can see of it.
//
// The three answers come from three different kinds of evidence, and conflating
// them is exactly what would make this dishonest:
//
//   - The engine serves this kind itself, so it built the client (or failed to) and
//     knows. connectorProblem is the same derivation the connector list shows
//     (ADR-0158), so the mesh and that page cannot disagree.
//   - The kind is worker-only (ADR-0172): the engine holds no credential and builds
//     no client, so the absence of a problem is not evidence of health. The only
//     thing this server knows is what workers reported holding on their polls.
//   - No worker has polled this server run at all, in which case nothing is known
//     about a worker-only kind and saying so is the whole of the answer. It resolves
//     itself on the first poll, and calling it broken in the meantime would put a
//     red node on every fresh server.
func (s *Server) workerStatus(kind, name string, polled bool, held map[string]bool) (state, reason string) {
	if why := s.connectorProblem(kind, name); why != "" {
		return panorama.StateNotReady, "This worker cannot serve work: " + why + "."
	}
	if !connectorKindIsWorkerOnly(kind) {
		return panorama.StateHealthy, "The engine holds a usable client for this worker."
	}
	if held[name] {
		return panorama.StateHealthy, "A worker polling this server reports holding it."
	}
	if !polled {
		return panorama.StateUnbound, "No worker has polled this server yet, so nothing " +
			"here can tell whether this worker can serve work."
	}
	return panorama.StateNotReady, "No worker that has polled this server reports holding " +
		"it, so its tasks would park."
}

// connectorKindIsWorkerOnly reports whether a kind is served only by an out-of-process
// worker (ADR-0172), which is what decides whether this engine's own registry is
// evidence about it at all. An unrecognized kind is not worker-only: it has no entry
// to be served from either way, and connectorProblem has already said nothing about
// it, so the caller's healthy answer stays as weak as its evidence.
func connectorKindIsWorkerOnly(kind string) bool {
	for _, k := range managedConnectorKinds {
		if k.name == kind {
			return k.workerOnly
		}
	}
	return false
}

// workerHoldings is what the polling workers currently report holding, and whether
// any of them has polled this server run at all. The registry is runtime state that
// a restart clears (see workers.go), which is precisely why the second return value
// exists: an empty set after a restart means "nobody has said anything yet", not
// "nothing is served".
//
// Runs on the run-loop goroutine, via its caller.
func (s *Server) workerHoldings() (held map[string]bool, polled bool) {
	held = map[string]bool{}
	for _, st := range s.workers.byName {
		polled = true
		for _, name := range st.Connectors {
			held[name] = true
		}
	}
	return held, polled
}
