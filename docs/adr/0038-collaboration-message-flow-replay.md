# ADR-0038: Collaboration message-flow replay

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

A collaboration (ADR-0023) deploys as one runnable definition per pool, linked at
runtime only by message correlation (ADR-0020) and message start events
(ADR-0035). The Operations live view (ADR-0022) renders a single definition and
overlays its tokens, so an operator watching a Kunde ⇄ Lieferant collaboration can
only look at one pool at a time and never sees the messages that cross between
them. Operators asked to *watch the messages flow* — to open the whole
collaboration and replay the exchange.

The obstacle is that a message leaves no durable trace today. `applyToState`
deletes a message subscription on `IntentSubscriptionCorrelated`, and a message
start event is indistinguishable from a normal start once its instance exists. The
write-ahead log still holds the `SubscriptionCorrelated` events with timestamps,
but the log has no indexed read path — only a full replay. So there is nothing the
API can query to reconstruct "message *m* crossed to element *e* at time *t*".

## Decision drivers

- **Determinism / recovery (invariant I4).** Whatever we record must rebuild
  identically on replay, from the event alone.
- **Hot path (invariant I1).** Recording must not allocate per command on the
  processor path.
- **Reuse the established pattern.** Element-visit history (ADR-0022) already
  retains derived history in a write-only column family for the overlay; the
  message-flow story should mirror it rather than invent a mechanism.
- **Cover both delivery kinds.** The canonical collaboration uses an intermediate
  catch (a subscription correlates) *and* a message start event (a pool is
  instantiated); the timeline must show both.

## Considered options

1. **Tail the WAL into an external/analytics feed** (the ADR-0036 "event mirror"
   variant) and reconstruct flows there.
2. **Query the WAL on demand** for `SubscriptionCorrelated` events.
3. **Retain a message-flow history in state**, written from `applyToState`,
   analogous to element-visit history.

## Decision outcome

Chosen option: **"Retain a message-flow history in state"**, because it reuses the
element-visit pattern, keeps the read a cheap prefix scan, and is deterministic by
construction.

- A new value type `VTMessageFlow` carries one delivered flow
  (`MessageFlowValue`): sender/receiver instance keys, the receiver definition and
  element, the message name, and the correlation key.
- A new column family `cfMessageFlow` keys each flow by
  `(receiverDefKey, timestamp, position)`, so a definition's flows scan back in the
  order they occurred — the replay timeline. The timestamp and position come from
  the event header; the write is a plain `Set`, never deleted, like the visit
  counter.
- `correlateMessage` emits one `VTMessageFlow` event per delivery: once for each
  correlated catch subscription (the subscription now carries its element identity,
  set at subscribe time), and once for each message-start instantiation (the
  processor's message-start index now carries the receiving element). `applyToState`
  turns that event into a `RecordMessageFlow`, so recording is deterministic and
  replays identically.
- A new endpoint `GET /api/v1/collaborations/{key}/runtime` discovers the sibling
  pools (deployments sharing the identical BPMN body, newest version per pool),
  merges every pool's live/visited overlay onto the shared diagram, and returns the
  message flows across all pools sorted into one timeline. `GET /api/v1/processes`
  gains a `collaborationKey` hint so the Operations list can link to the replay.
- The Operations app gains a collaboration replay view: the shared diagram with the
  merged token overlay plus a transport (play / step / scrub) that animates an
  envelope dot along each message-flow edge in timestamp order.

### Consequences

- **Positive:** A finished collaboration replays its message exchange; the feature
  reuses the visit-history mechanism and touches no invariant. Recording is
  event-sourced, so it survives recovery.
- **Negative / trade-offs accepted:** Retention is unbounded for now, as with the
  process-instance and element-visit histories (ADR-0017, ADR-0022). Sibling pools
  are discovered by identical XML; two collaborations with byte-identical bodies
  would be treated as one (a copy-paste edge case). The subscription payload grew by
  a definition key and element index, extending the persisted `MessageSubscription`
  encoding.
- **Follow-ups / risks to watch:** A retention/compaction policy for history column
  families (shared with ADR-0017/0022). A first-class collaboration/deployment
  group id would be more robust than XML identity if versioned collaboration replay
  is wanted later.

## Pros and cons of the options

### Option 1 — WAL tail into an external feed
- Good: no new state; a general audit stream.
- Bad: depends on the unbuilt event mirror; puts a core Operations feature behind an
  external system; reconstruction is more complex than a prefix scan.

### Option 2 — Query the WAL on demand
- Good: no new persisted state.
- Bad: the WAL has no indexed read path; every query is a full scan, and the read
  model (which element, which edge) still has to be rebuilt each time.

### Option 3 — Retain message-flow history in state (chosen)
- Good: mirrors ADR-0022; deterministic; cheap prefix-scan read; covers both catch
  and message-start deliveries.
- Bad: adds a column family and a value type; unbounded retention until a shared
  history-compaction policy lands.

## Links

- relates to ADR-0020 (message correlation), ADR-0023 (collaborations and pools),
  ADR-0035 (message start events)
- mirrors ADR-0022 (element-visit history); shares retention concerns with ADR-0017
