// Package job is Chrampfer's in-process worker harness: it bridges the engine's
// activatable jobs to worker handlers and feeds their results back as commands
// (ADR-0007, streaming pull with completion-as-command).
//
// This is the in-process form. Workers register a Handler per job type; the
// Runner pulls activatable jobs of those types from the state store, runs the
// handler, and submits CompleteJob back through the processor — the processor
// never blocks on the handler. The gRPC streaming transport, job leases with
// timeout/retry, and incident escalation on failure are later milestones; here a
// handler that returns an error simply surfaces it (the job stays pending).
package job

import (
	"fmt"

	"github.com/pblumer/chrampfer/state"
)

// Job is the unit of work handed to a worker.
type Job struct {
	Key                uint64
	Type               int32 // interned job-type index
	ProcessInstanceKey uint64
	ElementInstanceKey uint64
	Retries            int32
}

// Handler does a job's work. Returning nil completes the job; returning an error
// leaves it pending and surfaces the error (retry/incident handling is a later
// milestone).
type Handler func(Job) error

// Engine is the slice of the processor the runner drives: process queued
// commands, and accept job completions.
type Engine interface {
	RunUntilIdle() error
	CompleteJob(jobKey uint64)
}

// Runner dispatches activatable jobs to registered handlers.
type Runner struct {
	store    *state.Store
	engine   Engine
	handlers map[int32]Handler
}

// NewRunner creates a runner over a state store and the engine it feeds.
func NewRunner(store *state.Store, engine Engine) *Runner {
	return &Runner{store: store, engine: engine, handlers: map[int32]Handler{}}
}

// Handle registers a worker for a job type. The type is the interned index the
// compiler assigned (cross-process, globally consistent job-type interning is a
// later concern).
func (r *Runner) Handle(jobType int32, h Handler) { r.handlers[jobType] = h }

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
			if err := h(job); err != nil {
				return dispatched, fmt.Errorf("job %d (type %d): %w", k, jv.JobType, err)
			}
			r.engine.CompleteJob(k)
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
