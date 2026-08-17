// Package engine is the heart of Atlas: a single-writer processor that folds
// commands into durable events and applies them to state.
//
// One partition is driven by one goroutine (invariant I3), so there are no locks
// on process state. Each batch follows the fixed order append → one fsync →
// commit state → side effects (invariants I2, ADR-0005). State changes from a
// record happen in exactly one place, applyToState, used identically live and on
// recovery (invariant I4), which is what makes crash recovery a simple replay.
//
// The processor path is allocation-free per command and per event (invariant
// I1): payloads flow by value (see inflightValue), and the batch buffers, queue,
// side-effect list, and encode buffer are reused across batches. State reads
// (which decode from the store) and the per-batch state transaction are the
// remaining allocation sources, tracked separately.
package engine

import (
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// stateTx aliases the state transaction type for brevity in the engine.
type stateTx = state.Tx

// Processor owns one partition's command processing.
type Processor struct {
	partition uint16
	log       *wal.Log
	store     *state.Store
	clock     Clock
	keygen    *keyGen

	processes map[uint64]*compiler.CompiledProcess
	handlers  map[uint16]func(*ProcessingContext)
	behaviors [compiler.NumBpmnTypes]bpmnBehavior

	// messageStarts indexes message start events by message name → the definitions
	// a correlating message instantiates (ADR-0035), each with the element the
	// message flows into so the collaboration replay can name the receiving edge
	// (ADR-0038). It is derived from the compiled definitions, rebuilt by Deploy on
	// every start (the deploy store re-registers definitions on recovery), so it
	// needs no durable state of its own — the instances it creates go through the
	// normal event path and recover from the log.
	messageStarts map[string][]messageStartRef

	// signalStarts indexes signal start events by signal name → the definition keys a
	// broadcast signal instantiates (ADR-0088), mirroring messageStarts. A signal has
	// no correlation key, so this needs only the definition key (no per-name flow or
	// singleton state). Like messageStarts it is derived from the compiled definitions,
	// rebuilt by Deploy on every start, so it needs no durable state of its own — the
	// instances it creates go through the normal event path and recover from the log.
	signalStarts map[string][]uint64

	// latestProcess indexes bpmn process id → the newest deployed definition key, so
	// a call activity with `latest` binding resolves the process to start as a child
	// (ADR-0076). Like messageStarts it is derived from the compiled definitions and
	// rebuilt by Deploy on every start; deployments reload oldest-first on recovery,
	// so the last write wins deterministically (the ADR-0063 binding argument, I6).
	latestProcess map[string]uint64

	// callOverrides redirects, pins, or disables a call activity's target resolution
	// per server, keyed by called bpmn process id (ADR-0105). Unlike latestProcess it
	// is NOT derived from deployments — it is operator config the server layer loads
	// from a sidecar at startup and pushes here via SetCallTargetOverride, owned by
	// this run-loop goroutine (I3). It affects only LIVE resolution: a child's chosen
	// def key is frozen into its create event (ADR-0076), so replay is unaffected and
	// this map need not live in the log (I6).
	callOverrides map[string]CallTargetOverride

	// inactive is the set of definition keys an operator has deactivated (ADR-0119):
	// a deactivated definition does not auto-start new instances from its timer,
	// message, or signal start events. Like callOverrides it is operator config the
	// server layer loads from a sidecar at startup and pushes here via SetProcessActive,
	// owned by this run-loop goroutine (I3). It gates only the LIVE decision to schedule
	// a create followup, never applyToState, so no Activated event is ever suppressed on
	// replay — the flag need not live in the log (I6).
	inactive map[uint64]bool

	jobNotifier func(jobType int32)

	queue        []Command
	queueScratch []Command // double-buffers queue so advancing it never allocates
	position     uint64    // highest log position assigned
	batchPos     int       // index in queue of the command being processed (for in-transit-token checks)

	// per-batch reused buffers
	tx           *stateTx
	ctx          ProcessingContext
	batchRecords []eventRecord
	followups    []Command
	sideEffects  []sideEffect
	encBuf       []byte
	fatalErr     error

	// condDirty collects the process instances whose variables changed this batch, so the
	// batch loop can schedule a conditional re-check for each (ADR-0134). Reused, not
	// reallocated; drained and cleared at the end of Phase 1.
	condDirty []uint64

	// startsThisBatch remembers the (defKey, correlationKey) pairs a singleton message
	// start has already scheduled a create for in the current batch (ADR-0094). The
	// durable ActiveStartKey counter only reflects an instance once its Activated
	// followup applies (a later batch), so within one batch several messages for the
	// same key would all read zero; this set closes that same-batch window. Cleared
	// each batch (reused, not reallocated).
	startsThisBatch map[startKeyIdent]struct{}
}

// startKeyIdent identifies a (definition, correlation key) pair for the per-batch
// singleton-start dedup set. Both fields are values, so it is a comparable map key
// with no allocation beyond the already-existing correlation-key string.
type startKeyIdent struct {
	defKey uint64
	key    string
}

// New creates a processor for the given partition over an open log and store.
// A nil clock defaults to the system clock.
func New(partition uint16, log *wal.Log, store *state.Store, clock Clock) *Processor {
	if clock == nil {
		clock = SystemClock{}
	}
	p := &Processor{
		partition:     partition,
		log:           log,
		store:         store,
		clock:         clock,
		keygen:        &keyGen{partition: partition},
		processes:     map[uint64]*compiler.CompiledProcess{},
		messageStarts: map[string][]messageStartRef{},
		signalStarts:  map[string][]uint64{},
		inactive:      map[uint64]bool{},
		latestProcess: map[string]uint64{},
		callOverrides: map[string]CallTargetOverride{},
	}
	p.registerHandlers()
	p.registerBehaviors()
	return p
}

// Deploy registers an immutable compiled definition so instances can run it, and
// indexes any message start events so a correlating message instantiates it
// (ADR-0035).
func (p *Processor) Deploy(cp *compiler.CompiledProcess) {
	p.processes[cp.Key] = cp
	// Newest definition per process id wins; deployments reload oldest-first on
	// recovery, so this is deterministic (ADR-0076).
	p.latestProcess[cp.ProcessId()] = cp.Key
	// A redeployment supersedes older versions of the same process id: only the
	// latest version may start on a message or signal. Without this, every deployed
	// version's start subscription stays live, so one incoming event instantiates the
	// process once per version — the reported "one request, N welcome e-mails" fan-out
	// when a model is iterated in the modeler. Timer starts already retire a prior
	// version's armed timers on re-arm (see handleTimerStartArm); message and signal
	// starts must match (ADR-0035/0076/0088).
	p.supersedeStarts(cp.Key)
	for _, ms := range cp.MessageStartEvents() {
		p.messageStarts[ms.MessageName] = append(p.messageStarts[ms.MessageName],
			messageStartRef{defKey: cp.Key, elementId: ms.ElementId, correlationKey: ms.CorrelationKey, singletonStart: ms.SingletonStart})
	}
	// Index signal start events too, so a broadcast signal instantiates them (ADR-0088).
	for _, ss := range cp.SignalStartEvents() {
		p.signalStarts[ss.SignalName] = append(p.signalStarts[ss.SignalName], cp.Key)
	}
}

// CallTargetOverride redirects, pins, or disables a call activity's target on this
// server (ADR-0105). Exactly one shape is meaningful per record; the resolution
// precedence (see ProcessingContext.resolveCallTarget) is Disabled, then
// PinnedDefKey, then RedirectProcessId, else the default `latest` resolution. It is
// operator config, not derived from deployments and not event-sourced — it changes
// only future resolutions; a child already created carries its frozen def key, so
// replay is unaffected (I6).
type CallTargetOverride struct {
	// Disabled parks the call (as an undeployed callee does) instead of resolving.
	Disabled bool
	// PinnedDefKey resolves to exactly this definition key (0 = not pinned). The
	// server layer picks the key from an operator-named version; the engine is
	// version-agnostic and simply uses it, parking if it is no longer deployed.
	PinnedDefKey uint64
	// RedirectProcessId resolves the newest deployment of this process id instead of
	// the called one ("" = no redirect). A redirect uses the default `latest`
	// resolution for its target (no chaining), so overrides cannot form a cycle.
	RedirectProcessId string
}

// SetCallTargetOverride installs (or replaces) the per-server override for a called
// process id. Must be called on the run-loop goroutine (the map's single owner):
// the server layer calls it at startup and on an admin change (ADR-0105).
func (p *Processor) SetCallTargetOverride(calledProcessId string, ov CallTargetOverride) {
	p.callOverrides[calledProcessId] = ov
}

// ClearCallTargetOverride removes a called process id's override, restoring the
// default `latest` resolution. Idempotent. Run-loop goroutine only.
func (p *Processor) ClearCallTargetOverride(calledProcessId string) {
	delete(p.callOverrides, calledProcessId)
}

// SetProcessActive marks a deployed definition active or inactive (ADR-0119). An
// inactive definition does not auto-start new instances when its timer, message, or
// signal start events fire; existing instances run to completion, and an explicit
// operator/API start is unaffected. The server layer loads the flag from the
// deployment sidecar at startup and calls this on an operator toggle. Like
// SetCallTargetOverride it is operator config, not event-sourced: it changes only the
// live decision to schedule a create, so replay is unaffected (I6). Run-loop goroutine
// only (the map's single owner).
func (p *Processor) SetProcessActive(defKey uint64, active bool) {
	if active {
		delete(p.inactive, defKey)
		return
	}
	p.inactive[defKey] = true
}

// ProcessActive reports whether a definition may auto-start new instances — the
// inverse of the ADR-0119 deactivation flag. A key that was never deactivated (the
// default) is active. Run-loop goroutine only.
func (p *Processor) ProcessActive(defKey uint64) bool {
	return !p.inactive[defKey]
}

// Undeploy removes a definition so no new instances of it can be created,
// dropping its message-start index entries too. It is the caller's
// responsibility not to undeploy a definition with running instances (they
// resolve their definition by key on every batch).
func (p *Processor) Undeploy(defKey uint64) {
	if cp := p.processes[defKey]; cp != nil {
		for _, ms := range cp.MessageStartEvents() {
			p.messageStarts[ms.MessageName] = removeStartRef(p.messageStarts[ms.MessageName], defKey)
		}
		for _, ss := range cp.SignalStartEvents() {
			p.signalStarts[ss.SignalName] = removeSignalStart(p.signalStarts[ss.SignalName], defKey)
		}
	}
	delete(p.processes, defKey)
	// Drop any deactivation for this key so a future definition reusing it (there is
	// none today — keys are monotonic) never inherits a stale inactive flag.
	delete(p.inactive, defKey)
}

// messageStartRef points a starting message at the definition it instantiates
// and the message-start element it flows into (for the collaboration replay).
// correlationKey is the event's compiled correlation-key expression, evaluated
// over the starting message's payload so the created instance records the key it
// began with (ADR-0020); nil when the event declares none.
type messageStartRef struct {
	defKey         uint64
	elementId      int32
	correlationKey *expr.Compiled
	// singletonStart gates instantiation on there being no live instance of defKey
	// already started with the same correlation key (ADR-0094); false = ADR-0035's
	// start-per-message default.
	singletonStart bool
}

// supersedeStarts drops every message- and signal-start index entry that points at
// an older version of newKey's process id, so redeploying a process leaves only its
// latest version able to start on an incoming message or signal. newKey must already
// be in p.processes. Entries for a *different* process id that happens to share a
// message or signal name are kept — only same-process-id older versions are retired —
// so two distinct processes started by the same event both keep starting. Rebuilding
// the index by replaying deploys oldest-first therefore converges on the latest
// version deterministically (ADR-0076), matching how a re-armed timer start retires a
// prior version's timers (handleTimerStartArm).
func (p *Processor) supersedeStarts(newKey uint64) {
	cp := p.processes[newKey]
	if cp == nil {
		return
	}
	pid := cp.ProcessId()
	// olderVersion reports whether defKey is a different, same-process-id definition
	// than the one being deployed — i.e. a version this deploy supersedes.
	olderVersion := func(defKey uint64) bool {
		if defKey == newKey {
			return false
		}
		other := p.processes[defKey]
		return other != nil && other.ProcessId() == pid
	}
	for name, refs := range p.messageStarts {
		kept := refs[:0:0]
		for _, r := range refs {
			if !olderVersion(r.defKey) {
				kept = append(kept, r)
			}
		}
		if len(kept) == 0 {
			delete(p.messageStarts, name)
		} else {
			p.messageStarts[name] = kept
		}
	}
	for name, keys := range p.signalStarts {
		kept := keys[:0:0]
		for _, k := range keys {
			if !olderVersion(k) {
				kept = append(kept, k)
			}
		}
		if len(kept) == 0 {
			delete(p.signalStarts, name)
		} else {
			p.signalStarts[name] = kept
		}
	}
}

// removeStartRef returns refs with the first entry for defKey removed. A name
// whose last message-start definition is undeployed keeps an empty slice, which
// correlates to nothing — harmless and rare, so it is not pruned from the map.
func removeStartRef(refs []messageStartRef, defKey uint64) []messageStartRef {
	for i, r := range refs {
		if r.defKey == defKey {
			return append(refs[:i], refs[i+1:]...)
		}
	}
	return refs
}

// removeSignalStart returns keys with the first entry for defKey removed — the
// signal-start counterpart of removeStartRef (ADR-0088). A name whose last
// signal-start definition is undeployed keeps an empty slice, which instantiates
// nothing; harmless and rare, so it is not pruned from the map.
func removeSignalStart(keys []uint64, defKey uint64) []uint64 {
	for i, k := range keys {
		if k == defKey {
			return append(keys[:i], keys[i+1:]...)
		}
	}
	return keys
}

// SetJobNotifier installs the hook the service-task behavior triggers (after
// fsync) when a job of a type becomes available.
func (p *Processor) SetJobNotifier(fn func(jobType int32)) { p.jobNotifier = fn }

// ArmStartTimers enqueues arming of a freshly deployed definition's timer start
// events: the handler creates their durable timers and retires any that a prior
// version of the same process left armed, so only the latest version's schedule
// is active (ADR-0051). Call it once per *fresh* deploy (not on recovery — the
// restored TimerCreated events already hold the armed timers), then RunUntilIdle
// (or Drive) to process it. It scans the armed start timers, so callers skip it
// for a first-version process with no timer start events (nothing to arm or
// supersede); a re-version still calls it so a removed schedule is retired.
func (p *Processor) ArmStartTimers(defKey uint64) {
	p.queue = append(p.queue, Command{
		Key:       defKey,
		ValueType: model.VTTimer,
		Intent:    model.IntentTimerStartArm,
	})
}

// CreateInstance enqueues creation of a new instance of the given definition,
// optionally seeded with initial variables. Call RunUntilIdle to process it.
func (p *Processor) CreateInstance(defKey uint64, startVars ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		Value:     inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: defKey}},
		StartVars: startVars,
	})
}

// CompleteJob enqueues completion of a job by a worker, optionally carrying the
// output variables the worker produced (e.g. a business rule task's decision
// result). The outputs are written into the job's process instance scope when the
// completion is processed, before the element completes, so a downstream gateway
// can route on them. They are frozen into VariableCreated events, so replay
// re-applies them without re-running the worker (invariant I6).
func (p *Processor) CompleteJob(jobKey uint64, outputs ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		Key:       jobKey,
		ValueType: model.VTJob,
		Intent:    model.IntentJobCompleted,
		StartVars: outputs,
	})
}

// CompleteJobWithDecision completes a business rule task's job like CompleteJob,
// additionally carrying the DMN decision evaluation the worker produced (ADR-0066):
// its inputs, outputs, and trace, frozen into a history event when the completion
// is folded so an operator can later inspect how the decision was made. decision
// may be nil, in which case this behaves exactly like CompleteJob.
func (p *Processor) CompleteJobWithDecision(jobKey uint64, decision *model.DecisionEvaluationValue, outputs ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		Key:       jobKey,
		ValueType: model.VTJob,
		Intent:    model.IntentJobCompleted,
		StartVars: outputs,
		Decision:  decision,
	})
}

// SetVariables enqueues an external, operator-initiated write of variables onto a
// running instance's scope (ADR-0095): each variable is created if its name is new
// in the target scope or overwritten if it already exists. piKey is the process
// instance; scopeKey is the scope the variables land in — pass piKey (or 0, which
// the handler treats as piKey) for the instance root scope, or a live element
// instance key belonging to piKey for a subprocess/multi-instance-body local scope.
// The writes are frozen into VariableCreated/VariableUpdated events, so they replay
// without re-running this command (invariant I6) and appear in the instance's
// variable timeline as the audit trail. Setting variables on an instance that is
// gone (finished or never existed), or on a scope that does not belong to it, is a
// no-op. It does not re-evaluate any gateway a token has already passed — it only
// changes the stored values. Each variable set is additionally recorded as an audit
// event naming actor — who made the change (ADR-0098) — so the "who changed it" trail
// is durable; pass "" when the caller is unidentified. Call RunUntilIdle (or Drive)
// to process it.
func (p *Processor) SetVariables(piKey, scopeKey uint64, actor string, vars ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		Key:       piKey,
		ValueType: model.VTVariable,
		Intent:    model.IntentVariableModify,
		Value:     inflightValue{variable: model.VariableValue{ScopeKey: scopeKey}},
		StartVars: vars,
		Actor:     actor,
	})
}

// FailJob enqueues a worker's failure report for a job (ADR-0061), carrying the
// retries the worker leaves it, a failure message, and a retry backoff (unix-nanoseconds;
// 0 = retry immediately, ADR-0111). With retries > 0 the job is retried — immediately if
// backoff is 0, otherwise held off the activatable index until a retry timer fires backoff
// nanoseconds later; with retries <= 0 an incident is raised on the job's element and the
// token parks there. Failing a job that no longer exists is a no-op. Call RunUntilIdle (or
// Drive) to process it.
func (p *Processor) FailJob(jobKey uint64, retries int32, message string, backoff int64) {
	p.queue = append(p.queue, Command{
		Key:       jobKey,
		ValueType: model.VTJob,
		Intent:    model.IntentJobFailed,
		Value: inflightValue{
			job:      model.JobValue{Retries: retries},
			incident: model.IncidentValue{Message: message},
		},
		RetryBackoff: backoff,
	})
}

// ThrowJobError enqueues a worker's report that its job threw a BPMN error code
// (ADR-0089) — the "throw BPMN error" verb, a sibling of FailJob. Instead of retrying or
// raising an incident, the handler cancels the job and propagates the error from the job's
// element to the nearest matching error boundary or error event subprocess (or, uncaught,
// raises an incident). The code rides in the command's incident.Message field, a transient
// command carrier that is never persisted. Throwing on a job that no longer exists is a
// no-op. Call RunUntilIdle (or Drive) to process it.
func (p *Processor) ThrowJobError(jobKey uint64, errorCode string) {
	p.queue = append(p.queue, Command{
		Key:       jobKey,
		ValueType: model.VTJob,
		Intent:    model.IntentJobErrorThrown,
		Value:     inflightValue{incident: model.IncidentValue{Message: errorCode}},
	})
}

// ResolveIncident enqueues an operator's resolution of the incident attached to
// elementKey (ADR-0061): the incident is cleared and its job re-created with
// retries (>= 1), returning it to the activatable index so a worker retries it.
// Resolving an incident that no longer exists is a no-op. Call RunUntilIdle (or
// Drive) to process it.
func (p *Processor) ResolveIncident(elementKey uint64, retries int32) {
	p.queue = append(p.queue, Command{
		Key:       elementKey,
		ValueType: model.VTIncident,
		Intent:    model.IntentIncidentResolved,
		Value:     inflightValue{job: model.JobValue{Retries: retries}},
	})
}

// AssignJob enqueues a (re)assignment of a user task's assignee, identified by
// its job key. A non-empty assignee is a claim; an empty one unclaims the task,
// making it available again. The job stays open either way. Assigning a job that
// no longer exists is a no-op. Call RunUntilIdle to process it (ADR-0042).
func (p *Processor) AssignJob(jobKey uint64, assignee string) {
	p.queue = append(p.queue, Command{
		Key:       jobKey,
		ValueType: model.VTJob,
		Intent:    model.IntentJobAssigned,
		Value:     inflightValue{job: model.JobValue{Assignee: assignee}},
	})
}

// CancelInstance enqueues termination of a running process instance: every
// active element instance is terminated and the instance is recorded as
// terminated in history (ADR-0017). Any timer/subscription/job the instance left
// waiting is self-retiring — when it later fires or correlates it finds no
// element and does nothing. Call RunUntilIdle to process it.
func (p *Processor) CancelInstance(piKey uint64) {
	p.queue = append(p.queue, Command{
		Key:       piKey,
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentTerminating,
	})
}

// PurgeInstance enqueues the hard delete of a finished instance's history (ADR-0115):
// its terminal record and every per-instance family are removed from the state store
// through a durable IntentPurged event, so the deletion replays on recovery. The
// retention sweep calls it for an instance it has already read from the history index
// and gated on age + exported position; the carried value supplies the definition key
// the cleanup needs. Purging an instance that is not (or no longer) in history is a
// harmless no-op — the deletes are idempotent. Call RunUntilIdle to process it.
func (p *Processor) PurgeInstance(piKey uint64, pi *model.ProcessInstanceValue) {
	p.queue = append(p.queue, Command{
		Key:       piKey,
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentPurging,
		Value:     inflightValue{process: *pi},
	})
}

// PublishMessage enqueues publication of a message with the given name and
// correlation key, optionally carrying payload variables that are written into
// every correlated instance's scope. It correlates against open subscriptions
// through the same path a message throw event uses; a message that matches no
// subscription is a no-op (no buffering yet, ADR-0020). Call RunUntilIdle to
// process it.
func (p *Processor) PublishMessage(name, correlationKey string, vars ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTMessage,
		Intent:    model.IntentMessagePublished,
		Value:     inflightValue{subscription: model.MessageSubscriptionValue{MessageName: name, CorrelationKey: correlationKey}},
		StartVars: vars,
	})
}

// PublishInbound enqueues publication of a message that originated from an external
// event source (ADR-0075), carrying the source's identity (sourceID) and monotonic
// sequence (seq) so the publish is deduplicated against the source's durable
// high-water mark: a replayed at-least-once delivery is skipped rather than
// re-correlated (which would double-start a message-start process). Apart from the
// dedup guard it correlates exactly like PublishMessage. Call RunUntilIdle to
// process it.
func (p *Processor) PublishInbound(sourceID string, seq uint64, name, correlationKey string, vars ...model.VariableValue) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTMessage,
		Intent:    model.IntentMessagePublished,
		Value: inflightValue{
			subscription: model.MessageSubscriptionValue{MessageName: name, CorrelationKey: correlationKey},
			inbound:      model.InboundDeliveryValue{SourceID: sourceID, SourceSeq: seq},
		},
		StartVars: vars,
	})
}

// TriggerDueTimers enqueues a trigger command for every timer due at or before
// the current clock, carrying each timer's value so the handler needs no extra
// read. Call RunUntilIdle (or TickTimers) to process them. It is time-driven, so
// it belongs off the command path — a scheduler calls it periodically.
func (p *Processor) TriggerDueTimers() error {
	now := p.clock.Now()
	type due struct {
		key uint64
		v   model.TimerValue
	}
	var fire []due
	if err := p.store.DueTimers(now, func(k uint64, v *model.TimerValue) error {
		fire = append(fire, due{key: k, v: *v})
		return nil
	}); err != nil {
		return err
	}
	for _, d := range fire {
		p.queue = append(p.queue, Command{
			Key:       d.key,
			ValueType: model.VTTimer,
			Intent:    model.IntentTimerTriggered,
			Value:     inflightValue{timer: d.v},
		})
	}
	return nil
}

// TickTimers fires all due timers and processes the resulting work to idle. A
// server scheduler calls it on the partition's goroutine (invariant I3).
func (p *Processor) TickTimers() error {
	if err := p.TriggerDueTimers(); err != nil {
		return err
	}
	return p.RunUntilIdle()
}

// RunUntilIdle processes batches until the queue (including generated followups)
// drains. Deterministic and synchronous — the basis for tests and simple
// embedding; the channel-driven concurrent loop arrives with the API milestone.
func (p *Processor) RunUntilIdle() error {
	for len(p.queue) > 0 {
		if err := p.processBatch(); err != nil {
			return err
		}
	}
	return nil
}

// scheduleConditionRechecks enqueues a transient ConditionRecheck follow-up for each instance
// whose variables changed this batch and that has conditional events (ADR-0134). The re-check
// runs in the next batch and reads the now-committed variables; its firing is the persisted
// Completing chain, so it is deterministic and never runs on replay (I6).
func (p *Processor) scheduleConditionRechecks() {
	for _, piKey := range p.condDirty {
		p.followups = append(p.followups, Command{
			Key:       piKey,
			ValueType: model.VTProcessInstance,
			Intent:    model.IntentConditionRecheck,
		})
	}
}

func (p *Processor) processBatch() error {
	p.batchRecords = p.batchRecords[:0]
	p.followups = p.followups[:0]
	p.sideEffects = p.sideEffects[:0]
	p.condDirty = p.condDirty[:0]
	p.fatalErr = nil
	for k := range p.startsThisBatch {
		delete(p.startsThisBatch, k) // reuse the map; empty by the next batch (ADR-0094)
	}

	tx := p.store.NewTransaction()
	p.tx = tx

	// Phase 1: process commands (pure in-memory, no I/O).
	n := 0
	for n < len(p.queue) && n < maxBatchSize {
		p.batchPos = n
		p.processOne(p.queue[n])
		n++
		if p.fatalErr != nil {
			tx.Close()
			return p.fatalErr
		}
	}

	// A variable write in an instance with conditional events schedules a re-check of that
	// instance's armed conditionals as a follow-up (ADR-0134). It runs in the next batch, on
	// the live command path only (never replay, I6); firing is the persisted Completing chain.
	p.scheduleConditionRechecks()

	if len(p.batchRecords) == 0 {
		// Nothing durable to write (e.g. a command with no handler). Advance
		// past the consumed commands and queue any followups.
		tx.Close()
		p.advanceQueue(n)
		return nil
	}

	// Phase 2: durability — encode events, append, then the ONLY fsync.
	for i := range p.batchRecords {
		er := &p.batchRecords[i]
		rec := model.Record{Header: er.header, Value: er.value.asValue(er.header.ValueType)}
		p.encBuf = model.AppendRecord(p.encBuf[:0], &rec)
		if err := p.log.Append(p.encBuf); err != nil {
			tx.Close()
			return err
		}
	}
	if err := p.log.Sync(); err != nil {
		tx.Close()
		return err
	}

	// Phase 3: make state visible, recording the applied position atomically.
	lastPos := p.batchRecords[len(p.batchRecords)-1].header.Position
	if err := tx.SetLastAppliedPosition(lastPos); err != nil {
		tx.Close()
		return err
	}
	if err := tx.Commit(); err != nil {
		tx.Close()
		return err
	}
	tx.Close()

	// Phase 4: followups go to the next batch; Phase 5: side effects post-fsync.
	p.advanceQueue(n)
	for _, se := range p.sideEffects {
		p.notifyJobAvailable(se.jobType)
	}
	return nil
}

func (p *Processor) processOne(cmd Command) {
	h := p.handlers[handlerKey(cmd.ValueType, cmd.Intent)]
	if h == nil {
		return // unknown command: rejected (not persisted)
	}
	p.ctx = ProcessingContext{cmd: cmd, tx: p.tx, p: p, lastPos: cmd.SourcePos}
	h(&p.ctx)
}

// advanceQueue drops the n consumed commands and appends this batch's followups,
// reusing a scratch buffer so it does not allocate once warmed.
func (p *Processor) advanceQueue(n int) {
	p.queueScratch = append(p.queueScratch[:0], p.queue[n:]...)
	p.queueScratch = append(p.queueScratch, p.followups...)
	p.queue, p.queueScratch = p.queueScratch, p.queue
}

func (p *Processor) fail(err error) {
	if err != nil && p.fatalErr == nil {
		p.fatalErr = err
	}
}

func (p *Processor) notifyJobAvailable(jobType int32) {
	if p.jobNotifier != nil {
		p.jobNotifier(jobType)
	}
}

// Recover rebuilds in-memory position/key state and catches the store up to the
// log. It replays events after the store's last applied position through the
// same applyToState used live (invariant I4), and restores the key counter and
// log position from what the log already froze (invariant I6). Call once after
// New, before processing.
func (p *Processor) Recover() error {
	lastApplied, err := p.store.LastAppliedPosition()
	if err != nil {
		return err
	}
	tx := p.store.NewTransaction()
	defer tx.Close()

	maxPos := lastApplied
	maxApplied := lastApplied
	var maxCounter uint64
	applied := false

	if err := p.log.Replay(func(data []byte) error {
		rec, err := model.ReadRecord(data)
		if err != nil {
			return err
		}
		h := rec.Header
		if h.Position > maxPos {
			maxPos = h.Position
		}
		if model.PartitionOf(h.Key) == p.partition {
			if cnt := model.CounterOf(h.Key); cnt > maxCounter {
				maxCounter = cnt
			}
		}
		if h.RecordType != model.RecordEvent || h.Position <= lastApplied {
			return nil // commands aren't replayed; already-applied events are skipped
		}
		iv := inflightFromRecord(rec)
		if err := applyToState(tx, h, &iv); err != nil {
			return err
		}
		applied = true
		if h.Position > maxApplied {
			maxApplied = h.Position
		}
		return nil
	}); err != nil {
		return err
	}

	if applied {
		if err := tx.SetLastAppliedPosition(maxApplied); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	p.position = maxPos
	p.keygen.counter = maxCounter
	return nil
}
