# ADR-0026: A clio connector — server-registered event-store integration

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas engine team

## Context and problem statement

Atlas runs business processes; [clio](https://clio.blumer.cloud) is an
append-only, schema-registered **event store** (hierarchical subjects, registered
event types/schemas, reduce-spec projections, `write_events` / `run_query` /
`get_state`). Users want Atlas to talk to a clio instance — for two genuinely
different reasons that are easy to conflate:

1. **A process step that acts on clio** — append a domain event ("order
   received") or read projected state to branch on. This is *modeled* behaviour:
   the model says, at a specific point, "write this to clio" or "ask clio."
2. **Observing Atlas execution in clio** — mirror Atlas's own durable event
   stream (its WAL records) into clio as an audit/analytics feed. This is
   *infrastructure*: no model mentions it; every process is covered automatically.

Both are **outbound side effects to a remote service**, so both are governed by
the same hard constraints, and both must answer the same awkward question
(append-only + at-least-once = duplicates). But they are not the same feature and
should not be built as one. This ADR compares them and decides what to build
first.

A second, cross-cutting requirement the user stated: the clio endpoint must be
**registered at the server**, not baked into models. Ops configures which clio
instance (e.g. `clio.blumer.cloud`) and the credentials; a model refers to a
connector by name, never by URL or secret.

## Decision drivers

- **Invariants are non-negotiable.** A remote call is a side effect: it must run
  only in the post-fsync side-effect phase (I2 / ADR-0005), never inside
  `applyToState`, which is replayed on recovery and must stay deterministic and
  side-effect-free (I4). It must never run on the single-writer processor
  goroutine (must not block the partition; ADR-0007), and must not allocate on
  the hot path (I1).
- **Reuse the proven seam.** Atlas already integrates an external engine — temis
  for DMN — through the job/worker path (ADR-0014) with full crash-recovery
  semantics. A connector should reuse that, not invent a parallel mechanism.
- **At-least-once is the reality.** Jobs (and any post-fsync forwarder) are
  at-least-once: a crash between "clio accepted the write" and "Atlas recorded
  that it did" replays the write. Writing to an **append-only** store makes
  duplicates visible, so idempotency is a first-class design concern, not an
  afterthought.
- **Secrets and endpoints belong to ops, not to models.** A BPMN file is shared,
  versioned, and rendered; it must not carry a URL or a token.
- **Opt-in vs. blanket.** A modeled step is opt-in and precise; a mirror is
  blanket and automatic. Different blast radius, different governance.

## Considered options

**For the integration shape:**

1. **Variant A — clio connector *task* (via the job/worker path).** A BPMN task
   (`zeebe:taskDefinition type="clio:write-events"` or `clio:query`) delegates to
   clio exactly as a business rule task delegates to temis: at activation the
   engine creates a job; an in-process clio worker, off the processor goroutine
   and after fsync, calls clio and completes the job, mapping results back to
   process variables.
2. **Variant B — clio event *mirror* (a post-fsync outbox).** A background
   forwarder tails Atlas's durable WAL from a persisted cursor and appends each
   committed record to clio (subject `atlas/<partition>/<processInstanceKey>`,
   event type = the record's `(ValueType, Intent)`), advancing the cursor only
   after clio acknowledges. No model changes; every process is mirrored.
3. **Variant C — call clio inline from a behavior.** Rejected on sight: it runs a
   network call on the single writer and tempts a call from inside `applyToState`
   — violates I1, I4, and ADR-0007. Listed only to be explicit.

**For endpoint configuration (applies to A and B):**

- A **server-side connector registry**: named connectors (`{name, kind, endpoint,
  credentialsRef}`) configured at the server; a model/task references a connector
  by **name**. The worker/forwarder resolves the name to a concrete clio endpoint
  and secret at runtime.

## Decision outcome

Chosen: **Variant A (the connector task via the job path) first, behind a
server-side connector registry.** **Variant B (the event mirror) is accepted as
a complementary future direction, not built now.** Variant C is rejected.

Rationale:

- Variant A is what "a connector registered at the server" most directly means: a
  process explicitly does something with clio, opt-in per model, endpoint and
  secret supplied by ops. It reuses the ADR-0014 pattern almost verbatim — a new
  `clio` package with a `job.Runner` handler on reserved job types
  (`io.atlas.clio.write`, `io.atlas.clio.query`) — so it inherits the job
  lifecycle **including crash recovery** with essentially no engine change: no new
  value type, no `applyToState` change, no processor change. The clio dependency
  lives only in the new package, never in `engine`.
- Variant B is valuable (turnkey audit/analytics of every process) but is a
  heavier, always-on subsystem: a durable forward cursor, backpressure when clio
  is slow or down (without ever stalling the partition), and its own recovery
  story. It is an outbox/CDC feature in its own right and is cleanly additive
  later — it does not block, and is not blocked by, Variant A.

Both variants share one mandatory rule, dictated by at-least-once semantics over
an append-only store: **every clio write carries a deterministic idempotency
key** so a replayed write is de-duplicated by clio rather than doubling an event.
For Variant A the key is derived from the job key (which is frozen into the job
event, so it is stable across replay, I6); for Variant B it is the WAL position
(globally unique and monotonic). Neither may derive the key from wall-clock or
regenerated state.

The **connector registry** is the shared substrate: `{name → {kind, endpoint,
credentialsRef}}` loaded from server configuration (credentials by reference to
an env var / secret store, never inline). A `clio:write-events` task's compiled
detail records the connector **name**, the target **subject**, the **event type**,
and a **payload mapping** (which process variables form the event body) — all
model-authored, deploy-time data (I5). The worker resolves the name to an
endpoint+secret at call time. This keeps secrets out of models and lets ops point
Atlas at `clio.blumer.cloud` (or a test instance) without touching a single BPMN
file.

### Consequences

- **Positive:** processes can append domain events to and read projections from
  clio at modeled points, with recovery and non-blocking execution inherited from
  the job protocol and zero hot-path or `applyToState` impact. Endpoints and
  secrets are centrally managed; models stay portable. The idempotency-key rule
  makes at-least-once safe against an append-only store. Variant B remains open as
  a purely additive audit feed.
- **Negative / trade-offs accepted:** a clio write is at-least-once (mitigated by
  the idempotency key, which requires clio to honor it — a dependency on clio's
  API). A connector adds an external runtime dependency to the deployment (a
  process that reaches a `clio:write-events` task parks until the worker and clio
  are reachable — same failure mode as any service task). The registry is new
  server surface (config parsing, secret handling, health). Payload/result
  mappings depend on the Milestone-1 variable subsystem, exactly like the DMN
  task's input/output mappings (ADR-0014).
- **Follow-ups / risks to watch:** build Variant B (the WAL→clio outbox with a
  durable cursor and backpressure) once the connector framework and variable
  mappings exist; register clio event **schemas** at deploy so writes are
  validated (clio supports schema registration); define retry/incident policy for
  a persistently unreachable clio; decide whether `clio:query` reads
  `get_state`/`run_query` synchronously in the worker (simplest) or via a
  subscription; pin the clio API version.

## Pros and cons of the options

### Variant A — connector task via the job path (chosen first)
- Good: reuses ADR-0014/0007 wholesale (recovery, non-blocking, dependency
  isolation); opt-in and precise; endpoint/secret via the registry; near-zero
  engine change.
- Bad: at-least-once needs an idempotency key honored by clio; only covers points
  a model explicitly marks; parks the token if clio is down.

### Variant B — event mirror / outbox (accepted, deferred)
- Good: turnkey, blanket audit/analytics of every process with no model changes;
  Atlas is already event-sourced, so the mapping to clio events is natural.
- Bad: a new always-on subsystem — durable forward cursor, backpressure, its own
  recovery; blanket (governance/volume concerns); still at-least-once (keyed by
  WAL position). Heavier than Variant A for the first slice.

### Variant C — inline call from a behavior (rejected)
- Good: no job round-trip.
- Bad: network I/O on the single writer; invites a call inside `applyToState`;
  violates I1, I4, ADR-0007. Not viable.

## Links

- mirrors ADR-0014 (DMN business rule tasks via temis) and ADR-0007 (job worker
  protocol) — the connector-via-job pattern
- depends on ADR-0005 / I2 (side effects only after fsync) and honors I1, I4, I6
- relates to ADR-0019 (durable deployments) for where connector-referencing
  definitions live; Variant B relates to the WAL design (ADR-0005) as its source
- unblocked by the Milestone-1 variable subsystem for payload/result mappings
