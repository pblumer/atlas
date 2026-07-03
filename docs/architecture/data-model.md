# Data Model

Everything that flows through Atlas is a **keyed record** with a `(ValueType, Intent)` discriminator. This document is the reference for records, keys, value types, intents, payloads, serialization, and the state-store index layout.

## Command vs. Event vs. State

Three concepts, often conflated:

- **Command** — an *intention*. May be rejected. Comes from outside or from the processor itself. **Not persisted.**
- **Event** — a *fact* that already happened. Never rejected. **Appended to the log.** The only thing persisted.
- **State** — the materialization (fold) of all events, living in the key-value store.

Flow: `Command → processor → Events → (state mutation + log append)`.

The invariant that makes recovery deterministic: **commands are processed, but only events are persisted.** On recovery, events are replayed (commands are not), because events have already frozen any non-deterministic effects.

## Record header

```go
type Intent     uint8
type ValueType  uint8
type RecordType uint8  // Command | Event | CommandRejection

type RecordHeader struct {
    Position    uint64    // monotonic log position (sequence number)
    SourcePos   uint64    // position of the record that caused this one (causality)
    Key         uint64    // entity key
    Timestamp   int64     // unix nano
    RecordType  RecordType
    ValueType   ValueType
    Intent      Intent
    PartitionId uint16
}
```

`SourcePos` builds a causal chain through the log: every record points at the record that produced it. Invaluable for debugging and for reconstructing why something happened.

## Keys

Every stateful entity (process instance, element instance, job, timer) gets a globally unique 64-bit key, with the partition baked in so routing is a bit-shift:

```go
// [16 bit partition][48 bit monotonic counter]
func newKey(partition uint16, counter uint64) uint64 {
    return uint64(partition)<<48 | (counter & 0xFFFFFFFFFFFF)
}
func partitionOf(key uint64) uint16 { return uint16(key >> 48) }
```

Keys are unique across partitions, and a key alone tells you which partition owns the entity.

## Value types

For full BPMN coverage:

```go
const (
    VTProcessInstance     ValueType = iota // the running instance as a whole
    VTElementInstance                      // a single active BPMN element instance (token carrier)
    VTJob                                  // service-task work for external workers
    VTTimer                                // timer-event subscription
    VTMessageSubscription                  // waiting for a message (receive task, message event)
    VTMessage                              // an incoming message (buffered)
    VTVariable                             // a process variable
    VTIncident                             // a fault state (job failed, expression error)
    VTSignal
    VTError                                // error-event propagation
    VTProcessDefinition                    // a deployed definition
)
```

## Intents

Grouped by area. The element-instance lifecycle is the heart of the model:

```go
const (
    // ElementInstance lifecycle (every BPMN element goes through this)
    IntentActivating Intent = iota
    IntentActivated
    IntentCompleting
    IntentCompleted
    IntentTerminating
    IntentTerminated

    // SequenceFlow
    IntentSequenceFlowTaken

    // Job
    IntentJobCreated
    IntentJobActivated   // picked up by a worker
    IntentJobCompleted
    IntentJobFailed
    IntentJobTimedOut

    // Timer
    IntentTimerCreated
    IntentTimerTriggered

    // Message / Signal
    IntentSubscriptionCreated
    IntentSubscriptionCorrelated
    IntentMessagePublished

    // Variable
    IntentVariableCreated
    IntentVariableUpdated

    // Incident
    IntentIncidentCreated
    IntentIncidentResolved
)
```

The lifecycle `Activating → Activated → Completing → Completed` (plus `Terminating → Terminated`) is what makes subprocesses, boundary events, and I/O mappings clean. A subprocess is `Activated` while its children run and only `Completing` once the last child is `Completed`.

## Payloads

The header says *what*; the payload says the details. One compact struct per value type. Examples:

```go
type ElementInstanceValue struct {
    ProcessInstanceKey uint64
    ProcessDefKey      uint64
    ElementId          int32   // INDEX into the compiled graph, not a string
    FlowScopeKey       uint64  // parent scope (subprocess instance), 0 = root
    BpmnElementType    uint8   // for fast dispatch
}

type JobValue struct {
    ProcessInstanceKey uint64
    ElementInstanceKey uint64
    JobType            int32   // interned string → index
    Retries            int32
    Deadline           int64
    // variables live in variable state, not copied here
}

type TimerValue struct {
    ProcessInstanceKey uint64
    ElementInstanceKey uint64
    TargetElementId    int32
    DueDate            int64
    Repetitions        int32   // -1 = infinite (timer cycle)
}
```

**Reference, don't copy.** Variables live in their own variable state, referenced by scope key — never embedded in a job record. Otherwise the log bloats and the same data is written many times over.

## Serialization

The log is written millions of times per second, so the encoding is directly throughput-relevant. JSON is disqualified (reflection, allocations, parsing). Options, pragmatic to extreme:

1. **Protobuf** — pragmatic, schema-versioned, good Go tooling. Not used on the hottest paths.
2. **Hand-written binary encoding** — write directly into a pre-allocated buffer, no reflection. Maximum throughput; you manage schema evolution yourself (version byte in the header).
3. **Zero-copy / FlatBuffers-style** — the record stays in the buffer and is never deserialized; fields are read by offset directly from the byte slice. This is the SBE-style approach behind the most extreme engines.

**Chosen starting point:** hand-written binary encoding with a clear version byte. Much faster than Protobuf, still debuggable. The zero-copy step is deferred until profiling proves (de)serialization is the hotspot — before that it is premature optimization. See [ADR-0009](../adr/0009-record-serialization-format.md).

## State-store index layout

The log is the truth; the state store is a fast, queryable materialization. State is organized as key-prefix "column families" that act as indexes:

```
el:<elementInstanceKey>          → ElementInstanceValue       (primary state)
elByProc:<procInstKey>:<elKey>   → nil                        (elements of an instance, for termination)
job:<jobKey>                     → JobValue
jobActivatable:<jobType>:<key>   → nil                        (open jobs per type, worker polling)
timer:<dueDate>:<timerKey>       → TimerValue                 (sorted by due date → range scan)
msgSub:<msgName>:<corrKey>       → SubscriptionValue
var:<scopeKey>:<name>            → bytes
incident:<incidentKey>           → IncidentValue
```

The timer index shows the pattern: because `dueDate` is the prefix, "which timers are due now" is a range scan from the start up to `now` — no full scan, no separate scheduler structure. The `jobActivatable` index lets a worker find open jobs of a type with a prefix scan.

## Why this model holds together

Every transition is a keyed record with a `(ValueType, Intent)` discriminator; strings are compiled to indices; state is a fold of the event log over secondary indexes. The same `applyToState` runs live and on recovery, so the log and the state can never drift apart.
