# ADR-0094: Singleton message start — at most one live instance per correlation key

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

## Context and problem statement

A message start event (ADR-0035) instantiates a **new** process every time a message
with its name correlates — matching is by name only; the correlation key is recorded
on the created instance for display but never gates creation (`correlateMessage`,
`engine/behavior.go`). Combined with the clio inbound bridge (ADR-0075), which
republishes every watched clio event as a message, this means every event on a watched
subject starts a process. When `/employees` was connected, the historical
`employee.create` backlog started one instance per event — hundreds of thousands of
parked instances (the incident that motivated ADR-0075's forward-only default and the
bulk-cancel drain).

Forward-only ingestion and a per-poll cap stop a *backlog* from flooding the engine.
They do not express a different, common intent: **one live process per business
entity**. If several events arrive for the *same* employee (an `employee.create` then
an `employee.update`, a redelivery, or two events in one poll), each still starts a
separate onboarding instance. Operators want a message start that is **idempotent per
correlation key**: while an instance started for key *K* is still live, another message
for *K* wakes/So nothing, rather than starting a duplicate.

## Decision drivers

- **Don't change existing semantics silently.** ADR-0035's start-per-message behavior
  is relied upon; the new behavior must be **opt-in**, default off.
- **Deterministic recovery (I4/I6).** The "is one already live?" fact must be rebuilt
  identically on replay — it therefore rides engine state (events), not a live-only
  side computation.
- **Cheap.** The check must be a point lookup, not an O(active instances) scan — the
  flood is exactly when a per-message scan would be catastrophic.
- **Engine stays connector-agnostic (ADR-0036/0075).** The gate lives in
  `correlateMessage`, driven by a compiled model flag, and knows nothing of clio.

## Considered options

1. **Global**: every message start with a non-empty correlation key is singleton.
   Simplest, but changes ADR-0035 for every existing model — surprising.
2. **Per inbound subscription (ADR-0075)**: a `singleton` flag on the subscription.
   Keeps it near clio, but the gate is in the engine's `correlateMessage`, so the flag
   would have to be threaded from the connector through the publish command into the
   core — leaking a connector concept into the clio-agnostic engine.
3. **Opt-in model flag on the message start event (chosen).** A
   `singletonStart="true"` attribute on the `<startEvent>`. The compiler carries it on
   the message-start detail; the engine gates creation on it. Default off, so every
   existing model is unchanged; a modeler opts a specific process into
   one-per-entity.

## Decision outcome

Chosen: **option 3.** A message start event may declare `singletonStart="true"`. When
set and the event's evaluated correlation key is non-empty, `correlateMessage` starts a
new instance **only if no live instance of that definition already began with that
key**. The liveness fact is a durable per-`(definitionKey, correlationKey)` counter,
`cfActiveStartKey` (`activeStartKey:<defKey>:<corrKey> → int32`), a composing merge
counter exactly like `cfActiveChildren` (ADR-0074): `applyToState` increments it on a
process instance's `Activated` event when the instance carries a correlation key, and
decrements it on `Completed`/`Terminated`. Because it is event-driven it rebuilds
identically on replay (I4/I6); because it is a keyed counter the gate is a point lookup.

The gate decision itself is **live-only** — `handleMessagePublished` is a command
handler, not a replayed event, so its skip/create choice is never re-run on recovery;
replay simply re-applies whatever `IntentActivating` events were emitted live. Within a
single batch, several messages for the same key would all read the counter before any
of their (followup) creates apply, so the processor also holds a **per-batch set** of
`(defKey, key)` already scheduled this batch and skips a second create for the same key
in the same batch. Across batches the durable counter carries the fact. An empty key is
never singleton (an empty key identifies no entity — it always starts), and a
non-singleton start (the default) is unchanged.

### Consequences

- **Positive:** a process can be modeled as one-per-entity; repeated events for the same
  key no longer pile up duplicate instances; the guard is a point lookup, recovery-safe,
  and engine stays connector-agnostic. Completing/terminating the live instance
  re-opens the key, so the *next* event starts a fresh one.
- **Negative / trade-offs:** one extra merge write per message-start instance
  activation/termination (negligible, mirrors `activeChildren`); a new column family.
  The intra-window guarantee is "at most one live instance per key" enforced by the
  durable counter across batches plus the per-batch set within one batch — a message
  that correlates a *catch* is unaffected (this ADR only gates *starts*).
- **Follow-ups:** a message start could optionally *wake* the existing instance instead
  of no-op'ing when it is singleton and one is live (buffer/deliver the payload); left
  out here (no message buffering yet, ADR-0020).

## Links

- gates ADR-0035 (message start) with the ADR-0020 correlation key; mirrors the
  ADR-0074 `activeChildren` merge counter; motivated by the ADR-0075 inbound flood.
- honors I1 (point lookup, no per-command scan), I4/I6 (event-driven counter rebuilds
  on replay).
