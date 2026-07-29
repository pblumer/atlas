# ADR-0080: Sublinear runtime views via maintained aggregate counters

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

## Context and problem statement

The Operations runtime view — "how many instances of this definition are live, and
how many tokens sit on each element" — was computed by **scanning every active
element instance and every active process instance** and filtering by definition
(`handleProcessRuntime`, and the same per pool in `handleCollaborationRuntime`).
Worse, those scans run inside `s.do`, i.e. on the single-writer run-loop goroutine
(ADR-0007). So a single runtime request is O(total instances) *and* holds the run
loop for its whole duration — starving every other command and query.

At a few hundred thousand instances this stops being theoretical: a store that
accumulated ~529k parked instances made each runtime/stats/list request time out,
because one decode-heavy scan monopolized the loop. The engine markets millions of
instances; the read path has to be sublinear for that to be true.

## Decision drivers

- **Sublinear reads.** The default runtime view must be O(elements), not O(instances).
- **Don't starve the loop.** A read must not hold the run loop for an O(N) scan.
- **Invariants.** Any maintained index must fold in the one `applyToState` used live
  and on recovery (I4), stay deterministic (I6), and not allocate on the hot path (I1).
- **Existing state persists.** The store is not rebuilt from the log on every start;
  it replays only the tail from `LastAppliedPosition`. A new derived index must be
  seeded for instances that already exist, or it reads zero for them.

## Considered options

1. **Off-loop snapshot reads.** Keep the O(N) scan but run it against a Pebble
   snapshot off the run loop, so it no longer starves writes. Removes the starvation
   but each read is still O(N) — slow, and heavy under churn.
2. **Maintained aggregate counters (chosen).** Fold three signed counters in
   `applyToState`: active instances per definition, live tokens per (definition,
   element), and cumulative visits per (definition, element). The runtime view reads
   them directly — O(elements).
3. **Full state snapshots for fast recovery.** Orthogonal (it addresses restart time,
   not query cost); a larger, separate effort.

## Decision outcome

Chosen: **option 2.** Three Pebble *merge* counters (reusing the existing
`counterMerger`, so increments/decrements are write-only — no read, no hot-path
allocation, I1): `cfDefInstanceCount`, `cfElementTokenCount`, `cfElementVisitAgg`.
`applyToState` bumps them on the instance/element activation and completion/
termination events it already folds, so they compose across a crash and rebuild on
replay (I4/I6), exactly like the active-children and element-visit counters they
generalize. `handleProcessRuntime`'s aggregate path and every pool of
`handleCollaborationRuntime` now read the counters — O(elements), never a scan over
every instance, so the run loop is never held for an O(N) read.

Because a live store persists and replays only the tail, the counters are **seeded
once** from existing state on store open (`backfillRuntimeCountersIfNeeded`): a
one-time scan accumulates per-definition and per-element totals in memory and writes
them plus a version marker in a single atomic, synced batch, so a crash mid-migration
leaves nothing and the next open re-runs cleanly (no double count). Thereafter
`applyToState` maintains them.

The single-instance overlay (`?instance=`) — a deliberate isolate-one-instance action,
not the default view — still walks instances filtered to the one; making it sublinear
too is a follow-up.

### Consequences

- **Positive:** the default and collaboration runtime views are O(elements) and never
  starve the run loop; the engine stays responsive at hundreds of thousands of
  instances; the mechanism reuses the proven merge-counter machinery and folds in the
  one `applyToState` (correct live and on recovery).
- **Negative / trade-offs accepted:** three extra counter writes per instance/token
  lifecycle event (write amplification traded for cheap reads — the right trade when
  reads must scale); a one-time backfill scan on the first open after upgrade; the
  `?instance=` filter path remains O(instances) for now.
- **Follow-ups / risks to watch:** make the `?instance=` overlay sublinear; paginate
  the instance/finished listings (still O(N)); state snapshots for fast recovery
  (restart still replays the tail from the last snapshot/genesis).

## Pros and cons of the options

### Option 1 — off-loop snapshot reads
- Good: removes run-loop starvation with no fold changes.
- Bad: still O(N) per read; heavy under write churn; doesn't make the view scale.

### Option 2 — maintained counters (chosen)
- Good: O(elements) reads; folds in `applyToState` (recovery-correct); reuses merge
  counters (I1).
- Bad: write amplification; a one-time backfill for pre-existing state.

### Option 3 — full state snapshots
- Good: also fixes restart/recovery time.
- Bad: orthogonal to query cost and much larger; deferred.

## Links

- honors I1 (no hot-path allocation), I3 (single-writer run loop), I4/I6
  (deterministic fold, live and on recovery); builds on ADR-0007 (run loop / `do`),
  ADR-0022 (element-visit history the visit aggregate rolls up), ADR-0017 (retained
  history)
