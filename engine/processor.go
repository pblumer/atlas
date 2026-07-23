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
}

// New creates a processor for the given partition over an open log and store.
// A nil clock defaults to the system clock.
func New(partition uint16, log *wal.Log, store *state.Store, clock Clock) *Processor {
	if clock == nil {
		clock = SystemClock{}
	}
	p := &Processor{
		partition: partition,
		log:       log,
		store:     store,
		clock:     clock,
		keygen:    &keyGen{partition: partition},
		processes: map[uint64]*compiler.CompiledProcess{},
	}
	p.registerHandlers()
	p.registerBehaviors()
	return p
}

// Deploy registers an immutable compiled definition so instances can run it.
func (p *Processor) Deploy(cp *compiler.CompiledProcess) { p.processes[cp.Key] = cp }

// Undeploy removes a definition so no new instances of it can be created. It is
// the caller's responsibility not to undeploy a definition with running
// instances (they resolve their definition by key on every batch).
func (p *Processor) Undeploy(defKey uint64) { delete(p.processes, defKey) }

// SetJobNotifier installs the hook the service-task behavior triggers (after
// fsync) when a job of a type becomes available.
func (p *Processor) SetJobNotifier(fn func(jobType int32)) { p.jobNotifier = fn }

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

// CompleteJob enqueues completion of a job by a worker.
func (p *Processor) CompleteJob(jobKey uint64) {
	p.queue = append(p.queue, Command{
		Key:       jobKey,
		ValueType: model.VTJob,
		Intent:    model.IntentJobCompleted,
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

func (p *Processor) processBatch() error {
	p.batchRecords = p.batchRecords[:0]
	p.followups = p.followups[:0]
	p.sideEffects = p.sideEffects[:0]
	p.fatalErr = nil

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
