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
	"errors"
	"sync"
	"time"

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
	// LeaseEpoch is the round's claim on this job — the fencing token the engine
	// minted when the runner leased it (ADR-0007). Submit presents it back, so an
	// outcome from a round whose lease has since elapsed and been handed on is
	// dropped instead of applied (ADR-draft-in-process-job-leases).
	LeaseEpoch uint64
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
	// ToolCalls are the tools an agent chose for the next round of an agent-driven
	// ad-hoc subprocess (ADR-0253). Empty for every other worker — and for an agent
	// worker it is how "the run is finished" is said: a completion naming no tool ends
	// the loop and completes the container, which is why the zero value is an ending
	// rather than an error.
	ToolCalls []model.ToolCall
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
	ActivateJob(jobKey uint64, worker string, leaseFor int64)
	CompleteJob(jobKey uint64, outputs ...model.VariableValue)
	CompleteJobWithDecision(jobKey uint64, decision *model.DecisionEvaluationValue, outputs ...model.VariableValue)
	CompleteJobWithToolCalls(jobKey uint64, toolCalls []model.ToolCall, outputs ...model.VariableValue)
	FailJob(jobKey uint64, retries int32, message string, backoff int64)
}

// Outcome is what one job's handler produced: the job, what it completed with,
// and the error that failed it. Exactly one of Completion and Err is meaningful.
type Outcome struct {
	Job        Job
	Completion Completion
	Err        error
}

// Runner dispatches activatable jobs to registered handlers.
//
// A handler is registered as a *factory* rather than a handler, because handlers
// read state and no longer run on the goroutine that owns it: each round binds its
// handlers to that round's [state.Reader], a consistent read view taken while the
// run loop was held (ADR-0157 step 6). The connector/* packages are unchanged by
// this — their Handler constructors already take a reader, so the factory is one
// closure at the registration site.
type Runner struct {
	store     *state.Store
	engine    Engine
	factories map[int32]func(state.Reader) CompletingHandler
	// Concurrency bounds how many handlers a round runs at once.
	//
	// Serial dispatch had one virtue: a backlog could not become a thundering herd.
	// Running handlers concurrently (ADR-0157 step 6) is what ended the amplification
	// of a burst, but unbounded it would trade one failure for another — a thousand
	// parked jobs becoming a thousand simultaneous outbound calls, exhausting file
	// descriptors here and hammering whatever is on the other end. The cap keeps the
	// throughput and drops the herd.
	Concurrency int
	// claimBatch bounds how many jobs one round collects; see SetClaimBatch.
	claimBatch int
	// leaseFor is how long a claimed job is held; see SetLease.
	leaseFor int64
}

// DefaultConcurrency is how many handlers a round runs at once when nothing says
// otherwise. It is well above serial, so a burst still drains quickly, and well
// below the point where the sockets and memory of one round are a problem.
const DefaultConcurrency = 16

// InProcessWorker is the name the in-process runner leases jobs under. It is a
// name no external worker can present — an external worker's name comes from its
// own configuration, and this one names the engine itself — so a job leased here is
// visibly the runner's in every operator view that shows an assignee.
const InProcessWorker = "atlas:in-process"

// DefaultLease is how long the in-process runner holds a claimed job. A lease is a
// bound, not a lock (ADR-0007): if a handler outlives it the job is offered again,
// which is what makes a wedged handler recoverable without an operator. Five
// minutes is well past any handler that is going to return and well short of
// leaving a job stuck behind one that is not.
const DefaultLease = int64(5 * time.Minute)

// NewRunner creates a runner over a state store and the engine it feeds.
func NewRunner(store *state.Store, engine Engine) *Runner {
	return &Runner{
		store: store, engine: engine,
		factories:   map[int32]func(state.Reader) CompletingHandler{},
		Concurrency: DefaultConcurrency,
	}
}

// Handle registers an output-less worker for a job type. The type is the interned
// index the compiler assigned (cross-process, globally consistent job-type
// interning is a later concern).
func (r *Runner) Handle(jobType int32, build func(state.Reader) Handler) {
	r.factories[jobType] = func(rd state.Reader) CompletingHandler {
		h := build(rd)
		return func(j Job) (Completion, error) { return Completion{}, h(j) }
	}
}

// HandleWithOutput registers a worker whose completion writes output variables
// back into the instance (e.g. a service-task worker that returns variables). Same
// dispatch as Handle; the only difference is that its returned variables ride along
// on the CompleteJob command.
func (r *Runner) HandleWithOutput(jobType int32, build func(state.Reader) OutputHandler) {
	r.factories[jobType] = func(rd state.Reader) CompletingHandler {
		h := build(rd)
		return func(j Job) (Completion, error) {
			outputs, err := h(j)
			return Completion{Outputs: outputs}, err
		}
	}
}

// HandleCompleting registers a worker whose completion carries both output
// variables and a decision evaluation to retain (the DMN worker, ADR-0066). Same
// dispatch as the others; its Completion rides along on the CompleteJob command.
func (r *Runner) HandleCompleting(jobType int32, build func(state.Reader) CompletingHandler) {
	r.factories[jobType] = build
}

// Unhandle removes the in-process worker for a job type, so its jobs park for an
// external one instead (ADR-0168).
//
// It exists as a removal rather than a condition at each registration site because
// the registrations are spread across the server — a managed Worker Type
// registers through its own descriptor, the script languages through their loop,
// the rest inline — and a switch that has to be remembered at ten places is a switch
// that will be missed at one.
func (r *Runner) Unhandle(jobType int32) { delete(r.factories, jobType) }

// Handles reports whether an in-process worker is registered for a job type.
//
// It is what keeps one job from being worked twice: an external worker leasing by
// type must be refused a type this runner is already draining, because nothing
// else separates them — the runner does not lease, it simply dispatches whatever
// is activatable (ADR-0157). Relocating a kind to an external worker therefore
// means not registering its handler here.
func (r *Runner) Handles(jobType int32) bool {
	_, ok := r.factories[jobType]
	return ok
}

// DefaultClaimBatch is how many jobs one [Runner.Claim] collects before it stops
// and lets the round proceed. Every caller drives in a loop until a claim comes
// back empty, so the cap costs a round, not a job.
//
// It exists because Claim runs on the single writer and reads a record per job: an
// uncapped claim against a backlog of a hundred thousand held the writer for all of
// them, and every other instance, timer and health probe waited behind a burst that
// one round was never going to finish anyway (ADR-draft-bounded-job-polling).
const DefaultClaimBatch = 256

// errClaimFull stops a claim scan once the round's share is collected. A sentinel
// to break the scan early, not a failure — the scan's contract is that any non-nil
// error from the callback ends it.
var errClaimFull = errors.New("job: claim batch full")

// SetClaimBatch bounds how many jobs one Claim collects. Zero or less restores
// [DefaultClaimBatch].
func (r *Runner) SetClaimBatch(n int) { r.claimBatch = n }

// SetLease sets how long a claimed job is held, in nanoseconds. Zero or less
// restores [DefaultLease].
func (r *Runner) SetLease(d int64) { r.leaseFor = d }

// lease is the effective hold, defaulted.
func (r *Runner) lease() int64 {
	if r.leaseFor > 0 {
		return r.leaseFor
	}
	return DefaultLease
}

// claimBatchSize is the effective cap, defaulted.
func (r *Runner) claimBatchSize() int {
	if r.claimBatch > 0 {
		return r.claimBatch
	}
	return DefaultClaimBatch
}

// Claim leases up to a round's worth of activatable jobs of the registered types.
// It reads and writes engine state, so it runs on the goroutine that owns it — the
// run loop — and it does nothing slow: the work itself happens in [Runner.Work],
// off the loop.
//
// It *leases*, where it used to merely dispatch. The runner had no claim identity,
// so two callers driving at once would both be handed the same job and work it
// twice — which is why the server serialized driving end to end, holding one mutex
// across every handler's outbound call. A lease is the identity that makes the
// serialization unnecessary: the activation takes the job off the activatable index
// before this returns, so a second claim cannot see it
// (ADR-draft-in-process-job-leases).
//
// Each served type gets an equal share of the round rather than whatever is left
// after the types before it. Ranging a map is randomly ordered, so leaving it to
// chance would work *on average*, and "on average" is not what a job type flooded
// by its neighbour needs.
func (r *Runner) Claim() ([]Job, error) {
	share := r.claimBatchSize() / max(1, len(r.factories))
	if share < 1 {
		share = 1
	}
	var keys []uint64
	for jobType := range r.factories {
		before := len(keys)
		err := r.store.ActivatableJobs(jobType, func(k uint64) error {
			keys = append(keys, k)
			if len(keys)-before >= share {
				return errClaimFull
			}
			return nil
		})
		if err != nil && !errors.Is(err, errClaimFull) {
			return nil, err
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	// Lease them all, then apply: activating mutates the index the scan just read,
	// so the two are kept apart the same way the external pull keeps them apart.
	for _, k := range keys {
		r.engine.ActivateJob(k, InProcessWorker, r.lease())
	}
	if err := r.engine.RunUntilIdle(); err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(keys))
	for _, k := range keys {
		jv, ok, err := r.store.GetJob(k)
		if err != nil {
			return jobs, err
		}
		// Gone (completed meanwhile) or held by somebody else: not this round's.
		if !ok || jv.Assignee != InProcessWorker || jv.LeaseExpiresAt == 0 {
			continue
		}
		jobs = append(jobs, Job{
			Key:                k,
			Type:               jv.JobType,
			ProcessInstanceKey: jv.ProcessInstanceKey,
			ElementInstanceKey: jv.ElementInstanceKey,
			Retries:            jv.Retries,
			LeaseEpoch:         jv.LeaseEpoch,
		})
	}
	return jobs, nil
}

// holdsLease reports whether the job is still held by the round that claimed it.
// It is the in-process form of the check the HTTP completion endpoint makes on an
// external worker's report: the epoch is the fencing token, and a report from a
// round whose lease elapsed and was handed on presents a number the job has moved
// past (ADR-0007).
func (r *Runner) holdsLease(j Job) bool {
	jv, ok, err := r.store.GetJob(j.Key)
	if err != nil || !ok {
		return false
	}
	return jv.LeaseExpiresAt != 0 && jv.Assignee == InProcessWorker && jv.LeaseEpoch == j.LeaseEpoch && j.LeaseEpoch != 0
}

// Work runs the handlers for claimed jobs and returns what each produced. This is
// the part that must NOT hold the run loop: a handler makes the outbound call a
// worker exists for, and holding the single writer for its duration is the
// stall ADR-0155 measured and ADR-0157 step 6 removes.
//
// Handlers run concurrently, one goroutine per job. That is also what ends the
// amplification of the serial runner: a burst of parked jobs against a dead host
// used to cost one timeout after another, and now costs the slowest of them.
//
// reader is the round's consistent read view. Handlers are built against it here,
// so each sees one coherent state rather than whatever the writer has reached.
func (r *Runner) Work(jobs []Job, reader state.Reader) []Outcome {
	built := map[int32]CompletingHandler{}
	var runnable []Job
	for _, j := range jobs {
		if _, ok := built[j.Type]; !ok {
			factory, handled := r.factories[j.Type]
			if !handled {
				continue // an external worker's job; not ours to touch
			}
			built[j.Type] = factory(reader)
		}
		runnable = append(runnable, j)
	}
	outcomes := make([]Outcome, len(runnable))
	limit := r.Concurrency
	if limit <= 0 {
		limit = DefaultConcurrency
	}
	slots := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, j := range runnable {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			completion, err := built[j.Type](j)
			outcomes[i] = Outcome{Job: j, Completion: completion, Err: err}
		}()
	}
	wg.Wait()
	return outcomes
}

// Submit turns a round's outcomes into commands. It mutates engine state, so like
// [Runner.Claim] it runs on the run loop.
//
// A failure is routed into the incident model (ADR-0061) rather than returned: one
// job that cannot complete — a business rule task whose decision model is not
// deployed, say — must not poison the run, or every later deploy and completion
// that drives jobs would fail with it. FailJob retries while retries remain, then
// parks the token on an incident. The in-process runner retries immediately
// (backoff 0); a worker-supplied backoff arrives through the HTTP fail endpoint
// instead (ADR-0111).
func (r *Runner) Submit(outcomes []Outcome) {
	for _, o := range outcomes {
		// Only the round that still holds the lease may report on it. A handler that
		// outlived its lease has had the job handed on, and applying its outcome now
		// would be the double execution the lease exists to prevent — the same fence
		// the HTTP completion endpoint puts in front of an external worker's report
		// (ADR-draft-in-process-job-leases).
		if !r.holdsLease(o.Job) {
			continue
		}
		if o.Err != nil {
			r.engine.FailJob(o.Job.Key, o.Job.Retries-1, o.Err.Error(), 0)
			continue
		}
		if len(o.Completion.ToolCalls) > 0 {
			// An agent's round: the completion carries what to run next, not what the
			// job produced (ADR-0253). A completion naming no tool is not this case —
			// it is the agent finishing, and the ordinary path below completes the
			// container for it.
			r.engine.CompleteJobWithToolCalls(o.Job.Key, o.Completion.ToolCalls, o.Completion.Outputs...)
			continue
		}
		r.engine.CompleteJobWithDecision(o.Job.Key, o.Completion.Decision, o.Completion.Outputs...)
	}
}

// PollOnce claims, works and submits one round on the calling goroutine. It is the
// simple synchronous form — used by [Runner.Drive] and by callers that own the loop
// themselves; a server that must keep the loop free drives the three steps itself.
func (r *Runner) PollOnce() (int, error) {
	jobs, err := r.Claim()
	if err != nil {
		return 0, err
	}
	outcomes := r.Work(jobs, r.store)
	r.Submit(outcomes)
	return len(outcomes), nil
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
