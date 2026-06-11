# Chrampfer Architecture

This document describes the architecture of Chrampfer, a durable, high-throughput BPMN 2.x workflow engine written in Go. It is the canonical reference for how the system is structured and why. For the reasoning behind specific decisions, see the [Architecture Decision Records](adr/).

## Table of contents

1. [Design philosophy](#design-philosophy)
2. [System overview](#system-overview)
3. [The three pillars](#the-three-pillars)
4. [Execution model](#execution-model)
5. [Data model](#data-model)
6. [The processor](#the-processor)
7. [The graph compiler](#the-graph-compiler)
8. [State store and indexes](#state-store-and-indexes)
9. [Partitioning and scaling](#partitioning-and-scaling)
10. [Durability and recovery](#durability-and-recovery)
11. [Job workers and external tasks](#job-workers-and-external-tasks)
12. [BPMN coverage](#bpmn-coverage)
13. [Failure handling and incidents](#failure-handling-and-incidents)
14. [Observability](#observability)
15. [Component map](#component-map)

---

## Design philosophy

Chrampfer is built on four convictions, each of which shapes the whole system. They are not independent features bolted together — they reinforce one another.

**Compile, don't interpret.** A BPMN model is parsed and validated exactly once, at deploy time, and turned into a flat, integer-indexed execution graph. At runtime there is no XML, no string lookups, no map access on the hot path — only array indexing over cache-friendly slices. The expensive work happens when a human is watching (deployment); the cheap work happens millions of times (execution).

**Event sourcing over state mutation.** State is never overwritten in place. Every meaningful transition is appended as an immutable event to a write-ahead log. The current state is a *materialization* (a fold) of that log. This makes the log the single source of truth, gives us crash recovery almost for free, and removes an entire class of "did the DB write and the in-memory change both succeed?" consistency bugs.

**Group commit for durability.** Durability comes from `fsync`, and `fsync` is expensive. Doing one per event caps throughput at a few thousand per second. Chrampfer batches many events and makes them durable with a *single* `fsync`, so throughput scales with batch size, not with disk round-trips. Under low load batches are small (low latency); under high load batches grow (high throughput). The system self-tunes.

**Single writer per partition.** Each partition is driven by exactly one goroutine that processes commands sequentially. This sounds counter-intuitive for a "fast" system, but it eliminates lock contention, keeps state access on one hot CPU core, and makes execution deterministic — which in turn makes recovery a trivial log replay. Horizontal scale comes from running many independent partitions, not from threading a single one.

See [ADR-0001](adr/0001-event-sourcing-and-log-structured-state.md), [ADR-0002](adr/0002-single-writer-partition-model.md), and [ADR-0004](adr/0004-compile-bpmn-to-indexed-graph.md) for the full rationale.

## System overview

```
                          ┌─────────────────────────────────────────────┐
                          │                  Chrampfer                   │
                          │                                              │
   Deploy BPMN  ────────► │  ┌────────────────┐                         │
                          │  │  Graph Compiler │  (once per deployment)  │
                          │  └───────┬────────┘                         │
                          │          │ immutable CompiledProcess         │
                          │          ▼                                   │
   Client commands ─────► │  ┌────────────────────────────────────────┐ │
   (start instance,       │  │            Partition 0..N               │ │
    publish message,      │  │  ┌──────────────────────────────────┐   │ │
    complete job)         │  │  │   Single-writer Processor loop    │   │ │
                          │  │  │   - batch commands                │   │ │
                          │  │  │   - mutate state (in-memory tx)   │   │ │
                          │  │  │   - emit events                   │   │ │
                          │  │  └────────┬─────────────┬───────────┘   │ │
                          │  │           │             │                │ │
                          │  │   ┌───────▼──────┐  ┌───▼──────────┐    │ │
                          │  │   │   WAL (log)  │  │  State store │    │ │
                          │  │   │ append+fsync │  │  (Pebble)    │    │ │
                          │  │   └──────────────┘  └──────────────┘    │ │
                          │  └────────────────────────────────────────┘ │
                          │          │ jobs                              │
                          └──────────┼──────────────────────────────────┘
                                     ▼
                          External job workers (gRPC stream)
```

A client never talks to the state store directly. Everything is a **command** submitted to a partition. The processor turns commands into **events**, makes them durable, and applies them to state. External work (service tasks) is handed out to **job workers** as jobs, and their results come back as new commands.

## The three pillars

Chrampfer's core is three tightly-coupled subsystems. The interfaces between them are deliberately narrow.

| Pillar | Responsibility | Key property |
|--------|----------------|--------------|
| **Graph compiler** | BPMN XML → immutable, integer-indexed `CompiledProcess` | Runs once per deploy; output is read-only and lock-free |
| **Processor** | Move tokens through the graph as a fold over an event log | Single-writer, deterministic, batched group-commit |
| **Data model** | Every transition is a keyed record `(ValueType, Intent)` | Same `applyToState` runs live and on recovery |

The seams where they meet are the parts to get right:

- `compiledNode.JobType`, `Outgoing(nodeId)`, `FlowScope` — compiler → processor
- `applyToState(tx, record)` — data model → processor, used both live and during recovery
- `FlowScope` / `ChildCount` — compiler → processor scope bookkeeping

## Execution model

Chrampfer uses a **token model** consistent with the BPMN specification, but tokens are not heap-allocated objects. A token is the presence of an active *element instance* in the state store. Token movement is the creation and completion of element instances, recorded as events.

### The element-instance lifecycle

Every BPMN element an active token passes through goes through an explicit lifecycle, modeled as an intent sequence:

```
ACTIVATING ──► ACTIVATED ──► COMPLETING ──► COMPLETED
                   │
                   └────────► TERMINATING ──► TERMINATED   (e.g. interrupted by a boundary event)
```

This lifecycle is not bureaucracy — it is precisely what makes subprocesses, boundary events, and input/output mappings clean:

- A **service task** becomes `ACTIVATED` and then *waits* (a job is created); it only moves to `COMPLETING` when a worker completes that job.
- A **subprocess** is `ACTIVATED` while its children run, and only moves to `COMPLETING` when its last child reaches `COMPLETED`.
- An **interrupting boundary event** drives the attached scope to `TERMINATING`.

### Token flow

Token movement is a loop of events:

```
ElementInstance(COMPLETING)
  └─► takeOutgoingFlows: one SequenceFlowTaken event per outgoing flow
        └─► one "activate element" command per target node (followup)
              └─► ElementInstance(ACTIVATING) → ACTIVATED → behavior.OnActivated
                    └─► (task waits / gateway decides / next element activates)
```

Gateways are simply behaviors with custom `takeOutgoingFlows` logic: an exclusive gateway evaluates conditions and takes *one* flow; a parallel gateway takes *all* and, at the join, relies on a counter in the scope state.

## Data model

Three distinct concepts that are often conflated:

- **Command** — an *intention* to do something. May be rejected. Comes from outside or from the processor itself. Not persisted.
- **Event** — a *fact* that something happened. Never rejected. Appended to the log. The only thing persisted.
- **State** — the materialization of all events, living in the key-value store.

The flow is `Command → processor → Events → (state mutation + log append)`. The crucial invariant: **commands are processed, but only events are persisted.** On recovery we replay events, not commands, because events have already "frozen" any non-deterministic effects (timestamps, generated keys).

### Record header

Every log entry shares a compact header. Integer keys, not strings:

```go
type RecordHeader struct {
    Position    uint64    // monotonic log position (sequence number)
    SourcePos   uint64    // position of the record that caused this one (causality)
    Key         uint64    // entity key (instance, element instance, job, ...)
    Timestamp   int64     // unix nano — written into the event, replayed not regenerated
    RecordType  RecordType // Command | Event | CommandRejection
    ValueType   ValueType  // ProcessInstance | ElementInstance | Job | Timer | ...
    Intent      Intent     // Activating | Completing | Created | Triggered | ...
    PartitionId uint16
}
```

### Keys encode their partition

Every stateful entity gets a globally unique 64-bit key with the partition baked in, so routing is a bit-shift, not a lookup:

```
Key layout: [16 bit partition][48 bit monotonic counter]
```

### ValueType × Intent

Rather than a deep struct hierarchy, the `(ValueType, Intent)` pair is the discriminator. The full set is documented in [the data model reference](architecture/data-model.md). The element-instance lifecycle intents (`Activating → Activated → Completing → Completed`, plus `Terminating → Terminated`) are the heart of the model.

### Referencing, not copying

Payloads reference shared state instead of copying it. Variables live in their own variable state, referenced by scope key — they are never embedded in a job record. This keeps the log small and avoids writing the same data many times over.

## The processor

The processor is a **single-threaded loop per partition**. One cycle:

1. Pull commands from the queue (until the batch is full or the queue is empty).
2. For each command: process it, writing events into a batch buffer and state mutations into an uncommitted transaction.
3. Append the batch to the log and issue **one** `fsync` (group commit).
4. Commit the state transaction.
5. Enqueue followup commands generated during processing.
6. Run side effects (notify workers, send responses) — *after* the fsync.

The ordering of 3–6 is the safety-critical part: **make it durable first, make it visible second.** A job worker is notified only after the `JobCreated` event is safely on disk; otherwise a worker could act on a job the engine "forgets" after a crash.

The batch loop self-tunes via a `select`/`default` pattern: as long as commands are available, keep collecting; the moment the queue is empty, flush. Low load → small batches, low latency. High load → large batches, maximum throughput.

Full detail, including the `ProcessingContext` API that BPMN behaviors use, is in [the processor reference](architecture/processor.md).

## The graph compiler

The compiler runs once per deployment and produces an immutable `CompiledProcess` that many goroutines read concurrently without locks. The pipeline:

```
BPMN XML
  → Parse:        XML → raw object tree
  → Resolve:      string IDs → integer indices, wire up flows (two passes for forward refs)
  → Intern:       all strings (job types, variable names, message names) → index tables
  → Compile expr: FEEL conditions/mappings → prepared expression trees / bytecode
  → Validate:     reachability, gateway coverage, scope consistency
  → Linearize:    pour into flat, indexed slices
  → CompiledProcess (immutable, GC-light, cache-friendly)
```

Two structural decisions matter most for speed:

- **Struct-of-arrays topology.** Outgoing/incoming flows are not per-node slices (which would mean thousands of tiny heap allocations and cache misses). They live in two shared, densely-packed arrays that nodes index into via offset+count.
- **Detail tables instead of fat nodes.** A `CompiledNode` stays small (close to a cache line); type-specific data (service task, timer, gateway) lives in separate tables grouped by type, also cache-friendly.

BPMN's hierarchy (subprocesses, attached boundary events) is flattened into index references: each node carries a `FlowScope` index, and each scope carries a `ChildCount`. At runtime, subprocess completion reduces to *one integer counter per scope instance*. Full detail in [the compiler reference](architecture/compiler.md).

## State store and indexes

The log is the truth; the state store is a fast, queryable materialization. Chrampfer uses an embedded, pure-Go LSM-tree store (Pebble — see [ADR-0003](adr/0003-pebble-as-state-store.md)). State is organized as several key-prefix "column families" that act as indexes:

```
el:<elementInstanceKey>          → ElementInstanceValue       (primary state)
elByProc:<procInstKey>:<elKey>   → nil                        (elements of an instance, for termination)
job:<jobKey>                     → JobValue
jobActivatable:<jobType>:<key>   → nil                        (open jobs per type, worker polling)
timer:<dueDate>:<timerKey>       → TimerValue                 (sorted by due date → range scan)
msgSub:<msgName>:<corrKey>       → SubscriptionValue
var:<scopeKey>:<name>            → bytes
```

The timer index is illustrative: because `dueDate` is the prefix, "which timers are due now" is a range scan from the start up to `now` — no full scan, no separate scheduler structure.

## Partitioning and scaling

A partition is a fully independent unit: its own command queue, single-writer processor, WAL, and state store. A process instance lives entirely within one partition. Routing is `instanceKey % N` (and the partition is encoded in every key).

Partitions do not share state and do not coordinate for normal execution, so throughput scales close to linearly with cores. Cross-partition concerns (message correlation across partitions, call activities spanning partitions) require explicit message passing and are handled as a deliberate, separate mechanism — see [ADR-0006](adr/0006-partition-routing-and-cross-partition.md) and the roadmap.

## Durability and recovery

Because state is a pure fold of the event log, recovery is straightforward:

1. Read the last applied log position from the state store.
2. Iterate the log forward from there.
3. Apply each *event* (commands are ignored) via the exact same `applyToState` used live.
4. Commit.

Determinism is preserved because generated keys and timestamps are written *into* the events; on replay they are read from the log, not regenerated. See [ADR-0005](adr/0005-group-commit-and-fsync-strategy.md) for the durability guarantees and the group-commit contract.

## Job workers and external tasks

The single writer must never block. Service tasks therefore never call external systems inline. Instead:

1. Activating a service task creates a **job** (a `JobCreated` event) and indexes it as activatable.
2. After the batch's fsync, a side effect notifies workers subscribed to that job type.
3. External workers stream/poll jobs, do the work, and submit a `CompleteJob` command.
4. That command flows back through the processor like any other, moving the element instance to `COMPLETING`.

This keeps the processor at full speed and makes external work naturally at-least-once, with idempotency handled via job keys. See [ADR-0007](adr/0007-job-worker-protocol.md).

## BPMN coverage

The target is full BPMN 2.0 execution semantics. Coverage is delivered in phases (see the [roadmap](../ROADMAP.md)):

- **Core:** start/end events, sequence flows, exclusive/parallel/inclusive gateways, service tasks
- **Events:** timer, message, signal, error; boundary events (interrupting and non-interrupting)
- **Structure:** embedded subprocesses, event subprocesses, call activities
- **Advanced:** multi-instance (sequential and parallel), compensation, transactions
- **Data:** input/output mappings, variable scoping with copy-on-write propagation

## Failure handling and incidents

When something cannot proceed — a job fails after exhausting retries, an expression cannot be evaluated, a variable is missing — Chrampfer does not crash the instance. It raises an **incident**: a first-class state entity that pauses the affected token and surfaces the problem for operator intervention. Resolving an incident produces a command that resumes execution. This keeps long-running processes robust against transient and operator-fixable failures.

## Observability

Because every state transition is an event in an ordered log, the log itself is the primary observability surface: a complete, replayable audit trail of everything that ever happened. On top of it Chrampfer exposes metrics (throughput, batch sizes, fsync latency, queue depth per partition), structured logs, and tracing hooks around command processing. The exported-log stream is also the integration point for downstream analytics.

## Component map

```
chrampfer/
├── compiler/      BPMN XML → CompiledProcess (parse, resolve, intern, expr, validate, linearize)
├── model/         Record, header, ValueType/Intent, payload encode/decode
├── engine/        Partition, processor loop, batching, ProcessingContext
├── behavior/      Per-BPMN-element behaviors (service task, gateways, events, subprocess)
├── state/         State store wrapper, transactions, indexes (column families)
├── wal/           Write-ahead log: segmented append, group commit, replay
├── expr/          FEEL expression compilation and evaluation
├── job/           Job store, worker subscription, gRPC streaming protocol
├── timer/         Due-date index scanning and timer triggering
└── api/           Client-facing command submission and queries
```

---

*See also: [Roadmap](../ROADMAP.md) · [ADRs](adr/) · [Invariants](architecture/invariants.md) · [Glossary](architecture/glossary.md) · [Contributing](../CONTRIBUTING.md) · [Agent guide](../AGENTS.md)*
