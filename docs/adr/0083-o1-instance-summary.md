# ADR-0083: An O(1) instances summary — per-definition finished-count and last-activity counters

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0080 gave the Operations *runtime* view O(1) reads: a per-definition
active-instance counter (`DefInstanceCount`) and per-element token/visit counters,
maintained in `applyToState` and seeded from existing state by a one-time backfill, so
the process-runtime overlay no longer scans every instance.

The Operations **overview** (`GET /api/v1/instances/summary`, one running/finished row
per process) was not converted. It still scanned *all* active and *all* completed
process instances in a single run-loop closure to tally per-definition counts. With a
definition holding hundreds of thousands of instances (the reported `/employees`
flood) that scan blocks the single-writer loop for many seconds on every load — the
overview reports "Failed to fetch" and, because the scan monopolizes the one loop,
concurrent pages (e.g. the Modeler) stall too. Crucially, **draining the active
instances does not help**: cancelling moves them into the *history*, which the
"finished" scan then walks — the cost moves rather than disappears.

## Decision drivers

- **O(1) like the rest of the runtime view (ADR-0080/I1):** the overview must read a
  handful of counters, never scan instances or history.
- **Survive draining:** the "finished" count must not require scanning the history,
  which only grows as active instances are terminated into it.
- **Deterministic recovery (I4/I6):** the counters must rebuild identically on replay
  and be seeded for state that predates them, exactly as ADR-0080's counters are.

## Decision outcome

Extend ADR-0080 with two more per-definition counters, maintained in `applyToState`
alongside `DefInstanceCount`:

- **`cfDefCompletedCount`** — a monotonic merge counter bumped on every process-instance
  `Completed`/`Terminated` (never decremented: finished instances only accumulate). The
  summary's "finished" column reads it in O(1) instead of scanning the history.
- **`cfDefLastActivity`** — the unix-nano timestamp of a definition's most recent
  instance lifecycle event, written by **overwrite** on each `Activated`/`Completed`/
  `Terminated`. The processor's event timestamps are non-decreasing in log order, so the
  last write is the latest and replay rebuilds the identical value; it needs no read.

`handleInstancesSummary` now iterates the deployed definitions (`s.order`, ~tens) and
does three point reads each (`DefInstanceCount`, `DefCompletedCount`,
`DefLastActivity`) — O(definitions), never O(instances). A one-time migration
(`backfillSummaryCountersIfNeeded`, guarded by its own `summary_counters_v1` marker,
separate from ADR-0080's marker so a store that already ran that backfill still seeds
these) scans the current active instances and history once to seed the finished counts
and last-activity, then records the marker in a single synced batch — so a crash
mid-migration leaves nothing and re-runs cleanly.

### Consequences

- **Positive:** the overview is responsive at any instance count, and stays so after the
  runaway instances are drained — the whole point of being able to drain them. The
  single-writer loop is never blocked by a summary load, so other pages stop stalling.
- **Negative / trade-offs:** two more column families and a second one-time backfill
  scan at first open after upgrade (a few seconds for a large history, once). The
  "finished" count and history are unbounded for now — a history-retention/compaction
  policy is a separate follow-up, but no longer gates the overview's responsiveness.

## Links

- extends ADR-0080 (runtime aggregate counters) with the same merge-counter + one-time
  backfill pattern; unblocks the overview the ADR-0075 flood (and the bulk-cancel drain
  that follows it) otherwise left slow; honors I1 (point reads), I4/I6 (replay-rebuilt
  counters).
