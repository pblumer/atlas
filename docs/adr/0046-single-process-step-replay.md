# ADR-0046: Single-process step-by-step replay

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

The Operations live view (ADR-0013 viewer) shows where a token sits *now*, and the
element-visit heatmap (ADR-0022) shows *how often* each element has been walked, as
an aggregate count. Neither answers the question an operator asks of a finished (or
running) instance: *in what order did this instance walk its elements?* For a
collaboration we already replay the exchange step by step — the message-flow
timeline with a play/step/scrub transport (ADR-0038) — but that timeline records
only the messages that cross *between* pools, never the element sequence *inside* a
single process. So a plain, single-pool process — the common case — has a live
overlay and a heatmap but no way to be replayed step by step.

The obstacle is the same one ADR-0022 and ADR-0038 hit: Atlas is log-structured
(ADR-0001), so `applyToState` deletes an element instance the moment it completes,
and the visit counter it retains is keyed for aggregation, not ordering — it has no
timestamp and no per-instance sequence. Reconstructing the order from the
write-ahead log would mean a full replay per request; the WAL has no indexed read
path.

## Decision drivers

- **Determinism / recovery (invariant I4).** Whatever we record must rebuild
  identically on replay, from the event alone.
- **Hot path (invariant I1).** Recording must not allocate per command on the
  processor path.
- **Reuse the established pattern.** Message-flow history (ADR-0038) already retains
  a *time-ordered* derived history in its own column family for a replay transport;
  element-visit history (ADR-0022) already records on the same `IntentActivated`
  event. The single-process story should mirror both rather than invent a mechanism.
- **Reuse the transport.** The play/step/scrub UI built for collaboration replay
  should drive single-process replay too.

## Considered options

1. **Query the WAL on demand** for an instance's `IntentActivated` events and order
   them.
2. **Extend the element-visit counter key** with a timestamp/position so the visit
   family itself carries order.
3. **Retain a per-instance, time-ordered element-step history in state**, written
   from `applyToState`, analogous to the message-flow timeline.

## Decision outcome

Chosen option: **"Retain a per-instance element-step history"**, because it mirrors
ADR-0038, keeps the read a cheap prefix scan, reuses the existing replay transport,
and is deterministic by construction.

- A new column family `cfElementStep` keys each activation by
  `(processInstanceKey, timestamp, position)`, with the activated element's
  compiled-graph index as the value. The instance key leads, so one instance's whole
  trail is a single prefix scan; the timestamp orders the scan (the replay timeline),
  and the log position is the trailing disambiguator for two activations in the same
  nanosecond. The timestamp and position come from the event header; the write is a
  plain `Set`, never deleted — like the message-flow record.
- `applyToState`, on `IntentActivated` for an element instance, issues the step
  write (`Tx.RecordElementStep`) right after the existing visit-counter bump. Both
  derive solely from the event (the header's timestamp/position and the activated
  element), so replay rebuilds an identical, identically-ordered trail (invariant
  I4). The write is a single `Set` with no read, so it does not allocate on the hot
  path (invariant I1).
- A new endpoint `GET /api/v1/instances/{key}/timeline` resolves the instance —
  active or finished — to its definition (`Store.ProcessInstance` looks in the
  active family, then the terminal-history family, ADR-0017), maps each step's
  element index to a diagram id via the definition's compiled process, and returns
  the ordered steps with their timestamps and types.
- The Operations app gains a single-process replay view reusing the collaboration
  transport: the instance's diagram with a play / step / scrub bar that walks the
  token through the elements in activation order — the current element highlighted,
  the earlier ones grayed, and a token dot animated along the sequence flow between
  consecutive steps. The instance live view links to it.

### Consequences

- **Positive:** Any single instance — running or finished — replays its execution
  step by step; the feature reuses the visit-history recording shape, the message-
  flow timeline shape, and the collaboration replay transport, and touches no
  invariant. Recording is event-sourced, so it survives recovery.
- **Negative / trade-offs accepted:** Retention is unbounded for now, as with the
  process-instance, element-visit, and message-flow histories (ADR-0017, ADR-0022,
  ADR-0038). One extra `Set` per element activation. The step value carries only the
  element index — variable snapshots per step are out of scope for this MVP, so the
  replay shows *where* the token went, not the variable values at each point. A step
  between two elements not joined by a single drawn sequence flow (e.g. across a
  gateway, or a boundary interruption) highlights both nodes but does not animate a
  dot, since there is no single edge to travel.
- **Follow-ups / risks to watch:** A shared retention/compaction policy for the
  history families (shared with ADR-0017/0022/0038). Per-step variable snapshots if
  operators want to scrub variable values alongside the token. A guard on the step
  value decode (today it reads a fixed 4-byte index; a corrupt derived value would
  misread rather than error) if history ever spans encoding changes.

## Pros and cons of the options

### Option 1 — query the WAL on demand
- Good: no new persisted state.
- Bad: the WAL has no indexed read path; every query is a full scan that grows with
  history, not with the instance — the same reason ADR-0022 and ADR-0038 rejected it.

### Option 2 — extend the visit-counter key with time/position
- Good: no new column family.
- Bad: breaks the visit counter's aggregation — its whole point is that repeated
  visits to one element fold into a single count via the counter merger; adding a
  timestamp to the key makes every activation a distinct row and defeats the heatmap.
  Two different questions ("how often" vs "in what order") want two different keys.

### Option 3 — per-instance element-step history (chosen)
- Good: mirrors ADR-0038; deterministic; cheap prefix-scan read; reuses the replay
  transport; leaves the visit counter untouched.
- Bad: adds a column family; unbounded retention until a shared history-compaction
  policy lands.

## Links

- mirrors ADR-0038 (collaboration message-flow replay — same "separate time-ordered
  family, written from `applyToState`, driven by a play/step/scrub transport" shape)
- relates to ADR-0022 (element-visit history — same `IntentActivated` recording
  point, complementary question), ADR-0017 (process-instance history — the terminal
  lookup the endpoint reuses), ADR-0013 (the Operations viewer this extends)
- shares the unbounded-retention concern with ADR-0017, ADR-0022, ADR-0038
