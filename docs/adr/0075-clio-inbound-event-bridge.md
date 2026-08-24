# ADR-0075: A clio inbound event bridge — at-least-once ingestion with engine-side idempotent delivery

- **Status:** Accepted
- **Date:** 2026-07-28
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0036 gave Atlas an *outbound* clio connector: a modeled task appends, queries, or
reads a server-registered [clio](https://clio.blumer.cloud) event store through the
job path. The complementary direction — **letting a clio event drive Atlas** — was
left open. An operator wants an event on a watched clio subject to (1) **start** new
process instances and (2) **wake** instances waiting at a message catch. Atlas already
has the machinery for both: message correlation (ADR-0020), where one published
message both starts every matching message-start process (ADR-0035) and correlates
every waiting subscription. So the missing piece is a component that consumes clio
events and republishes each as an Atlas message.

The hard part is delivery semantics. Any tail-and-forward consumer is **at-least-once**:
a crash between "read the clio event" and "durably record that we processed it" replays
the event. For a message **catch**, a replay is harmless — the first delivery retired
the subscription, so the duplicate matches nothing. For a message **start**, a replay is
*not* harmless: `correlateMessage` unconditionally starts one instance per matching
start event, so a replayed publish **double-starts** a process. Because the thing being
deduplicated (a created instance) lives in engine state, a dedup authority outside the
engine (a sidecar cursor file) cannot gate instance creation atomically with it.

## Decision drivers

- **Invariants.** The clio read is a network call: it must run off the processor
  goroutine (I3), never inside `applyToState` (I4). The publish must be durable before
  it is acted on (I2), and replay must be deterministic (I6).
- **Reuse correlation.** A clio event should funnel into the *existing* `correlateMessage`
  path, not a parallel start/wake mechanism.
- **Correctness under at-least-once.** A replay must not double-start a process.
- **Layering.** The engine must not learn about clio; ADR-0036 kept the clio dependency
  in its own package, and the inbound path should keep that boundary.

## Considered options

1. **Sidecar cursor only.** The bridge persists a per-subscription cursor and advances
   it after each durable publish. Correct for catch (natural idempotency), but the
   crash window can double-start message-start processes. No engine change.
2. **A clio-specific durable cursor value type in the WAL.** Fold the cursor advance
   into the publish batch as a `VTClioCursor` record. Atomic and correct, but embeds a
   connector-specific concept into `model`/`state`/recovery — breaks the layering.
3. **A generic engine-side idempotent-delivery mark + best-effort sidecar cursor.** The
   publish command carries an opaque `SourceID` and monotonic `SourceSeq`; the engine
   folds a per-source high-water mark into the *same batch* as the correlate/start
   effects, and skips a publish whose sequence is not past the mark. The bridge also
   keeps a sidecar cursor purely to resume near the tip.

## Decision outcome

Chosen: **option 3.** A new generic value type `VTInboundDelivery`
(`InboundDeliveryValue{SourceID, SourceSeq}`) and intent `IntentInboundDeliveryApplied`
record a per-source high-water mark. `Processor.PublishInbound(sourceID, seq, name,
key, vars…)` enqueues the same `IntentMessagePublished` command the API/throw path uses,
plus the source id/seq. `handleMessagePublished` guards: if
`InboundHighWater(sourceID) >= seq` it skips (a replay); otherwise it correlates **and**
appends one `IntentInboundDeliveryApplied` event that `applyToState` folds into the
high-water mark. Because the correlate/start events and the mark ride one batch, a
single fsync commits them atomically (I2); recovery replays them and rebuilds the mark
(I4/I6). Ingestion is therefore **effectively-once into processes** even though the
bridge is at-least-once. The mark is deliberately generic — the engine never interprets
`SourceID` — so clio is never named in the engine, preserving ADR-0036's layering.

The bridge itself (`api/inboundBridge`) mirrors the timer scheduler: a ticker goroutine
that reads new clio events **off** the run loop (`clio.Client.ReadEvents` → clio's
`POST /api/v1/read-events`, NDJSON oldest-first), computes each event's correlation key
and payload off the loop, and hands one publish batch per subscription back onto the run
loop via `s.do`. The publish is made durable (`Drive` → fsync) *before* a **best-effort**
sidecar cursor advances. The cursor only speeds a restart's resume; it is explicitly
**not** the correctness authority. If it is lost or stale, the bridge re-reads and the
engine high-water mark drops the duplicates. `SourceID = "clio:" + connectorID + ":" +
subject`.

**`SourceSeq` is the clio event `id`.** A clio event carries no separate sequence field;
its `id` *is* a per-partition monotonic counter rendered as a decimal string
(`strconv.FormatUint`), and clio's own read cursor (`lowerBound`) is an inclusive `id`
bound. The bridge parses the `id` to the `uint64` the engine deduplicates on, so the mark
advances in clio's own event order. This assumes a single-partition clio
(`CLIO_PARTITIONS=1`, clio's recommended production setting): clio documents its scalar
`lowerBound` cursor as well-defined only for `N=1`, and the same restriction is what makes
one global `id` order — and thus one high-water per source — correct here.

**Correlation keys and seeded variables see the event's subject, not just its body.**
`correlationKeyOf` evaluates the FEEL key over the event body **plus** four reserved
envelope fields — `subject`, `subjectTail` (the last `/`-segment, e.g. `E-123456` for
`/employees/E-123456`), `eventType`, `eventId` — and `eventVars` seeds those same fields
as variables on the started/woken instance. This is what lets a watch on a parent subject
derive a per-entity correlation key (`= subjectTail`) from the child subject an event was
written to, the motivating use case. Envelope fields take precedence over a body field of
the same name so a subscription can always rely on them.

Subscriptions are operator-managed like connectors: an `inboundSubStore` sidecar holds
`{connectorId, watchedSubject, recursive, messageName, correlationKey, enabled}` records,
CRUD'd under `/api/v1/connectors/{id}/inbound-subscriptions` and editable in the Console.
`recursive` reads the watched subject's whole subtree (clio's `recursive` read flag), so a
watch on `/employees` catches an event on `/employees/E-123456`. A bad correlation-key
FEEL expression is rejected at config time, not left to fail every poll.

### Consequences

- **Positive:** a clio event both starts and wakes processes through the one existing
  correlation path; a crash never double-starts; the engine stays clio-agnostic; the
  bridge never blocks the partition (network I/O is off-loop). Endpoints/tokens stay in
  the managed connector store (ADR-0041), resolved from the vault.
- **Negative / trade-offs accepted:** a new durable value type and a background poller
  are new surface. Polling has latency (default 2s) versus a push subscription. The
  high-water table grows one entry per source (bounded by configured subscriptions).
- **Follow-ups / risks to watch:** a push/streaming clio subscription instead of polling;
  bounding/rotating the high-water table if sources ever become unbounded; **multi-partition
  clio** (`CLIO_PARTITIONS>1`) would break the single global `id` order the dedup relies on
  — supporting it means keying the high-water per `(source, partition)` and is deferred until
  clio itself makes its scalar cursor well-defined for `N>1`. The wire format now targets the
  clio v1 API (`/api/v1/read-events`, `/api/v1/write-events`, `/api/v1/run-query`,
  `/api/v1/state/<subject>`); it is isolated in `clio.HTTPClient`.

## Pros and cons of the options

### Option 1 — sidecar cursor only
- Good: no engine change; correct for catch via natural idempotency.
- Bad: double-starts message-start processes in the crash window — a real duplicate
  instance. Atlas is the downstream here, so it must own the dedup: unlike a message
  *catch* (naturally idempotent once the subscription is retired), a *start* has no such
  guard.

### Option 2 — clio-specific WAL cursor
- Good: atomic and correct.
- Bad: couples the core engine (`model`/`state`/recovery) to a connector concept, breaking
  the layering ADR-0036 established; the generic mark gets the same atomicity without it.

### Option 3 — generic high-water + best-effort cursor (chosen)
- Good: effectively-once into processes, atomic with the effects it guards, engine stays
  clio-agnostic, cheap sidecar resume.
- Bad: one extra durable event per fresh delivery; a per-source table to keep.

## Links

- inbound counterpart to ADR-0036 (the clio connector task); reuses ADR-0020 (message
  correlation) and ADR-0035 (message start), and the ADR-0007 run-loop/`do` discipline
- honors I2 (durable before visible), I3 (no network on the processor), I4/I6
  (deterministic replay); depends on ADR-0041 (managed connectors + vault) for the
  clio endpoint/token
