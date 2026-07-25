// Package job is Atlas's in-process worker harness: it bridges the engine's
// activatable jobs to worker handlers and feeds their results back as commands
// (ADR-0007, streaming pull with completion-as-command).
//
// This is the in-process form. Workers register a Handler per job type; the
// Runner pulls activatable jobs of those types from the state store, runs the
// handler, and submits CompleteJob back through the processor — the processor
// never blocks on the handler. A handler that returns an error is routed into the
// incident model (ADR-0061): the job is failed (retried while retries remain, then
// an incident parks its token) rather than aborting the whole Drive, so one
// failing job cannot poison the run loop. The gRPC streaming transport and job
// leases with timeout/backoff are later milestones.
package job

import (
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
// evaluation to retain for debugging (ADR-0064). Decision is nil for workers that
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
	FailJob(jobKey uint64, retries int32, message string)
}

// Runner dispatches activatable jobs to registered handlers.
type Runner struct {
	store    *state.Store
	engine   Engine
	handlers map[int32]CompletingHandler
}

// NewRunner creates a runner over a state store and the engine it feeds.
func NewRunner(store *state.Store, engine Engine) *Runner {
	return &Runner{store: store, engine: engine, handlers: map[int32]CompletingHandler{}}
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
// variables and a decision evaluation to retain (the DMN worker, ADR-0064). Same
// dispatch as the others; its Completion rides along on the CompleteJob command.
func (r *Runner) HandleCompleting(jobType int32, h CompletingHandler) { r.handlers[jobType] = h }

// PollOnce pulls every activatable job of a registered type, runs its handler,
// and submits a completion command for each that succeeds. It returns how many
// jobs it dispatched. The submitted completions are processed on the next
// RunUntilIdle.
func (r *Runner) PollOnce() (int, error) {
	dispatched := 0
	for jobType, h := range r.handlers {
		var keys []uint64
		if err := r.store.ActivatableJobs(jobType, func(k uint64) error {
			keys = append(keys, k)
			return nil
		}); err != nil {
			return dispatched, err
		}
		for _, k := range keys {
			jv, ok, err := r.store.GetJob(k)
			if err != nil {
				return dispatched, err
			}
			if !ok {
				continue // completed since the scan; skip
			}
			job := Job{
				Key:                k,
				Type:               jv.JobType,
				ProcessInstanceKey: jv.ProcessInstanceKey,
				ElementInstanceKey: jv.ElementInstanceKey,
				Retries:            jv.Retries,
			}
			completion, err := h(job)
			if err != nil {
				// A worker that can't complete its job must not abort the whole
				// Drive — otherwise one failing job (e.g. a business rule task whose
				// decision model isn't deployed) poisons the run loop and fails every
				// future deploy or completion that drives jobs. Route the failure into
				// the incident model instead (ADR-0061): FailJob retries the job while
				// retries remain, then raises an incident that parks the token. Count
				// it as progress so Drive loops to apply the FailJob command; when the
				// job parks (or completes on a retry) it drops off the activatable
				// index and Drive terminates.
				r.engine.FailJob(job.Key, job.Retries-1, err.Error())
				dispatched++
				continue
			}
			r.engine.CompleteJobWithDecision(k, completion.Decision, completion.Outputs...)
			dispatched++
		}
	}
	return dispatched, nil
}

// Drive runs the engine and dispatches jobs alternately until the system is
// idle: no pending commands and no activatable jobs for registered types. It is
// the in-process equivalent of workers streaming alongside a running processor.
func (r *Runner) Drive() error {
	for {
		if err := r.engine.RunUntilIdle(); err != nil {
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
