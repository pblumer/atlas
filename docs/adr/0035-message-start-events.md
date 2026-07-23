# ADR-0035: Message start events and the processInstanceKey built-in

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0020 gave Atlas message correlation between *running* instances: an
intermediate catch subscribes, an intermediate throw (or an API publish)
correlates. ADR-0023 made a collaboration deploy one process per executable pool.
Put together, a two-pool request/response — pool A sends, pool B receives, does
work, replies — is *almost* expressible, but the receiving pool has no way to
come into existence: its diagram begins with a **message start event**, and Atlas
compiled every `<startEvent>` to a none start event, ignoring the
`messageEventDefinition`. So the receiver never started, and the exchange
deadlocked at the sender's reply catch.

Two things were missing:

1. **Instantiation by message.** A deployed process whose entry point is a
   message start event must spring into a new instance when a correlating message
   is published or thrown — seeded with that message's payload.
2. **A correlation token for the reply.** For the reply to reach *the* instance
   that asked, both sides must share a value that uniquely identifies the
   requester and rides the round-trip. A business key (e.g. `orderId`) does this,
   but modelers also want the instance's own identity without inventing one.

## Decision drivers

- **Hold the invariants.** Instantiation-by-message must be a deterministic,
  side-effect-free `applyToState` mutation on recovery (I4); any generated key
  must be frozen into an event, never regenerated (I6); no per-command allocation
  on the hot path (I1).
- **One correlation mechanism.** A message start event should ride the same
  publish/throw path as ADR-0020, not a parallel one.
- **Operational simplicity.** Prefer not to add durable runtime state that needs
  its own recovery story if the same effect falls out of existing machinery.
- **A small, honest slice.** Match-by-name first; leave buffering, a start-event
  correlation key, and cross-partition correlation as stated follow-ups.

## Considered options

1. **Deploy-time start subscription in durable state (a new column family).**
   Register `(messageName, defKey)` in the state store at deploy; correlate scans
   it; recovery rebuilds it from the WAL.
2. **Deploy-time start subscription in an in-memory index, derived from the
   compiled definitions.** `Deploy` builds `messageName → []defKey`; correlate
   consults it; a created instance goes through the normal instance-activating
   command, so its events — and recovery — are identical to an API-created
   instance.
3. **A distinct "message start" instantiation command path** separate from the
   existing create-instance command.

For the reply token, orthogonally: **(a)** rely only on a modeled business key,
or **(b)** also expose the process instance key as a reserved FEEL identifier.

## Decision outcome

Chosen: **option 2** for instantiation, plus **(b)** the `processInstanceKey`
built-in.

A message start event compiles to a new `TypeMessageStartEvent` that is a normal
process entry point (it is in `StartEvents()` and behaves at runtime exactly like
a none start — it flows straight on) *and* is recorded in a per-definition
`messageStarts` table. `Processor.Deploy` folds those into an in-memory
`messageStarts: map[messageName][]defKey`; `Undeploy` drops them. `correlateMessage`,
after delivering to open subscriptions, also instantiates every definition whose
message start matches the name, by scheduling the **same** instance-activating
followup command an API create uses, carrying the message payload as start
variables.

The index is *derived from the compiled definitions*, not runtime state: the
deploy store re-registers every definition on restart (ADR-0019), so `Deploy`
rebuilds it for free. It is never consulted during replay — the instances it
created are reconstructed from the events they emitted, like any other instance.
So there is **no new durable state and no new recovery path**.

A message throw event now publishes the throwing instance's variables as the
message payload (previously it sent none), so a correlated catch — or a
message-start instance the throw creates — is seeded with them. `processInstanceKey`
is a reserved FEEL identifier bound in `bindInputs` to the evaluating instance's
own key, **as a string** so the full 64-bit key survives exactly (a FEEL number
is a float and would lose precision). A modeler stamps it into a variable
(`senderId = processInstanceKey`), throws it in the request payload, and
correlates the reply on it — the reply reaches exactly the requester that asked.

### Consequences

- **Positive:** two-pool request/response runs end to end. No new column family,
  no new recovery logic — the feature is compiler metadata + an in-memory index +
  reuse of the create-instance command. The correlation token can be a business
  key or, with the built-in, the instance's own identity.
- **Negative / trade-offs accepted:** message-start matching is by **name only**;
  the message's correlation key is not evaluated for start events yet. A throw now
  carries *all* the thrower's process variables (no output mapping); this is a
  reasonable v1 but coarser than Zeebe's mappings. `processInstanceKey` shadows a
  same-named process variable. A process with a message start event can also still
  be started by a plain API create (it then just flows on), which is permissive
  but matches Zeebe.
- **Follow-ups / risks to watch:** message **buffering** (ADR-0020) still governs
  timing — the requester must be subscribed at its reply catch before the responder
  throws; for this topology it always is (one hop vs several), but buffering is the
  real fix. A start-event correlation key (for de-duplication / keyed start),
  variable output mappings on throw, and cross-partition correlation remain open.

## Pros and cons of the options

### Option 1 (durable start subscription)
- Good: symmetric with instance subscriptions; visible in the state store.
- Bad: a whole new column family plus encode/decode, and a recovery story for
  config that is already reconstructable from the compiled definitions —
  duplicated source of truth.

### Option 2 (in-memory, deploy-derived index) — chosen
- Good: no durable state, no new recovery path, reuses the create-instance
  command so recovery of created instances is already covered.
- Bad: the index lives only in memory; correct only because `Deploy` runs on every
  start. (It does, by ADR-0019.)

### Option 3 (distinct instantiation path)
- Good: explicit.
- Bad: a second way to create an instance to keep in lockstep with the first —
  more surface, more replay risk, for no benefit.

## Links

- builds on ADR-0020 (message events and correlation)
- builds on ADR-0023 (collaborations and pools as multi-process deployments)
- relates to ADR-0019 (durable deployments — why the in-memory index recovers)
- honors the invariants in docs/architecture/invariants.md (I1, I4, I6)
