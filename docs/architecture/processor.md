# Processor

The processor is the heart of Chrampfer. It is a **single-threaded loop per partition** that turns commands into durable events and applies them to state. This document covers the batch cycle, the processing context that BPMN behaviors use, the state applier, recovery, and behavior dispatch.

## Mental model

One cycle of the loop:

```
1. Pull commands from the queue (until batch full or queue empty)
2. For each command: process → write events into a batch buffer,
   write state mutations into a transaction (not yet committed)
3. ONE append to the log + ONE fsync (group commit)
4. Commit the state transaction
5. Enqueue followup commands (generated during processing)
6. Run side effects (notify workers, send responses) — AFTER the fsync
```

The ordering of 3–6 is safety-critical: **make it durable first, make it visible second.** A job worker is notified only after the `JobCreated` event is on disk. Otherwise a worker could act on a job the engine forgets after a crash.

## The main loop

```go
type Processor struct {
    partitionId uint16
    cmdQueue    chan Command
    log         *LogStream
    state       *StateStore
    handlers    [256]ProcessorFn
    keyGen      *KeyGenerator
    clock       Clock

    batch       *Batch          // reused — no hot-path allocation
    sideEffects []SideEffect
}

func (p *Processor) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            p.drain()
            return
        case first := <-p.cmdQueue:
            p.processBatch(first)
        }
    }
}
```

One goroutine per partition. It blocks on the queue, then processes as many commands as are available in one pass.

## The batch cycle

```go
const maxBatchSize = 1024

func (p *Processor) processBatch(first Command) {
    p.batch.Reset()
    p.sideEffects = p.sideEffects[:0]

    tx := p.state.NewTransaction()
    defer tx.Close()

    // Phase 1: process (pure in-memory, no I/O)
    p.processOne(first, tx)
    for len(p.batch.records) < maxBatchSize {
        select {
        case cmd := <-p.cmdQueue:
            p.processOne(cmd, tx)
        default:
            goto flush
        }
    }

flush:
    if p.batch.IsEmpty() {
        return
    }
    // Phase 2: durability — the ONLY fsync in the cycle
    if err := p.log.Append(p.batch.records); err != nil {
        p.fatal(err)
    }
    p.log.Sync() // group commit: one fsync for the whole batch

    // Phase 3: make state visible
    if err := tx.Commit(); err != nil {
        p.fatal(err)
    }

    // Phase 4: enqueue followups (causal chain → next batch)
    for _, c := range p.batch.followups {
        p.cmdQueue <- c
    }

    // Phase 5: side effects AFTER fsync
    for _, se := range p.sideEffects {
        se.Run()
    }
}
```

Why it is shaped this way:

- **Phase 1 does zero I/O.** Pure in-memory processing against the transaction. Fast, and it lets us accumulate many commands before the expensive disk operation.
- **The `select`/`default` pattern is the group-commit heart.** Collect while commands are available; flush the instant the queue is empty. Self-tuning: low load → small batches, low latency; high load → large batches, max throughput. No timers, no tuning knobs.
- **Followups before side effects.** Internal followup commands (`SequenceFlowTaken → ActivateElement`) go back into the queue and are made durable in the *next* batch. Side effects (worker notification) run only after this batch is safely on disk.

## Processing one command

```go
func (p *Processor) processOne(cmd Command, tx *Transaction) {
    h := p.handlers[handlerIndex(cmd.ValueType, cmd.Intent)]
    if h == nil {
        p.rejectCommand(cmd, "no handler")
        return
    }
    ctx := &ProcessingContext{cmd: cmd, tx: tx, batch: p.batch, proc: p}
    h(ctx)
}
```

## The ProcessingContext API

This is the interface every BPMN behavior works through. A behavior may do exactly three things: read state, write events (= facts + state mutation), schedule what comes next. A behavior never writes to the log or calls fsync directly — it only accumulates into the batch.

```go
type ProcessingContext struct {
    cmd   Command
    tx    *Transaction
    batch *Batch
    proc  *Processor
}

// Read state (from the in-flight transaction; sees uncommitted changes)
func (c *ProcessingContext) ElementInstance(key uint64) *ElementInstanceValue {
    return c.tx.GetElementInstance(key)
}

// Write an event: creates a log record AND mutates state in one operation.
// This is the most important call — it keeps log and state in lockstep.
func (c *ProcessingContext) AppendFollowupEvent(key uint64, intent Intent, value Value) {
    rec := c.batch.NewRecord()
    rec.Key = key
    rec.RecordType = RecordEvent
    rec.Intent = intent
    rec.ValueType = value.Type()
    rec.SourcePos = c.cmd.Position
    rec.Timestamp = c.proc.clock.Now()
    value.Encode(&rec.payload)

    c.batch.records = append(c.batch.records, rec) // into the log batch
    applyToState(c.tx, rec)                         // AND mutate state
}

// Schedule an internal followup command (next batch)
func (c *ProcessingContext) AppendFollowupCommand(key uint64, intent Intent, value Value) {
    c.batch.followups = append(c.batch.followups, Command{
        Key: key, Intent: intent, ValueType: value.Type(),
        Position: c.cmd.Position, value: value,
    })
}

// Register a side effect (runs AFTER fsync)
func (c *ProcessingContext) SideEffect(fn func()) {
    c.proc.sideEffects = append(c.proc.sideEffects, SideEffect{Run: fn})
}

func (c *ProcessingContext) NewKey() uint64 { return c.proc.keyGen.Next() }
```

`AppendFollowupEvent` does two things atomically from the behavior's point of view: it writes the fact into the log batch *and* mirrors it into state, both from the same record. Log and state cannot diverge, because recovery runs the exact same `applyToState` over the log records.

## State applier — one truth for replay

`applyToState` is deliberately separate because it runs in **two** places: live in the processor, and during recovery replay. Same function, same logic → guaranteed consistency.

```go
// Runs both live and on recovery. Purely deterministic, no I/O beyond state.
func applyToState(tx *Transaction, rec *Record) {
    switch rec.ValueType {
    case VTElementInstance:
        v := decodeElementInstance(rec.payload)
        switch rec.Intent {
        case IntentActivating, IntentActivated:
            tx.PutElementInstance(rec.Key, v)
        case IntentCompleted, IntentTerminated:
            tx.DeleteElementInstance(rec.Key)
            tx.DecrementActiveChildren(v.FlowScopeKey)
        }
    case VTJob:
        switch rec.Intent {
        case IntentJobCreated:
            tx.PutJob(rec.Key, decodeJob(rec.payload))
            tx.IndexActivatableJob(rec.Key)
        case IntentJobCompleted, IntentJobFailed:
            tx.DeleteJob(rec.Key)
        }
    case VTTimer:
        switch rec.Intent {
        case IntentTimerCreated:
            tx.PutTimer(rec.Key, decodeTimer(rec.payload))
        case IntentTimerTriggered:
            tx.DeleteTimer(rec.Key)
        }
    // ... further value types
    }
}
```

## Recovery

Because state is a pure fold of events, recovery is trivial:

```go
func (p *Processor) Recover() error {
    lastCommitted := p.state.LastAppliedPosition()
    tx := p.state.NewTransaction()
    err := p.log.Iterate(lastCommitted+1, func(rec *Record) error {
        if rec.RecordType == RecordEvent {
            applyToState(tx, rec) // exactly the same function as live
        }
        return nil
    })
    if err != nil {
        return err
    }
    return tx.Commit()
}
```

Subtle but important: on replay, **commands are ignored, only events are applied.** Commands may have had non-deterministic effects (timestamps, random keys); events have frozen those effects already. That is why generated keys and timestamps are written into the events — on replay they are read from the log, not regenerated.

## Behavior dispatch

When an element instance goes to `ACTIVATING`, the *behavior* depends on the element type: a service task creates a job, a parallel gateway activates several outgoing flows, a timer event creates a timer.

Two dispatch stages: first `(ValueType, Intent)`, then for element instances additionally by `BpmnElementType`:

```go
func handleElementActivating(c *ProcessingContext) {
    ei := c.cmd.value.(*ElementInstanceValue)
    c.AppendFollowupEvent(c.cmd.Key, IntentActivated, ei) // lifecycle event first
    bpmnBehaviors[ei.BpmnElementType].OnActivated(c, ei)  // then type-specific behavior
}

type BpmnBehavior interface {
    OnActivated(c *ProcessingContext, ei *ElementInstanceValue)
    OnCompleting(c *ProcessingContext, ei *ElementInstanceValue)
    OnTerminating(c *ProcessingContext, ei *ElementInstanceValue)
}
```

Example — service task:

```go
func (serviceTaskBehavior) OnActivated(c *ProcessingContext, ei *ElementInstanceValue) {
    node := c.proc.process(ei.ProcessDefKey).Node(ei.ElementId)
    jobKey := c.NewKey()
    job := &JobValue{
        ProcessInstanceKey: ei.ProcessInstanceKey,
        ElementInstanceKey: c.cmd.Key,
        JobType:            node.JobType,
        Retries:            node.Retries,
    }
    c.AppendFollowupEvent(jobKey, IntentJobCreated, job)
    c.SideEffect(func() { c.proc.jobNotifier.Notify(node.JobType) }) // after fsync
    // stays ACTIVATED — no COMPLETING until a worker completes the job
}

func (serviceTaskBehavior) OnCompleting(c *ProcessingContext, ei *ElementInstanceValue) {
    c.AppendFollowupEvent(c.cmd.Key, IntentCompleted, ei)
    takeOutgoingFlows(c, ei) // activates next elements via the graph
}
```

## Performance notes

- **No allocation on the hot path.** The batch buffer, side-effect slice, and record payloads are reused across cycles. Records are pooled.
- **Single core, hot cache.** One goroutine touches the partition's state, so it stays in L2/L3.
- **No locks.** The single-writer model removes mutex contention entirely; the `CompiledProcess` is immutable and read without synchronization.
- **Backpressure** is natural: a full `cmdQueue` slows producers, which is the correct response to saturation.

See [ADR-0002](../adr/0002-single-writer-partition-model.md) and [ADR-0005](../adr/0005-group-commit-and-fsync-strategy.md).
