# ADR-0472: Atlas keeps a log of the signals and messages that were sent

- **Status:** Accepted (2026-10-22)
- **Implementation:** Landed
- **Date:** 2026-10-22
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0447](0447-the-signals-deployed-models-throw-and-receive.md) and
[ADR-0446](0446-the-messages-deployed-models-send-and-receive.md) show what the deployed models
*could* exchange: who throws a name, who receives it, where it crosses a project. They read the
definitions and say so. They cannot say what *did* happen, and ADR-0447 §7 left that open as "a
record of its own": how often a name was thrown, when last, and whom it reached.

What a run leaves behind today answers part of it, unevenly:

- **A delivered message** leaves a `VTMessageFlow` record with its sender and receiver
  ([ADR-0038](0038-collaboration-message-flow-replay.md)). It is kept per receiving definition, for the
  collaboration replay of one definition, and never removed.
- **A message nothing received** leaves nothing, unless it was published through
  `POST /api/v1/messages` with a `timeToLiveMs` and so kept in the buffer
  ([ADR-0370](0370-durable-message-buffer.md)). A throw, a watch and a product action publish
  without one.
- **A signal throw** leaves no record of its own. Each receiving subscription is retired with
  `SubscriptionCorrelated`, which names neither the sender nor the broadcast. A throw that reached
  nobody leaves only the throw element's step in its own instance's history.
- **A correlation that cleared a stale subscription**, whose element no longer waits, looks the
  same on the log as a delivery. The engine retires such a subscription lazily, when the next
  matching send arrives (`deliverToSubscriptions`, `broadcastSignal`).

So the question "what just happened between my processes" has no answer outside one instance's
timeline. A modeler whose receiver never moved cannot tell whether nothing was thrown, the name
differed, or the throw arrived before the receiver waited.

The question this record answers: **how Atlas records, durably and in order, every signal and
message that was sent and every place it arrived, so that an operator and a project's members can
read what happened, without storing what the sending instance held and without slowing the
engine more than the fact costs.**

## Decision drivers

- **Durable before visible, replay identical** (invariants I2, I4, I6). A row is folded from a
  record on the log, by `applyToState`, from the record alone, live and on recovery alike.
- **No allocation per command on the hot path** (I1). A throw is token movement.
- **No payload.** A signal or message carries every variable of the sending instance. The log
  records names, keys and moments, never values.
- **Bounded.** Every send adds a row; the table must not grow for ever.
- **Nobody reads more than they already may** ([ADR-0275](0275-instance-visibility.md)).
- **One binary.** No external store is required to answer the question.

## Considered options

1. **Structured log lines** for every send and delivery, read in Console → Logs.
2. **Scan the write-ahead log** on request for the records that already exist.
3. **A dispatch fact, and a log table folded from it and the existing records**, with a
   retention of its own.
4. **Counters per name** on `/metrics`.
5. **Export the records to OpenSearch** (the existing exporter) and read them there.

## Decision outcome

Chosen: **option 3.**

### 1. The dispatch fact

A new value type, `VTDispatch`, with the event intent `IntentDispatched`, records that a signal
was broadcast or a message published. One record per send, written on the command path after the
deliveries, so it carries how far the send reached:

- the channel (signal or message), the name and, for a message, the correlation key;
- the source: a process (a throw, an end event, a send task), a publish (the route, or atlas's own
  order and fulfilment publishes), an inbound watch, or a directed delivery to one instance
  ([ADR-0428](0428-a-product-lifecycle-may-run-as-one-instance-per-position.md));
- for a process, the sending instance, its definition and the element; for a directed delivery,
  the target instance and its definition;
- `Reached`, the subscriptions whose element still waited and received it, and `Started`, the
  instances it started.

A send that reached nobody is written like any other, with both counts zero. That is the case the
log exists for.

It is written in `broadcastSignal`, in `correlateMessage`, and in `handleDelivering` for a
directed delivery that is not a retry. A buffered message's later delivery is not a new send: it
is a row of its own (§3).

### 2. The receptions say who sent them, and whether they were deliveries

The receptions are on the log already. Two fields are appended to a signal's, so a record
written before decodes them as zero and a version before this one ignores them:

- `SignalSubscriptionValue.SenderProcessInstanceKey`: the throwing instance, set on
  `SubscriptionCorrelated`.
- `SignalSubscriptionValue.Stale`: the subscription's element no longer waited, so the correlation
  only cleared it. A stale correlation is not a reception, is not counted in `Reached`, and has no
  row.

A message's stale correlation needs no mark: it leaves no `VTMessageFlow` record at all. Building
the log showed that a stale correlation was treated as a delivery in two more places — it wrote
the send's payload into an instance it never reached, and it counted as delivered, so a message
with a time to live that only a left-behind subscription matched was lost instead of kept. Both
are corrected with this record, for signals and messages alike, and recorded as an amendment of
[ADR-0370](0370-durable-message-buffer.md) (2026-10-23).

A signal that starts an instance was counted and nothing more. A new intent on `VTSignal`,
`IntentSignalStarted`, records each start a broadcast causes: the definition and its start
element, the signal and the sender. It changes no subscription; it is a row only. A message start
already has its `VTMessageFlow` record.

### 3. The table

`applyToState` folds one row per record into a table keyed by partition and log position, as the
event feed is ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5):

| Row | Folded from |
|---|---|
| `signal.thrown`, `message.sent` | `VTDispatch` / `IntentDispatched` |
| `signal.received` | `VTSignal` / `SubscriptionCorrelated`, not stale |
| `signal.started` | `VTSignal` / `IntentSignalStarted` |
| `message.received`, `message.started` | `VTMessageFlow` / `MessagePublished`; a receiving instance of zero is a start |
| `message.kept` | `VTMessage` / `IntentMessageBuffered` |
| `message.taken` | `VTMessage` / `IntentMessageConsumed` |
| `message.expired` | `VTMessage` / `IntentMessageExpired`, when the message still waited |

A row holds its kind, the record's timestamp, the record's source position and the value without
any payload. **The source position is the cause:** a send and the receptions it caused are written
while one command is processed, so they share it, and a reader groups a reception under its send
without the fold keeping anything between records.

The correlation key is kept, because "which order" is the question an operator asks first. It is
data the instance held. The table keeps it for the retention below, where `VTMessageFlow` keeps it
for ever.

### 4. Retention

Rows are kept for `--event-log-ttl`, 30 days by default (`ATLAS_EVENT_LOG_TTL`). The retention
sweep, once an hour, finds the last row recorded before the cutoff and enqueues a prune through
its position. The prune is a fact: `VTFeedRetention` gains `IntentEventLogPruning` and
`IntentEventLogPruned`, and `applyToState` drops the rows and remembers the cut, as it does for the
feed. A reader whose cursor lies below the cut is told so.

### 5. Who may read it

A row is read as an instance is read (ADR-0275):

- Authentication off, an operator or an administrator: every row.
- A member of a project: the rows whose definition, the sender's for a send and the receiver's for
  a reception, was deployed from a project they may view.
- A row that names no definition, a publish through the route, a watch or a buffered message: an
  operator or an administrator only.

The route is open to every signed-in caller and filters each row, so a caller who may read
nothing reads an empty log. A reception shows the sending instance's key even when the sender is
in a project the reader cannot see. A key is a reference, not data; opening it answers 404.

### 6. Surfaces

- `GET /api/v1/event-log`: newest first, `before` a cursor for older rows and `after` one for newer
  rows, `limit` up to 500, and filters `channel`, `name` and `instance`. A page reads at most
  10 000 rows, so a narrow filter answers a short page with `more` rather than walking the table.
- The Console: *Operations → Diagnostics → Event log*, a list that refreshes itself, with the
  receptions under their send, filters, and links to the instances. The *Events* page links to it,
  and a name there opens the log filtered by that name.
- MCP: `atlas_event_log`.

### 7. What this record does not decide

- **Counters per name** (option 4). The table answers how often within its window. A metric
  labelled by name has unbounded cardinality.
- **A payload in the log.** Never, unless declared payloads (ADR-0435 §10) make one safe to keep.
- **Erasure of a correlation key before its retention ends.** The key lives as long as the row.
- **Cross-partition sends** ([ADR-0006](0006-partition-routing-and-cross-partition.md)). Each
  partition logs what it processes; a reader reads the partition the server runs.

### Compatibility

- A data directory written before this version is read as it is. The table starts empty at the
  upgrade; records replayed after the last checkpoint fold their rows like any other, which is
  deterministic.
- A version before this one decodes `VTDispatch` to no value and skips it, ignores the new
  intents, and stops reading the appended fields where they begin. Going back loses the log and
  nothing else.

### Consequences

- **Positive:**
  - "Was it thrown, and where did it arrive" has an answer for every send, including a throw that
    reached nobody and a message that was lost.
  - A stale correlation no longer reads as a delivery: not in the log, not in an instance's
    variables, not in the buffer's decision, not in the collaboration replay.
  - The same project rule governs the log as governs the instances it describes.
- **Negative / trade-offs accepted:**
  - One more event per send, one more row per send and per reception. The cost is per send, not
    per instance, and the retention bounds the table.
  - The correlation key is kept for the retention, though it may be personal.
  - The log covers sends from the upgrade on.
- **Follow-ups / risks to watch:**
  - Counters per name, if the window is not enough.

## Pros and cons of the options

### Option 1: structured log lines
- Good: no change to the engine.
- Bad: a log line is not durable before visible, is not filtered by project, rotates out by size
  rather than by time, and still has no record of a stale correlation or of a throw's reach.

### Option 2: scan the write-ahead log
- Good: no new record.
- Bad: the log is truncated at checkpoints, so the window is whatever compaction left. A throw's
  reach and a lost message are still on no record.

### Option 3: a dispatch fact and a folded table (chosen)
- Good: durable, ordered, replayed identically; complete for sends from the upgrade on; bounded.
- Bad: an engine change, a new value type, and the cost above.

### Option 4: counters per name
- Good: cheap to read.
- Bad: unbounded label cardinality, and a count says nothing about where a send arrived.

### Option 5: OpenSearch
- Good: search and retention are the store's.
- Bad: needs a store beside the binary, and the records it would ship still lack a throw's reach.

## Links

- [ADR-0447](0447-the-signals-deployed-models-throw-and-receive.md) §7 and
  [ADR-0446](0446-the-messages-deployed-models-send-and-receive.md) §6: the runtime half they left
  open.
- [ADR-0038](0038-collaboration-message-flow-replay.md): the message-flow history this reuses.
- [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5: the feed table and
  its retention, the pattern this follows.
- [ADR-0370](0370-durable-message-buffer.md): the buffer whose facts become rows.
- [ADR-0275](0275-instance-visibility.md): who may read an instance, and so a row.
- [ADR-0088](0088-signal-events.md): signal broadcast semantics.
