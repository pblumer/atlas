package api

import (
	"fmt"
	"sort"

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
func (s *Server) incidentsByDefinition() (map[uint64]incidentTally, bool, error) {
	out := map[uint64]incidentTally{}
	// sites accumulates by definition and then by element, so several incidents on
	// one task are one entry with a count rather than one entry each.
	sites := map[uint64]map[string]*panorama.IncidentSite{}
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
		tally := out[pi.ProcessDefKey]
		tally.Count++
		out[pi.ProcessDefKey] = tally
		s.recordIncidentSite(sites, pi.ProcessDefKey, v)
		return nil
	}))
	if err != nil {
		return nil, false, err
	}
	for key, byElement := range sites {
		tally := out[key]
		tally.Sites = rankIncidentSites(byElement)
		out[key] = tally
	}
	return out, partial, nil
}

// incidentTally is what one definition's unresolved incidents amount to: how many
// there are, and where in its diagram they are parked.
type incidentTally struct {
	Count int
	Sites []panorama.IncidentSite
}

// maxIncidentSites bounds how many elements one process reports. A process with
// eleven broken tasks is not eleven findings somebody triages from a landscape
// view; it is one finding — "this process is in trouble" — and Operations is where
// the whole list belongs. The bound is on the answer, not on the scan, so the
// counts above it stay right.
const maxIncidentSites = 5

// maxIncidentMessage bounds one message. A worker can return a page of HTML as
// its error, and a panel is not where somebody reads that.
const maxIncidentMessage = 240

// recordIncidentSite attributes one incident to the element it is parked on.
//
// The element index in an incident is compiled-graph local, so it means nothing
// without the definition that produced it — which is why this resolves through the
// deployment rather than carrying the number outward. A definition this server no
// longer holds contributes to the count and to no site: the incident is real, and
// where it sits is something we can no longer name.
func (s *Server) recordIncidentSite(sites map[uint64]map[string]*panorama.IncidentSite,
	key uint64, v *model.IncidentValue) {
	d, ok := s.deployments[key]
	if !ok || d.cp == nil {
		return
	}
	id := d.cp.ElementBpmnId(v.ElementId)
	if id == "" {
		return
	}
	byElement := sites[key]
	if byElement == nil {
		byElement = map[string]*panorama.IncidentSite{}
		sites[key] = byElement
	}
	site := byElement[id]
	if site == nil {
		site = &panorama.IncidentSite{
			ElementID:   id,
			ElementType: d.cp.Node(v.ElementId).Type.String(),
			Message:     truncateMessage(v.Message),
		}
		byElement[id] = site
	}
	site.Count++
}

// rankIncidentSites orders the worst first and cuts to the bound.
//
// Deterministic past the count, by element id, because two reads of an unchanged
// server must produce the same document — a list that reshuffled itself would make
// the drift journal record changes that never happened.
func rankIncidentSites(byElement map[string]*panorama.IncidentSite) []panorama.IncidentSite {
	ranked := make([]panorama.IncidentSite, 0, len(byElement))
	for _, site := range byElement {
		ranked = append(ranked, *site)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count != ranked[j].Count {
			return ranked[i].Count > ranked[j].Count
		}
		return ranked[i].ElementID < ranked[j].ElementID
	})
	if len(ranked) > maxIncidentSites {
		ranked = ranked[:maxIncidentSites]
	}
	return ranked
}

// truncateMessage cuts a message to the bound, marking that it was cut. A silently
// shortened error reads as a complete one, and somebody would search for a string
// that does not exist.
func truncateMessage(message string) string {
	if len(message) <= maxIncidentMessage {
		return message
	}
	return message[:maxIncidentMessage] + "…"
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
//     knows. connectorProblem is the same derivation the worker list shows
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

// jobTypeStatus says what this server can honestly observe about one kind of work.
//
// This is the mapping ADR-0189 §6 needs and nobody had chosen, and the reason it
// stayed unchosen is real: a job type is not a thing that can be well or unwell. It
// is a *name for work*, and the states in §6 describe resources. So the question had
// to be turned into one the engine can answer — "is this kind of work getting done
// here" — and answered only where the evidence exists.
//
// Four answers, in the order they are checked, because the first that applies is the
// most specific thing known:
//
//   - Work of this kind is parked behind incidents: **degraded**. Jobs exist, they
//     were attempted, and they failed. That is the same reading a process gets, from
//     the same evidence.
//   - The engine runs this kind itself: **healthy**. It built the handler, so it
//     knows; there is no worker to wait for and nothing outside this process to ask.
//   - A worker has taken jobs of this kind since the server started: **healthy**.
//     Not "a worker exists" — *this kind of work has demonstrably been done here*,
//     which is a fact rather than an inference from a registration.
//   - Otherwise: **unbound**, which is not a finding. The worker registry is runtime
//     state that a restart empties, so *no worker has polled* is not evidence that
//     none exists — and a fresh server would otherwise mark every worker-served kind
//     as broken, which is precisely the mistake §4's severity rules exist to prevent.
//     The queue depth rides along in the detail, so a reader can see there is work
//     waiting without the view claiming to know why.
//
// The deliberate omission is a *not-ready*: "queued and nobody is serving it" is the
// state an operator most wants, and this server cannot tell it apart from "queued
// and the worker polls every five minutes". Saying so is the whole of the answer.
func jobTypeStatus(taken, incidents int64, inProcess bool) (state, reason string) {
	switch {
	case incidents > 0:
		return panorama.StateDegraded, fmt.Sprintf(
			"%d job(s) of this type are parked behind an unresolved incident.", incidents)
	case inProcess:
		return panorama.StateHealthy, "The engine runs this job type itself."
	case taken > 0:
		return panorama.StateHealthy, fmt.Sprintf(
			"A worker has taken %d job(s) of this type since this server started.", taken)
	default:
		return panorama.StateUnbound, "No worker has taken work of this type since this " +
			"server started, which is not the same as none serving it: the worker registry " +
			"is emptied by a restart."
	}
}

// jobTypeTaken counts, per job type, how many jobs the workers seen this run have
// pulled. Cumulative rather than in-flight on purpose: a type whose queue drained an
// hour ago is being served, and an in-flight gauge of zero would report it as
// unobserved every time it happened to be idle.
func (s *Server) jobTypeTaken() map[string]int64 {
	out := map[string]int64{}
	for _, st := range s.workers.byName {
		for jobType, n := range st.Types {
			out[jobType] += n
		}
	}
	return out
}
