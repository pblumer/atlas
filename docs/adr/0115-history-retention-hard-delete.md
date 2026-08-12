# ADR-0115: History retention — an export-gated, age-based hard delete of finished instances

- **Status:** Accepted
- **Date:** 2026-08-12
- **Deciders:** Atlas engine team

## Context and problem statement

The instance TTL (ADR-0085) bounds the **active** set: a parked instance self-cleans by
terminating into the history index (ADR-0017). But that history grows **unbounded** —
ADR-0017 flagged "History grows unbounded; there is no retention/compaction policy yet"
and ADR-0085 named an age-based prune of terminated records as its natural companion.
Operators doing housekeeping want the finished "data corpses" to age out automatically —
**but only once the data is safely exported**, so nothing is lost.

Now that the OpenSearch exporter (ADR-0114) exists, the guarantee is expressible: a
finished instance may be hard-deleted from the operational state store **after its events
are in OpenSearch**. This ADR adds that retention layer.

Two constraints make it non-trivial:

1. **State is a fold of the log (ADR-0001).** Deleting a history record straight out of
   the Pebble state store is not durable — recovery replays the log and rebuilds it. Any
   delete must itself be a **durable event**, so replay reproduces the deletion (I4/I6).
2. **Never delete ahead of export.** The operator's requirement — "make sure the data can
   still be exported (OpenSearch)" — must be a hard gate, not a hope.

## Decision drivers

- **Replayable delete (I4/I6).** The purge must rebuild identically on recovery, so it
  rides a durable event folded by `applyToState`, not a raw out-of-band store delete.
- **Export-before-delete.** An instance is purged only once *all* its events are exported
  — a provable gate, tied to the exporter's high-water mark.
- **No loop-blocking full scan (the ADR-0085 rule).** The sweep must be bounded per tick,
  not an O(history) scan on the single-writer loop.
- **Complete cleanup.** A finished instance owns many per-instance state families (element
  step/replay/visit history, variable snapshots, variable audit, decision evaluations,
  live variables, data objects and their snapshots, compensables). Purging only the
  headline record would orphan all of them.
- **Counters stay honest.** The per-definition *finished* count (ADR-0083) is monotonic by
  design ("no un-finishing"); a purge must not touch it, nor the already-decremented active
  count.
- **Opt-in, off by default.** Like TTL, a deletion policy that silently removes data is
  worse than a leak; retention is enabled only by an explicit max-age.

## Considered options

1. **A durable purge event + a bounded, export-gated sweep (chosen).** A new
   `IntentPurged` on `VTProcessInstance`, emitted by a periodic sweep that selects finished
   instances older than the max-age whose terminal log position is at or below the safe
   (exported) position. `applyToState` folds the event into a delete of every per-instance
   family. Replay-safe, export-gated, bounded.
2. **Out-of-band background compaction** that deletes history straight from Pebble. Simple,
   but it violates state=fold(log): recovery rebuilds what it deleted, so it is not durable
   without also rewriting the WAL. **Rejected.**
3. **WAL truncation/compaction** (reclaim the log itself). This is the deeper "reclaim
   disk" feature; it needs segment rewriting bounded by the min consumer position and is a
   separate, harder ADR. Retention of the *state* is the operator-visible win and is
   orthogonal. **Deferred.**

## Decision outcome

Chosen: **option 1.** The mechanism rests on one insight that makes it safe: **the exporter
reads the immutable WAL, while retention purges the mutable state store.** Pruning state
therefore never removes anything the exporter needs — the WAL still holds every event — and
the export-position gate simply guarantees OpenSearch already has an instance before its
operational copy is dropped.

- **Terminal log position on the history record.** `ProcessInstanceValue` gains a trailing,
  append-compatible `CompletedPosition uint64`, set in `applyToState` at the
  `IntentCompleted`/`IntentTerminated` transition from the terminal event's header
  `Position`. Because it comes only from the event, replay rebuilds it identically (I4/I6).
  It is the instance's highest log position (its terminal event is its last), so
  `CompletedPosition ≤ exportedPosition` proves every event of the instance is exported.
  Records written before this feature carry `0`; a zero is treated as "unknown → not
  export-provable" and is conservatively **never** auto-purged.

- **The purge event.** A new `VTProcessInstance` intent pair mirrors termination:
  `IntentPurging` (the command the sweep enqueues) → `IntentPurged` (the durable event).
  The command carries the instance's `ProcessInstanceValue` (the sweep already read it), so
  the handler emits `IntentPurged` with the `ProcessDefKey` its cleanup needs, and replay
  needs nothing but the event.

- **`applyToState(IntentPurged)`** deletes the terminal history record and every
  per-instance family addressable from the instance key (and its definition key): element
  step/replay/visit history, variable snapshots, variable audit, decision evaluations, live
  variables, data objects and their snapshots, and any residual compensables. It touches
  **no** per-definition counter — the active count was already decremented at termination,
  and the finished count is monotonic (ADR-0083). Deleting an already-absent key is a
  no-op, so a re-applied purge (replay, or a double-enqueue) is idempotent.

- **The sweep.** A periodic `retentionSweeper` goroutine, one bounded turn per tick on the
  run loop (the `timerScheduler`/bulk-terminate pattern, ADR-0090): it scans finished
  instances in a resumable, capped window (`CompletedProcessInstancesFrom(cursor, limit)`),
  selects those with `age(CompletedAt) > maxAge` **and** `0 < CompletedPosition ≤
  safePosition`, enqueues a purge per selected instance, and drives them to durable events
  in the same turn. A cursor advances across ticks and wraps at the end, so no single tick
  scans the whole family (the ADR-0085 no-full-scan rule).

- **The safe position (the export gate).** When the exporter is enabled, `safePosition` is
  its high-water mark — so retention deletes only what OpenSearch already holds, the
  operator's exact requirement. When the exporter is disabled, `safePosition` is the state
  store's `LastAppliedPosition` (the durable watermark): retention still works standalone by
  age, with the WAL as the archive of record. The exporter's high-water mark is read
  race-free (an atomic), since the sweep and the exporter run on different goroutines.

- **Config.** Opt-in via `--retention-max-age` / `ATLAS_RETENTION_MAX_AGE` (a duration; `0`
  = off). The sweep interval and per-tick cap are internal, with a test override.

### Consequences

- **Positive:** finished-instance history is bounded automatically; the delete is a pure,
  replayable `applyToState` mutation (I4/I6), so recovery reproduces it; the export gate is
  a hard guarantee, not a race; the sweep is bounded per tick (no loop-blocking full scan);
  it composes cleanly with TTL (bounds the active set) and the exporter (preserves the
  finished set before deletion). Enabling exporter + retention gives "export, then delete."
- **Negative / trade-offs accepted:** the WAL still retains every event (this reclaims the
  *state* store, not the log — WAL compaction is a separate, deferred feature), so disk on
  the log side is unchanged; instances finished before this feature (no `CompletedPosition`)
  are never auto-purged; message-flow and incident history are keyed off the instance key
  and are out of the per-instance purge (small, definition- or element-scoped, and a
  finished instance holds no live incident); sub-scope variables/data objects that outlived
  their activity are not swept (a finished instance's live state is root-scoped in practice).
- **Follow-ups / risks to watch:** WAL segment retention bounded by the min exported
  position (reclaim the log); an operator-facing "purge now" endpoint and a per-definition
  retention override; surfacing the purge in Operations and (already) in OpenSearch as an
  `IntentPurged` marker document.

## Pros and cons of the options

### Option 1 — durable purge event + export-gated sweep
- Good: replay-safe (state stays a fold of the log); hard export gate; bounded sweep;
  complete per-instance cleanup; counters untouched.
- Bad: a new intent and a multi-family delete in `applyToState`; the WAL is not reclaimed.

### Option 2 — out-of-band Pebble compaction
- Good: no new event; trivially "just delete".
- Bad: violates state=fold(log) — recovery rebuilds the deleted rows; not durable without
  also editing the WAL.

### Option 3 — WAL truncation
- Good: reclaims the log itself (real disk).
- Bad: needs segment rewriting bounded by the min consumer position; much larger blast
  radius; orthogonal to bounding the queryable state. Deferred to its own ADR.

## Links

- companion to ADR-0085 (process-instance TTL — bounds the *active* set; this bounds the
  *finished* set, the follow-up it named) and ADR-0114 (OpenSearch exporter — this consumes
  its high-water mark as the delete gate)
- deletes the history introduced by ADR-0017; leaves the ADR-0083/0080 per-definition
  counters untouched (the finished count is monotonic)
- reuses the ADR-0090 bulk-terminate sweep shape (bounded turn, one command per instance,
  `Drive`); honors I4/I6 (durable, replay-deterministic delete) and the ADR-0085
  no-full-scan rule
