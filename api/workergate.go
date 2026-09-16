package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
)

// The api half of the dispatch gate (ADR-0340). The `job` package asks a predicate
// whether a candidate may go out and tells it how the ones that did ended; this is what
// stands behind that predicate, and the only place that knows a job belongs to a Worker.
//
// Everything here runs on the run loop, where the store and the deployment registry are
// readable (I3), and writes nothing.

// jobGate satisfies [job.Gate] for the in-process runner, and is the same logic the
// external pull calls directly.
type jobGate struct{ s *Server }

var _ job.Gate = jobGate{}

// logBreakerChange is the minimum an operator is owed while a breaker holds work back.
// ADR-0340 makes visibility part of the decision rather than a follow-up, for a reason
// this line is the smallest form of: work that silently does not happen is the one
// failure mode nobody can diagnose, and an outage that used to announce itself as a
// flood of incidents now announces itself as nothing at all.
//
// It is deliberately the *name* of the job type, not its interned index: the index is
// this engine's bookkeeping and would tell a reader nothing.
func (s *Server) logBreakerChange(k breakerKey, state breakerState, reason string, cooldown time.Duration) {
	name, _ := s.jobTypes.Name(k.jobType)
	target := k.connector
	if target == "" {
		target = name // a job type an external worker serves by name alone is its own target
	}
	if state == breakerClosed {
		logging.Info(logging.WorkerBreakerClosed, "a worker's target is answering again; its jobs are going out",
			slog.String("target", target), slog.String("jobType", name))
		return
	}
	logging.Warn(logging.WorkerBreakerOpen, "holding a worker's jobs back: its target keeps failing",
		slog.String("target", target), slog.String("jobType", name),
		slog.String("reason", reason), slog.String("nextProbeIn", cooldown.String()))
}

// Holding is the cheap question, asked once per job type per round. In the steady state
// it is a lookup in an empty map and no candidate is ever resolved to anything.
func (g jobGate) Holding(jobType int32) bool { return g.s.breakers.holdingFor(jobType) }

// Allow resolves this one candidate to its target and asks that target's breaker. It is
// reached only for a type Holding said yes to, so the two point reads below are paid
// during an outage and not otherwise.
func (g jobGate) Allow(_ int32, jobKey uint64) bool {
	k, _, ok := g.s.breakerTargetOf(jobKey)
	if !ok {
		// Completed or re-leased since the scan saw it. Refusing is the safe answer:
		// there is nothing to hand out, and an open breaker's "yes" is its one probe
		// slot, which must not be spent on a call nobody is going to make.
		return false
	}
	return g.s.breakers.allow(k, jobKey)
}

// Failed reports a job's failure to its target's breaker. Every failure is resolved,
// because a failure can be the first of a target the breaker has never heard of.
func (g jobGate) Failed(_ int32, jobKey uint64, message string) {
	if k, instance, ok := g.s.breakerTargetOf(jobKey); ok {
		g.s.breakers.failed(k, jobKey, instance, message)
	}
}

// Succeeded closes a breaker, or breaks a failure streak. It asks the wider tracking
// question first: a completion on a job type nothing has failed on has nothing to
// report, and that is nearly every completion on nearly every server.
func (g jobGate) Succeeded(jobType int32, jobKey uint64) {
	if !g.s.breakers.tracking(jobType) {
		return
	}
	if k, _, ok := g.s.breakerTargetOf(jobKey); ok {
		g.s.breakers.succeeded(k)
	}
}

// breakerTargetOf resolves a job key to the target a breaker is about, and the process
// instance the job belongs to — which is what the trip condition counts distinct.
//
// This is the attribution ADR-0340 originally wanted stamped into the job record. It is
// two point reads: the job for its type and element instance, the element instance for
// its definition and element index, after which the compiled process names the Worker.
// Nothing here is durable and nothing is recomputed on replay (I6) — the same shape
// `incidentConnectorLookup` uses to attribute an incident listing.
func (s *Server) breakerTargetOf(jobKey uint64) (breakerKey, uint64, bool) {
	jv, ok, err := s.store.GetJob(jobKey)
	if err != nil || !ok {
		return breakerKey{}, 0, false
	}
	return s.breakerTargetOfJob(jv), jv.ProcessInstanceKey, true
}

// breakerTargetOfJob is breakerTargetOf for a caller that already holds the job record —
// both outcome handlers read it before they report — so it costs one read rather than
// two.
//
// A task that names no Worker resolves to its job type alone, with an empty connector.
// That is not a failure to attribute: an external worker serving a plain job type by
// name *is* the target, as far as this engine can see.
func (s *Server) breakerTargetOfJob(jv *model.JobValue) breakerKey {
	k := breakerKey{jobType: jv.JobType}
	ei, ok, err := s.store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		return k
	}
	d, ok := s.deployments[ei.ProcessDefKey]
	if !ok {
		return k
	}
	if ref, ok := d.cp.NodeConnectorRef(ei.ElementId); ok {
		k.connector = ref.Connector
	}
	return k
}

// scanHeldCandidates is the external pull's form of [job.Runner.claimHeld]: the same
// newest-first, budgeted, resuming scan of a job type whose gate is holding something
// back, for the half of dispatch that hands jobs to workers over HTTP.
//
// It is duplicated rather than shared because the two live either side of the `job`
// package's Gate interface, which exists precisely so that package never learns what a
// Worker is. The properties it has to keep are the ones written up there: filter inside
// the scan, newest first, resume where the last round stopped.
func (s *Server) scanHeldCandidates(jobType int32, want int, keys *[]uint64) error {
	var (
		last  uint64
		seen  int
		atEnd = true
	)
	gate := jobGate{s}
	err := unlessTruncated(s.store.ActivatableJobsDesc(jobType, s.gateResume[jobType], func(k uint64) error {
		last, seen = k, seen+1
		if gate.Allow(jobType, k) {
			*keys = append(*keys, k)
		}
		if len(*keys) >= want || seen >= job.GatedScanBudget {
			atEnd = false
			return errListTruncated
		}
		return nil
	}))
	if err != nil {
		return err
	}
	if atEnd {
		delete(s.gateResume, jobType) // back to the newest next round
		return nil
	}
	s.gateResume[jobType] = last
	return nil
}

// closeBreakerReq is the operator's override, and deliberately the only way a person may
// touch a breaker. There is no matching "open this": judging a target down is a
// conclusion the engine draws from what workers report, and letting a person assert it
// by hand would put an opinion where evidence belongs.
type closeBreakerReq struct {
	JobType   string `json:"jobType"`
	Connector string `json:"connector"`
}

// handleCloseBreaker ends a hold early — for the operator who has already fixed the
// endpoint and will not wait out a cooldown (ADR-0340).
//
// Closing is safe in a way opening would not be: the breaker simply forgets the target,
// and if it is still down the next three distinct failures judge it down again. That is
// why a close that found nothing open is a 200 saying so rather than a 404 — an operator
// clicking a row that recovered a second earlier has done nothing wrong, and the reply
// says what happened instead of implying an action that did not.
func (s *Server) handleCloseBreaker(w http.ResponseWriter, r *http.Request) {
	var req closeBreakerReq
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.JobType)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"jobType is required: a close names one target, and a request naming none would mean every breaker on this server")
		return
	}
	var (
		unknown bool
		closed  bool
	)
	s.do(func() {
		idx, ok := s.jobTypes.Index(name)
		if !ok {
			unknown = true
			return
		}
		closed = s.breakers.closeNow(breakerKey{jobType: idx, connector: strings.TrimSpace(req.Connector)})
	})
	if unknown {
		httpapi.Error(w, http.StatusNotFound,
			"no job type "+strconv.Quote(name)+" is known to this engine")
		return
	}
	// Drive off the loop: closing a breaker is what lets a held backlog go, and the
	// handlers that drain it make the outbound calls a worker exists for.
	if err := s.drive(); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "close breaker: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"jobType": name, "connector": req.Connector, "closed": closed,
	})
}
