package api

import (
	"log/slog"
	"time"

	"github.com/pblumer/atlas/job"
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
		slog.Info("a worker's target is answering again; its jobs are going out",
			"event", "worker.breaker_closed", "target", target, "jobType", name)
		return
	}
	slog.Warn("holding a worker's jobs back: its target keeps failing",
		"event", "worker.breaker_open", "target", target, "jobType", name,
		"reason", reason, "nextProbeIn", cooldown.String())
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
