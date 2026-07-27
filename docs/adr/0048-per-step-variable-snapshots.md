# ADR-0048: Per-step variable snapshots in the single-process replay

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

The single-process replay (ADR-0046) walks a finished or running instance through
its diagram step by step, showing *where* the token went. It deliberately left out
*what the variables were* at each point — its own follow-up note. The live view
(and the finished-instance list) shows only the instance's *current* variables,
because the variable store (`cfVariable`) is keyed `(scopeKey, name)` and each
change upserts in place, so an earlier value is overwritten and lost. When an
operator scrubs back to an earlier step, there is nothing to show the variable
values as they stood then.

The obstacle is the same log-structured one the element-step and message-flow
timelines already hit (ADR-0022, ADR-0038, ADR-0046): the write-ahead log holds
every `VariableCreated`/`VariableUpdated` event with its value and timestamp, but
the state store keeps only the latest value, and the WAL has no indexed read path.

## Decision drivers

- **Determinism / recovery (invariant I4).** Whatever we record must rebuild
  identically on replay, from the event alone.
- **Hot path (invariant I1).** Recording must not allocate per command on the
  processor path.
- **Reuse the established pattern.** The element-step timeline (ADR-0046) retains a
  time-ordered per-instance history in its own column family, written from
  `applyToState` on the same event that mutates live state. Variables should mirror
  it exactly, so the two timelines fold together.
- **Correct point-in-time semantics.** "The variables as the token entered element
  X" must be well-defined, including when a change and a step share a timestamp.

## Considered options

1. **Snapshot the whole variable set per element step** into the step record.
2. **Retain a per-instance variable-change history** (each change, time-ordered),
   and fold it against the step timeline at read time.
3. **Reconstruct from the WAL on demand.**

## Decision outcome

Chosen option: **"Retain a per-instance variable-change history and fold at read
time"**, because it mirrors ADR-0046's mechanism, records the minimum (one entry
per actual change, not a full snapshot per step), and folds by log position for
exact point-in-time semantics.

- A new column family `cfVariableSnapshot` keys each change by
  `(scopeKey, timestamp, position)` — the same shape as the element-step key — with
  the changed variable's full new value (`VariableValue`) as the payload. The scope
  key leads (a process instance today), so one instance's changes are a single
  prefix scan in change order; the timestamp and position come from the event
  header; the write is a plain `Set`, never deleted.
- `applyToState`, on `IntentVariableCreated`/`IntentVariableUpdated`, issues the
  history write (`Tx.RecordVariableSnapshot`) right after the existing live
  `PutVariable`. Both derive solely from the event, so replay rebuilds an identical,
  identically-ordered trail (invariant I4); one extra `Set`, no read, so no hot-path
  allocation (invariant I1).
- The instance-timeline endpoint (`GET /api/v1/instances/{key}/timeline`, ADR-0046)
  now folds: it scans the element steps and the variable changes, sorts both by log
  position, and walks the steps applying every change at or before each step's
  position into a running map. Each step therefore carries the variable values as
  they stood **when the token entered that element**. Folding by position (not just
  timestamp) is exact: a change and a step in the same nanosecond order by the log's
  monotonic position. A task's output variables, written on the task's completion,
  become visible at the *next* step — the point-in-time reading, not "the element
  that produced them."
- The replay view gains a variables panel that shows the current step's snapshot and
  updates as the operator plays, steps, or scrubs.

### Consequences

- **Positive:** Scrubbing the replay now shows the variable values at each point,
  not just the final state; the feature reuses ADR-0046's recording and read shapes;
  recording is event-sourced, so it survives recovery. Only actual changes are
  stored, not a full snapshot per step.
- **Negative / trade-offs accepted:** Retention is unbounded for now, as with the
  process-instance, element-visit, message-flow, and element-step histories
  (ADR-0017/0022/0038/0046). One extra `Set` per variable change. The read folds a
  full snapshot into every step of the response, which repeats unchanged values
  across steps — fine at the current instance scale; a later optimization could send
  deltas. Scope is the process-instance root today (as everywhere in the engine); a
  subprocess-scoped variable would be keyed under its own scope, which the
  instance-level fold does not yet gather.
- **Follow-ups / risks to watch:** The shared retention/compaction policy for the
  history families. Subprocess/child-scope variables once scoping grows beyond the
  instance root. A delta-encoded timeline response if per-step snapshots grow large.

## Pros and cons of the options

### Option 1 — snapshot the whole variable set per step
- Good: no fold at read time.
- Bad: writes a full snapshot on every element activation even when nothing changed,
  multiplying storage by the step count; the hot-path write is no longer minimal.

### Option 2 — per-instance variable-change history, fold at read (chosen)
- Good: mirrors ADR-0046; records only real changes; deterministic; exact
  point-in-time semantics via position folding.
- Bad: adds a column family; a read-time fold; unbounded retention until a shared
  compaction policy lands.

### Option 3 — reconstruct from the WAL on demand
- Good: no new persisted state.
- Bad: the WAL has no indexed read path; every query is a full scan that grows with
  history — the same reason the other timelines rejected it.

## Links

- extends ADR-0046 (single-process step replay — this fills its deferred per-step
  variable follow-up, reusing the same key shape and recording point)
- mirrors ADR-0038 (message-flow replay) and ADR-0022 (element-visit history) in
  recording derived history from `applyToState`
- shares the unbounded-retention concern with ADR-0017/0022/0038/0046
