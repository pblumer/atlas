package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
)

// What a worker actually did, one job at a time.
//
// The Workers view answers "is anyone doing this work" with counters. That is the
// right first question and the wrong last one: when a mail task fails, an operator
// does not want to know that the mail worker has failed eleven jobs, they want to
// know *which* eleven, what was handed to them, what came back, and what the error
// said. Until now that answer meant an incident per job, or the server log, or
// nothing.
//
// **This is a debugging window, not a record.** It is a bounded ring in this
// process's memory, exactly like the mail outbox (ADR-0150) and the worker counters
// beside it: never an event, never applied to state, never rebuilt on recovery
// (I4/I6), and emptied by a restart. The durable account of what a process did is
// the event log and the instance timeline, and this does not compete with it — it
// holds the last few jobs per worker for as long as someone is looking.
//
// That bound is also what makes it safe to hold variables at all. A job's variables
// are the process's own data — recipients, addresses, whatever a script was given —
// so they are shown to admins only (see [Server.handleWorkerJobs]) and they are
// clipped, and they age out of the ring within minutes on a busy worker rather than
// accumulating into a shadow copy of the instance history nobody administers.
//
// Like the rest of the registry it does no locking of its own: every mutation and
// every read happens on the run-loop goroutine, the single writer (I3).

// Bounds on what the ring keeps. Small on purpose: this is the tail an operator
// reads while diagnosing, and the variables in it are process data.
const (
	// jobRunsPerWorker is how many of a worker's most recent jobs are kept.
	jobRunsPerWorker = 50
	// maxJobRunVars bounds one job's rendered variables. A CSV import handed a large
	// input would otherwise pin megabytes per row of the ring.
	maxJobRunVars = 16 << 10
)

// Outcomes a job run can be in. "in flight" is a lease this server has not seen
// settled — it may still be working, or its worker may be gone and its lease about
// to elapse, which the row's age is what tells apart.
const (
	jobRunInFlight  = "in flight"
	jobRunCompleted = "completed"
	jobRunFailed    = "failed"
)

// jobRun is one job a worker leased, as the console shows it.
type jobRun struct {
	JobKey             uint64 `json:"jobKey"`
	Type               string `json:"type"`
	ProcessInstanceKey uint64 `json:"processInstanceKey,omitempty"`
	ProcessDefKey      uint64 `json:"processDefKey,omitempty"`
	// ElementID is the task's own id in the diagram, which is what lets an operator
	// go from "this failed" to the shape on the canvas.
	ElementID string `json:"elementId,omitempty"`
	LeasedAt  int64  `json:"leasedAt"`
	SettledAt int64  `json:"settledAt,omitempty"`
	Outcome   string `json:"outcome"`
	// Error is what the worker reported on a failure — the message that would
	// otherwise only appear on the incident, if the retries ran out enough to raise
	// one.
	Error string `json:"error,omitempty"`
	// Retries is what the worker left the job with, so a row shows whether this
	// failure still has attempts behind it or has parked. Deliberately not omitempty:
	// zero is the most important value it takes — "no attempts left, this one has
	// parked" — and omitting it would hide exactly the row an operator is looking for.
	// It is meaningless until the job settles, which is what Outcome says.
	Retries int32 `json:"retries"`
	// In and Out are the variables handed to the worker and the ones it returned,
	// rendered as JSON text so the ring holds a bounded string rather than an
	// arbitrarily deep structure it would have to keep alive.
	In  string `json:"in,omitempty"`
	Out string `json:"out,omitempty"`
}

// jobRunRing is one worker's recent jobs, newest last, with an index into the ones
// still held so an outcome can find the row its lease created.
type jobRunRing struct {
	runs  []*jobRun
	byKey map[uint64]*jobRun
}

func newJobRunRing() *jobRunRing { return &jobRunRing{byKey: map[uint64]*jobRun{}} }

// add records a newly leased job, evicting the oldest once the ring is full.
func (r *jobRunRing) add(run *jobRun) {
	r.runs = append(r.runs, run)
	r.byKey[run.JobKey] = run
	if len(r.runs) > jobRunsPerWorker {
		drop := len(r.runs) - jobRunsPerWorker
		for _, old := range r.runs[:drop] {
			// Only if it is still the indexed one: a job key re-leased after its lease
			// elapsed has a newer row, and dropping the index would orphan that one.
			if r.byKey[old.JobKey] == old {
				delete(r.byKey, old.JobKey)
			}
		}
		// Copy down rather than reslice, so the evicted rows and their variables are
		// not kept alive behind the array.
		r.runs = append(r.runs[:0], r.runs[drop:]...)
	}
}

// list returns the ring newest first, which is the order an operator reads it in.
func (r *jobRunRing) list() []jobRun {
	out := make([]jobRun, 0, len(r.runs))
	for i := len(r.runs) - 1; i >= 0; i-- {
		out = append(out, *r.runs[i])
	}
	return out
}

// recordLease opens a row for each job a worker just leased. The variables are the
// ones the engine resolved for it, which is the half an operator cannot reconstruct
// afterwards: they are the scope as it stood at lease time, and the instance may
// have moved on since.
func (r *workerRegistry) recordLease(worker string, jobs []pulledJob) {
	for _, j := range jobs {
		ring, ok := r.jobs[worker]
		if !ok {
			ring = newJobRunRing()
			r.jobs[worker] = ring
		}
		ring.add(&jobRun{
			JobKey:             j.JobKey,
			Type:               j.Type,
			ProcessInstanceKey: j.ProcessInstanceKey,
			ProcessDefKey:      j.ProcessDefKey,
			ElementID:          j.ElementID,
			LeasedAt:           r.now(),
			Outcome:            jobRunInFlight,
			In:                 renderJobVars(j.Variables),
		})
	}
}

// recordOutcome closes the row a lease opened. A settled job whose row has already
// aged out of the ring is dropped rather than re-added: the ring is the recent tail,
// and an outcome for something no longer in it has nothing to attach to.
func (r *workerRegistry) recordOutcome(worker string, jobKey uint64, outcome, message string, retries int32, out []model.VariableValue) {
	ring, ok := r.jobs[worker]
	if !ok {
		return
	}
	run, ok := ring.byKey[jobKey]
	if !ok {
		return
	}
	run.Outcome = outcome
	run.SettledAt = r.now()
	run.Error = clipJobRunField(message)
	run.Retries = retries
	if len(out) > 0 {
		run.Out = renderReturnedVars(out)
	}
}

// jobsOf returns one worker's recent jobs, newest first.
func (r *workerRegistry) jobsOf(worker string) []jobRun {
	ring, ok := r.jobs[worker]
	if !ok {
		return []jobRun{}
	}
	return ring.list()
}

// renderJobVars turns a job's variables into the bounded JSON text the ring holds.
// Marshalling here rather than at read time is deliberate: it detaches the row from
// whatever the caller still holds a reference to, so the ring cannot keep an
// instance's data alive or show a value that changed after the fact.
func renderJobVars(vars map[string]any) string {
	if len(vars) == 0 {
		return ""
	}
	b, err := json.Marshal(vars)
	if err != nil {
		// A variable that will not marshal is worth saying so about rather than
		// dropping the row: "there was something here" is the useful half.
		return "{\"…\":\"these variables could not be rendered: " + err.Error() + "\"}"
	}
	return clipJobRunField(string(b))
}

// renderReturnedVars turns the variables a worker completed with into the same
// bounded JSON text. They arrive as the engine's own value type rather than as a
// plain map, so this is the one place the two shapes meet.
func renderReturnedVars(vars []model.VariableValue) string {
	m := make(map[string]any, len(vars))
	for i := range vars {
		m[vars[i].Name] = nativeVar(&vars[i])
	}
	return renderJobVars(m)
}

// clipJobRunField bounds one stored field, marking the truncation in the text so a
// reader is never shown a shortened value as if it were whole.
func clipJobRunField(s string) string {
	if len(s) <= maxJobRunVars {
		return s
	}
	return s[:maxJobRunVars] + "… [truncated by the worker job ring]"
}

// handleWorkerJobs lists one worker's recent jobs: what it leased, what it was
// handed, what it returned, and what failed.
//
// **Admin only.** Everything else in the Workers view is operational metering —
// counts, states, names — and is as safe to show as a dashboard. This is the
// process's own data: recipients, addresses, whatever a script was given. Someone
// who may read it here may already read it on the instance, so the gate is not a new
// restriction; putting it on is what keeps the Workers view itself open to everyone
// who needs to see whether work is moving.
//
// Like the outbox read it does not go through s.do for the response: the registry is
// run-loop state, so the read itself does, and nothing else here touches the engine.
func (s *Server) handleWorkerJobs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return // requireAdmin wrote 403
	}
	// An empty id is a real worker: one that leased without giving a name. The view
	// labels it, and it must be reachable here for the same reason it is listed there.
	worker := strings.TrimSpace(r.PathValue("id"))
	var runs []jobRun
	s.do(func() { runs = s.workers.jobsOf(worker) })
	httpapi.JSON(w, http.StatusOK, map[string]any{"worker": worker, "jobs": runs})
}
