# ADR-0019: Message events and correlation

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas engine maintainers

## Context and problem statement

Atlas can already run a process to a service task, a script task, an exclusive
gateway, and an intermediate timer catch event. The one thing missing to make
*two* instances interact is messages: an instance that waits for a named message
carrying a matching key, and something — another instance or an external system —
that publishes it. This is the executable form of the BPMN *message flow*
between participants: an order-fulfillment instance waits for a "payment
received" message correlated on the order id, and the payment process (or an API
caller) publishes it.

The model already reserves the vocabulary for this — `VTMessageSubscription`,
`VTMessage`, and the intents `SubscriptionCreated`, `SubscriptionCorrelated`,
`MessagePublished` all exist — but there is no payload struct, no state index, no
compiler support, and no behavior. We need the first executable vertical slice.

## Decision drivers

- **Invariants hold.** Correlation must be a deterministic, side-effect-free
  `applyToState` mutation from the event alone (I4); the FEEL correlation key is
  evaluated at command time and frozen into the event (I6), never re-evaluated on
  replay; the compiled graph stays immutable and integer-indexed (I5).
- **One correlation mechanism for both producers.** An intermediate message
  *throw* event and an external API publish must correlate through the exact same
  path, so their semantics can't drift.
- **Reuse the existing machinery.** A new column family and value type, the
  existing `expr` boundary for the key expression, the existing variable events
  for the payload — no parallel subsystem.
- **A small, honest slice.** Single partition, and no message buffering yet: a
  message correlates only to subscriptions that already exist when it is
  published. Buffering (a published message waiting for a future subscriber),
  message start events, and cross-partition correlation are explicit follow-ups.

## Considered options

1. **Symmetric key expression on a shared `<message>` definition.** Both the
   catch and the throw reference the same `<bpmn:message name="…">` whose
   `zeebe:subscription correlationKey="= expr"` FEEL expression each side
   evaluates over *its own* variables. Publish = correlate to matching open
   subscriptions.
2. **Buffer every published message** in a `VTMessage` index and match lazily,
   so publish order and subscribe order don't matter.
3. **Correlate through a broker/queue abstraction** decoupled from the engine
   log.

## Decision outcome

Chosen option: **"Symmetric key expression on a shared message definition"**,
without buffering.

- A `MessageSubscriptionValue{ProcessInstanceKey, ElementInstanceKey,
  MessageName, CorrelationKey}` is stored in a new column family
  `cfMessageSubscription = 0x0A`, keyed by length-prefixed
  `(MessageName, CorrelationKey)` followed by the element-instance key. That
  makes "which subscriptions match this (name, key)?" a single prefix scan, and
  the trailing element key disambiguates several instances waiting on the same
  key.
- A **message intermediate catch event**, on activation, evaluates its compiled
  correlation-key expression over the waiting instance's variables, emits
  `SubscriptionCreated`, and waits (stays `Activated`) — exactly the shape of the
  timer catch event, which waits for a due date instead of a message.
- A **message intermediate throw event**, on activation, evaluates the *same*
  message's key expression over its own variables and correlates, then completes
  and takes its outgoing flows.
- **Correlation** (shared by the throw event and the API publish) scans the open
  subscriptions matching `(name, key)`, and for each emits
  `SubscriptionCorrelated` (which deletes the subscription), writes any message
  payload variables into that instance's scope, and commands its waiting element
  instance to complete. A publish that matches nothing is a no-op (no buffering).
- The HTTP API gains `POST /api/v1/messages` with
  `{"name","correlationKey","variables"}`, so an operator or an external system
  can publish a message; it correlates through the identical engine path.

The correlation key is compared as its FEEL canonical string (`Value.String()`),
so a number variable `orderId = 42` on both sides, and an API caller passing
`"42"`, all correlate.

### Consequences

- **Positive:** Two instances can now rendezvous. One correlation path serves
  both the throw event and the API. Correlation is a pure, replayable state
  mutation; recovery reproduces it from the frozen events. Message payload rides
  the existing variable events, so it lands in the target scope like any other
  variable.
- **Negative / trade-offs accepted:** No buffering — a message published before
  its subscriber exists is lost (a race the demo avoids by subscribing first).
  Single partition only: correlation scans the local store, so cross-partition
  correlation (ADR-0006) is future work. A message correlates to *every* matching
  open subscription, which is the intended fan-out but has no per-instance
  dedup/versioning yet.
- **Follow-ups / risks to watch:** Message buffering with TTL (`VTMessage` +
  message-name/id/key index); message start events (publish creates an instance);
  message boundary events; cross-partition correlation; message-name uniqueness
  validation at deploy.

## Pros and cons of the options

### Option 1 — symmetric key expression, no buffering (chosen)
- Good: minimal new surface; catch and throw share one key definition so they
  can't disagree; correlation is one prefix scan and one replayable mutation.
- Bad: publish-before-subscribe loses the message; fan-out has no dedup yet.

### Option 2 — buffer every published message
- Good: order-independent; enables message start events and late subscribers.
- Bad: a second index with its own lifecycle/TTL, and a matching pass on both
  publish and subscribe — more than the first slice needs. Deferred, not
  rejected.

### Option 3 — external broker
- Good: decouples producers from the engine.
- Bad: correlation state would live outside the log, breaking the single source
  of truth (ADR-0001) and the replay story. Wrong layer.

## Links

- relates to ADR-0001 (event sourcing and log-structured state)
- relates to ADR-0006 (partition routing and cross-partition communication)
- relates to ADR-0008 / ADR-0015 (FEEL for the correlation-key expression)
- relates to ADR-0009 (record serialization format)
