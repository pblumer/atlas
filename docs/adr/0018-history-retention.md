# ADR-0018: Age-based retention for finished-instance history

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas engine maintainers

## Context and problem statement

ADR-0017 made finished process instances inspectable by retaining them in a
history column family instead of deleting them. That family has no bound, so it
grows for the life of the deployment — every completed or terminated instance
stays forever, along with the variables under its scope. A long-running server
accumulates history without limit. We need a retention policy that reclaims old
history while keeping the invariants intact — in particular I4, which forbids
`applyToState` from reading wall-clock time (it must replay identically).

## Decision drivers

- **Invariant I4.** The decision of *what* is expired depends on the current
  time, which `applyToState` may not read. Any time-dependent choice must be made
  during live command processing and frozen into events.
- **Replay determinism.** Whatever gets deleted live must be deleted identically
  on recovery, from the log alone.
- **Bounded, idiomatic queries.** Finding expired history must not be a full
  scan; reuse the existing time-ordered-index pattern (the timer due-date index).
- **Operational simplicity.** No new scheduler subsystem if an existing seam
  (the server's single run loop) can drive the sweep.

## Considered options

1. **Count cap** — keep the most recent N finished instances, evicting the
   oldest. Fully deterministic (can live in `applyToState`), bounds storage
   directly, but bounds by count, not age.
2. **Age-based TTL** — drop instances completed longer ago than a retention
   window, swept periodically.
3. **Both** — cap by count and age.

## Decision outcome

Chosen option: **"Age-based TTL"** (the operator's choice: bound by how long
finished instances remain inspectable, not by how many).

Mechanism:

- A second index, `cfHistoryByTime` (`completedAt : piKey → nil`), orders finished
  instances by completion time, exactly like the timer due-date index. It is
  written alongside the history value when an instance ends.
- `PurgeExpiredHistory` enqueues a `PurgeHistory` **command**. Its handler reads
  the clock **once** (outside `applyToState`), computes `cutoff = now -
  retention`, range-scans `cfHistoryByTime` up to `cutoff`, and emits one
  `HistoryPurged` **event** per expired instance.
- `applyToState` on `HistoryPurged` deletes the history value, its time-index
  entry, and the instance's variables (a whole-prefix range delete). Because the
  event names the instance and carries its `CompletedAt`, replay deletes exactly
  the same records — no clock read on the replay path.
- The API server runs a ticker that drives the sweep on a cadence, so history is
  bounded even with no other activity. Retention window and sweep interval are
  flags (`--history-retention`, default 7 days; `--history-sweep-interval`,
  default 1h). A retention of 0 disables purging.

The retention window lives on the processor and is read only inside the
command handler, so changing it never makes an old log replay differently — the
already-applied `HistoryPurged` events are facts; only future sweeps see the new
window.

### Consequences

- **Positive:** History is bounded by age with no full scans; the delete path is
  a normal, replayable event; variables of purged instances are finally
  reclaimed (closing the ADR-0017 leak for expired instances). No new scheduler.
- **Negative / trade-offs accepted:** Bounds by age, not by storage size — a
  burst of completions within the window is retained in full. Purge is driven by
  a ticker, so an instance can outlive its window by up to one sweep interval.
  Timeliness on a quiescent server depends on the ticker, not on reaching a size.
- **Follow-ups / risks to watch:** A count cap could be layered on later for a
  hard storage bound. Variables of *retained* finished instances are still not
  cleaned until purge — acceptable, they now have a bounded lifetime.

## Pros and cons of the options

### Option 1 — count cap
- Good: fully deterministic, can live entirely in `applyToState`; hard storage bound.
- Bad: does not answer "keep the last N days"; evicts recent history under a burst.

### Option 2 — age-based TTL (chosen)
- Good: matches "how long is history kept?"; reuses the timer-index pattern; clean event-driven deletes.
- Bad: needs a clock read (kept out of `applyToState`) and a periodic trigger; no hard size bound.

### Option 3 — both
- Good: bounds by age and size.
- Bad: most machinery; combines the TTL sweep with count eviction for little immediate gain.

## Links

- builds on ADR-0017 (retain finished process instances in a history index)
- relates to ADR-0001 (event sourcing and log-structured state)
- follows the timer due-date index pattern (data-model.md)
