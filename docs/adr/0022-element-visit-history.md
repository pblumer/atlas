# ADR-0022: Retain a per-element token-visit history for the Operations overlay

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas engine maintainers

## Context and problem statement

The Operations live overlay (ADR-0013 viewer, driven by
`GET /processes/{key}/runtime`) highlights where tokens sit **right now**: it
scans the active element-instance family and badges each element with its live
token count. That is exactly empty for the common case — short processes run
straight to completion, so a moment later the diagram is blank and the operator
sees nothing about where tokens actually flowed.

Atlas is log-structured (ADR-0001): `applyToState` deletes an element instance
the moment it completes, so once a token leaves an element there is no record it
was ever there. Answering "which elements have tokens passed through, and how
often?" — the flow distribution an operator wants as a heatmap — would otherwise
mean replaying the whole write-ahead log per request. We need the visited-element
footprint to be inspectable cheaply, without giving up the lean active-only scans
the live overlay and stats depend on.

## Decision drivers

- **Keep active scans lean and correct.** `ActiveElementInstances` and the live
  token count must keep meaning *live* only — no filtering hot-path scans past
  finished work.
- **Invariant I1 (no hot-path allocation).** Recording a visit happens on every
  element activation, on the processor's hot path.
- **Invariant I4 (`applyToState` is the one mutator).** Any retention must be a
  deterministic, side-effect-free state mutation derived from the event alone, so
  recovery reproduces it byte-for-byte.
- **Reuse existing machinery.** Prefer the column-family / counter primitives we
  already have over inventing a parallel store.

## Considered options

1. **Reconstruct the footprint on demand by replaying the WAL** in the query
   layer.
2. **A per-element counter column family**, incremented from `applyToState` on
   each element activation via the existing counter merger, keyed so both the
   definition-wide and single-instance heatmaps are prefix scans.
3. **Never delete completed element instances**, flagging them terminal and
   filtering active scans.

## Decision outcome

Chosen option: **"Per-element counter column family"**. A new
`cfElementVisit` family keys a signed-int64 counter by
`(processDefKey, processInstanceKey, elementId)`. On `IntentActivated` for an
element instance, `applyToState` — after writing the active record and bumping
the active-children counter — issues a write-only `Merge` of `+1` on that key
(`Tx.RecordElementVisit`), exactly the allocation-free pattern the
active-children counter already uses (the `atlas.counter.sum.v1` merger). The
count is derived solely from the event payload, so replay rebuilds an identical
heatmap (invariant I4).

The definition key leads the composite key so a definition's whole heatmap is one
prefix scan; the instance key follows so a single instance's footprint is a
narrower prefix scan (`Store.ElementVisitHistory`, honoring the runtime
endpoint's existing `?instance=` filter). The runtime response gains a `visits`
field per element alongside the live `tokens`; the overlay draws elements with
live tokens green and history-only elements gray, so the distribution is visible
even after every instance has finished.

Unlike the active record, the visit counter is **not** deleted on completion —
retaining it is the whole point.

### Consequences

- **Positive:** Active scans, counts, and the live overlay are untouched and
  still mean "live". The heatmap is one prefix scan. Recording a visit is a
  write-only merge — no read, no allocation on the hot path (I1) — and a pure,
  replayable state mutation with no new side-effect surface (I4).
- **Negative / trade-offs accepted:** History grows unbounded, as with the
  process-instance history index (ADR-0017); there is no retention/compaction
  policy yet. A definition-wide scan yields one row per (instance, element), so
  the query layer sums per element. One extra `Merge` per element activation.
- **Follow-ups / risks to watch:** A shared retention/TTL policy covering both
  this family and `cfProcessInstanceHistory`. If per-instance granularity proves
  unnecessary, the key could collapse to `(processDefKey, elementId)` and shed
  the instance dimension.

## Pros and cons of the options

### Option 1 — replay the WAL on demand
- Good: no new state, no retention question.
- Bad: a full-log replay per overlay poll (every 1.5 s) is wildly out of budget
  and scales with history, not with the diagram.

### Option 2 — per-element counter family (chosen)
- Good: active semantics unchanged; heatmap isolated to its own family; write-
  only merge keeps the hot path allocation-free; deterministic replay.
- Bad: unbounded growth; a small per-activation cost; def-wide scan needs
  caller-side summing.

### Option 3 — retain terminal element instances in the active family
- Good: single family; no new key space.
- Bad: every active element scan and the live count must now filter tombstoned
  rows — re-adding hot-path work and a footgun (forget the filter → finished
  tokens leak into "live"), the exact trap ADR-0017 rejected for process
  instances.

## Links

- relates to ADR-0017 (process-instance history — same "separate family, written
  from `applyToState`, unbounded for now" shape)
- relates to ADR-0001 (log-structured state), ADR-0013 (the overlay this feeds)
- builds on the counter merger introduced for the active-children counter
