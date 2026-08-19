// Package job is Atlas's in-process worker harness: it bridges the engine's
// activatable jobs to worker handlers and feeds their results back as commands
// (ADR-0007, streaming pull with completion-as-command).
//
// This is the in-process form. Workers register a Handler per job type; the
// Runner pulls activatable jobs of those types from the state store, runs the
// handler, and submits CompleteJob back through the processor. A handler that
// returns an error is routed into the incident model (ADR-0061): the job is
// failed (retried while retries remain, then an incident parks its token) rather
// than aborting the whole Drive, so one failing job cannot poison the run loop.
// The gRPC streaming transport and job leases with timeout/backoff are later
// milestones.
//
// # Where each step runs (ADR-0150)
//
// A handler does the one thing the engine cannot bound: it talks to the outside
// world — an interpreter, an HTTP endpoint, an SMTP server. So the cycle is split
// across the single-writer boundary ([Loop]):
//
//   - claim (scan the activatable index, read the job record) — on the loop,
//   - the handler — OFF the loop, on the goroutine that called Drive,
//   - apply (CompleteJob / FailJob, and the RunUntilIdle around it) — on the loop.
//
// That is what makes "the processor never blocks on the handler" true of the
// in-process runner too. Before it, a Runner driven from inside a run-loop turn
// executed handlers there, so one unresponsive third-party host held the single
// writer — and therefore every request the server had — until it answered.
//
// A Runner built without a Loop runs every step on the calling goroutine, which
// is exactly the old behavior and what a library user or a test gets.
package job

import (
	"fmt"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Job is the unit of work handed to a worker.
type Job struct {
	Key                uint64
	Type               int32 // interned job-type index
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	Retries            int32
}

// Handler does a job's work with no output. Returning nil completes the job;
// returning an error fails it (retry while retries remain, then an incident,
// ADR-0061).
type Handler func(Job) error

// OutputHandler does a job's work and returns the variables to write back into
// the job's process instance on completion (nil for none) — e.g. a business rule
// task's decision result. As for Handler, returning an error fails the job
// (retry, then an incident).
type OutputHandler func(Job) ([]model.VariableValue, error)

// Completion is everything a worker hands back when a job succeeds: the output
// variables to write into the instance and, for a business rule task, the decision
// evaluation to retain for debugging (ADR-0066). Decision is nil for workers that
// do not produce one.
type Completion struct {
	Outputs  []model.VariableValue
	Decision *model.DecisionEvaluationValue
}

// CompletingHandler does a job's work and returns its full Completion — outputs
// plus any decision evaluation. It is the widest handler shape; Handler and
// OutputHandler are the output-less and decision-less special cases. Returning an
// error fails the job (retry, then an incident) exactly as for the others.
type CompletingHandler func(Job) (Completion, error)

// Engine is the slice of the processor the runner drives: process queued
// commands, accept job completions with their output variables and (for a decision)
// the evaluation to retain, and accept job failures (which retry or raise an
// incident, ADR-0061).
type Engine interface {
	RunUntilIdle() error
	CompleteJob(jobKey uint64, outputs ...model.VariableValue)
	CompleteJobWithDecision(jobKey uint64, decision *model.DecisionEvaluationValue, outputs ...model.VariableValue)
	FailJob(jobKey uint64, retries int32, message string, backoff int64)
}

// Loop is the single-writer boundary (invariant I3): Do runs fn on the goroutine
// that owns the partition's state and returns when it has run. api/runloop.Loop is
// the implementation the server passes; a Runner given none runs everything on the
// calling goroutine.
type Loop interface {
	Do(fn func())
}

// Option configures a Runner.
type Option func(*Runner)

// OnLoop hands the runner the single writer to dispatch its state work through, so
// the parts that touch the store and the processor run there while handlers do not
// (ADR-0150). Pass it whenever the runner shares a partition with anything else —
// which, in the server, is always.
func OnLoop(l Loop) Option { return func(r *Runner) { r.loop = l } }

// Runner dispatches activatable jobs to registered handlers.
type Runner struct {
	store    *state.Store
	engine   Engine
	handlers map[int32]CompletingHandler
	// loop is the single writer this runner dispatches state work through; nil
	// means "run it here", the standalone behavior.
	loop Loop
	// inflight holds the jobs this runner has claimed and not yet applied, so a
	// second driver running concurrently cannot hand the same job to a handler
	// twice. It is written only from claim and apply, both of which run on the
	// loop, so it needs no lock of its own (I3).
	inflight map[uint64]struct{}
}

// NewRunner creates a runner over a state store and the engine it feeds.
func NewRunner(store *state.Store, engine Engine, opts ...Option) *Runner {
	r := &Runner{
		store:    store,
		engine:   engine,
		handlers: map[int32]CompletingHandler{},
		inflight: map[uint64]struct{}{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// onLoop runs fn on the single writer, or here when the runner has none.
func (r *Runner) onLoop(fn func()) {
	if r.loop == nil {
		fn()
		return
	}
	r.loop.Do(fn)
}

// Handle registers an output-less worker for a job type. The type is the interned
// index the compiler assigned (cross-process, globally consistent job-type
// interning is a later concern).
func (r *Runner) Handle(jobType int32, h Handler) {
	r.handlers[jobType] = func(j Job) (Completion, error) { return Completion{}, h(j) }
}

// HandleWithOutput registers a worker whose completion writes output variables
// back into the instance (e.g. a service-task worker that returns variables). Same
// dispatch as Handle; the only difference is that its returned variables ride along
// on the CompleteJob command.
func (r *Runner) HandleWithOutput(jobType int32, h OutputHandler) {
	r.handlers[jobType] = func(j Job) (Completion, error) {
		outputs, err := h(j)
		return Completion{Outputs: outputs}, err
	}
}

// HandleCompleting registers a worker whose completion carries both output
// variables and a decision evaluation to retain (the DMN worker, ADR-0066). Same
// dispatch as the others; its Completion rides along on the CompleteJob command.
func (r *Runner) HandleCompleting(jobType int32, h CompletingHandler) { r.handlers[jobType] = h }

// PollOnce pulls every activatable job of a registered type, runs its handler,
// and submits a completion command for each that succeeds. It returns how many
// jobs it dispatched. The submitted completions are processed on the next
// RunUntilIdle.
//
// One job at a time, in three steps: claim on the loop, run the handler off it,
// apply on it again (ADR-0150). Claiming per job rather than per batch is what
// keeps a job that a previous handler finished off from being dispatched a second
// time — its record is re-read after that handler has been applied, not before.
//
// It must not be called from the loop goroutine: it dispatches onto the loop, and
// the loop cannot serve itself. In the server that is enforced by a drift test
// over package api.
func (r *Runner) PollOnce() (int, error) {
	dispatched := 0
	for jobType, h := range r.handlers {
		keys, err := r.activatable(jobType)
		if err != nil {
			return dispatched, err
		}
		for _, k := range keys {
			job, ok, err := r.claim(k)
			if err != nil {
				return dispatched, err
			}
			if !ok {
				continue // completed since the scan, or another driver has it; skip
			}
			// The handler is the whole reason for this split: it runs arbitrary
			// third-party-facing code — an interpreter, an HTTP call — and it runs
			// here, on the caller's goroutine, never on the single writer.
			completion, herr := run(h, job)
			r.onLoop(func() { r.apply(job, completion, herr) })
			dispatched++
		}
	}
	return dispatched, nil
}

// activatable collects the keys of every activatable job of one type. It reads the
// store, so it runs on the loop.
func (r *Runner) activatable(jobType int32) ([]uint64, error) {
	var (
		keys []uint64
		err  error
	)
	r.onLoop(func() {
		err = r.store.ActivatableJobs(jobType, func(k uint64) error {
			keys = append(keys, k)
			return nil
		})
	})
	return keys, err
}

// claim reads a scanned job's record and marks it in flight, reporting ok=false
// when it is gone (completed since the scan) or already claimed by a concurrent
// driver. It reads the store and writes the in-flight set, so it runs on the loop.
func (r *Runner) claim(k uint64) (Job, bool, error) {
	var (
		job Job
		ok  bool
		err error
	)
	r.onLoop(func() {
		if _, taken := r.inflight[k]; taken {
			return
		}
		jv, found, e := r.store.GetJob(k)
		if e != nil {
			err = e
			return
		}
		if !found {
			return
		}
		r.inflight[k] = struct{}{}
		job = Job{
			Key:                k,
			Type:               jv.JobType,
			ProcessInstanceKey: jv.ProcessInstanceKey,
			ElementInstanceKey: jv.ElementInstanceKey,
			Retries:            jv.Retries,
		}
		ok = true
	})
	return job, ok, err
}

// apply releases a job's claim and submits what its handler produced. It touches
// the processor and the in-flight set, so it runs on the loop.
func (r *Runner) apply(job Job, completion Completion, err error) {
	delete(r.inflight, job.Key)
	if err != nil {
		// A worker that can't complete its job must not abort the whole
		// Drive — otherwise one failing job (e.g. a business rule task whose
		// decision model isn't deployed) poisons the run loop and fails every
		// future deploy or completion that drives jobs. Route the failure into
		// the incident model instead (ADR-0061): FailJob retries the job while
		// retries remain, then raises an incident that parks the token. The
		// dispatch is counted as progress so Drive loops to apply the FailJob
		// command; when the job parks (or completes on a retry) it drops off the
		// activatable index and Drive terminates.
		// The in-process runner retries immediately (backoff 0); a worker-supplied
		// backoff arrives through the HTTP fail endpoint instead (ADR-0111).
		r.engine.FailJob(job.Key, job.Retries-1, err.Error(), 0)
		return
	}
	r.engine.CompleteJobWithDecision(job.Key, completion.Decision, completion.Outputs...)
}

// run calls a handler, turning a panic into the error the incident model already
// knows how to carry. Off the loop the panic would otherwise unwind into whatever
// goroutine is driving — an HTTP handler, where net/http recovers it and the claim
// would never be released, stranding the job until a restart.
func run(h CompletingHandler, job Job) (completion Completion, err error) {
	defer func() {
		if p := recover(); p != nil {
			completion, err = Completion{}, fmt.Errorf("job: handler for type %d panicked: %v", job.Type, p)
		}
	}()
	return h(job)
}

// Drive runs the engine and dispatches jobs alternately until the system is
// idle: no pending commands and no activatable jobs for registered types. It is
// the in-process equivalent of workers streaming alongside a running processor.
//
// Like PollOnce, it must be called off the loop goroutine (ADR-0150).
func (r *Runner) Drive() error {
	for {
		var err error
		r.onLoop(func() { err = r.engine.RunUntilIdle() })
		if err != nil {
			return err
		}
		n, err := r.PollOnce()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}
