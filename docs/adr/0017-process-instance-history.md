# ADR-0017: Retain finished process instances in a history index

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas engine maintainers

## Context and problem statement

Atlas is log-structured (ADR-0001): the state store materializes only *live*
records, and `applyToState` deletes an entity when it reaches a terminal state.
For a process instance that means `IntentCompleted` / `IntentTerminated` calls
`DeleteProcessInstance`, so the moment an instance finishes it vanishes from the
store.

That is correct for the hot path but leaves operators blind: the Operations UI
lists only `ActiveProcessInstances`, and most demo/short processes run straight
to completion, so the list is usually empty. There is no way to answer "which
instances have finished, and when?" without replaying the whole write-ahead log.
We need finished instances to be inspectable without giving up the lean
active-state scans that stats and the live overlay depend on.

## Decision drivers

- **Keep active scans lean and correct.** `ActiveProcessInstances`,
  `ActiveProcessInstanceCount`, and the live overlay must keep meaning *live*
  only — no filtering hot-path scans past tombstoned rows.
- **Invariant I4.** Any retention must happen inside `applyToState` from the
  event alone, deterministic and side-effect-free, so recovery reproduces it.
- **Bounded, obvious data model.** Reuse the existing column-family / value
  machinery rather than inventing a parallel store.

## Considered options

1. **Keep completed instances in the active column family with a state flag**,
   and filter every active scan to `state == active`.
2. **Write a separate history column family** on completion; keep deleting the
   active record.
3. **Reconstruct history on demand by replaying the WAL** in the query layer.

## Decision outcome

Chosen option: **"Separate history column family"**. On
`IntentCompleted` / `IntentTerminated`, `applyToState` writes a
`ProcessInstanceValue` — now carrying a terminal `State` and a `CompletedAt`
timestamp taken from the event header — into a new `cfProcessInstanceHistory`
family, and then deletes the active record exactly as before. The completion
time and terminal state are derived only from the event (its intent and
`RecordHeader.Timestamp`), so a replay rebuilds identical history.

The API's instance listing unions active instances (state `active`) with the
history family (state `completed` / `terminated`, with `completedAt`), and the
Operations UI renders both.

### Consequences

- **Positive:** Active scans, counts, and the live overlay are untouched and
  still mean "live". History is one prefix scan. Retention is a pure, replayable
  state mutation — no new side-effect surface.
- **Negative / trade-offs accepted:** History grows unbounded; there is no
  retention/compaction policy yet. `ProcessInstanceValue` grew from 8 to 17
  bytes (a `State` byte + an `int64 CompletedAt`); active records now carry
  `state=active, completedAt=0`.
- **Follow-ups / risks to watch:** A retention/TTL or archival policy for the
  history family. Instance-scoped variables are not cleaned up on completion, so
  a finished instance's variables remain readable — revisit when variable
  lifecycle is formalized.

## Pros and cons of the options

### Option 1 — state flag in the active family
- Good: single family; no new key space.
- Bad: every active scan and count must now filter tombstoned rows, re-adding
  hot-path work and a footgun (forget the filter → completed instances leak into
  "active"). Muddies the meaning of the active family.

### Option 2 — separate history family (chosen)
- Good: active semantics unchanged; history isolated; still a plain, replayable
  `applyToState` write.
- Bad: two places a process instance can live; a small value-encoding change.

### Option 3 — replay the WAL on demand
- Good: zero extra stored state.
- Bad: expensive and unbounded per query; pushes log-format knowledge into the
  query layer; no random access. Wrong tool for an operator list view.

## Links

- relates to ADR-0001 (event sourcing and log-structured state)
- relates to ADR-0009 (record serialization format)
