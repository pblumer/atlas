# ADR-DRAFT: The diagram's history is a query too — listing the instances that left an element

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-28
- **Deciders:** Atlas engine team
- **Open question:** The cost at production scale is estimated, not measured: roughly one
  valueless key per (instance, element) pair a history holds — about as many keys again as
  `elVisit` — and a one-time backfill over the visit and termination families at the first
  open. How large that is on disk, and how long the backfill takes, on a store holding a few
  million finished instances has not been observed.
- **Question checked:** 2026-09

## Context and problem statement

ADR-0261 made the diagram the query for **live** tokens: click an element, and the panel
lists the instances whose token is sitting on it. The other two numbers on a shape stayed
dead ends. A service task in production read **1 976 430** in gray — completed here and
moved on — and nothing else; clicking it answered *"no instance is sitting on
Service-Ereignis melden right now"*. True, and no help to an operator whose next question
was *which* instances took that branch, or which were cancelled at the task.

The legend under the diagram already has three switches, one per count (gray, amber,
green), and an operator asked that the click list whatever the switched-on counts stand
for. The question is how to answer it. The per-instance history counters of ADR-0022 and
ADR-0249 hold every fact needed — `elVisit` and `elTerm`, keyed
`<procDefKey>:<piKey>:<elementId>` — but in the wrong direction. "Which instances visited
element X" over that key is a walk of the version's whole history, re-run every 1.5
seconds while the view is open; on the version above that is some ten million keys a poll.
ADR-0261 rejected the same shape (its option 2) for the live filter, for the same reason.

## Decision drivers

- **The answer must cost the answer** (ADR-0261's first driver, unchanged): a page of
  instances must cost that page, on a poll, at millions of instances.
- **Derived state is event-driven**: anything added is written from the event that causes
  it, in `applyToState`, so replay rebuilds it (I4/I6).
- **No behaviour may depend on the upgrade**: a store that predates the index must not
  answer "nobody completed this task" for a task two million tokens went through.
- **The live answer stays first**: "who is stuck here" is the question the click was built
  for, and a long history must not bury it.
- **Retention keeps its promise**: an instance history retention purges must not remain
  listed as having completed a task.

## Considered options

1. **Scan the per-instance counters** for the element on each request.
2. **One element-major index per departure (chosen)**: `piDoneAtEl` and `piCancAtEl`, keyed
   `<procDefKey>:<elementId>:<piKey>`, written when a token completes or is terminated.
3. **One index of visits** (`<procDefKey>:<elementId>:<piKey>`, written on activation), with
   "completed" and "cancelled" derived per row from the per-instance counters.
4. **Answer the history from the exported event log** (ADR-0114) only.

## Decision outcome

Chosen: **option 2**, plus `?at=live|passed|cancelled` on the element filter and a panel
that lists one section per switched-on legend count.

- **Written from `applyToState`** on an element instance's `IntentCompleted` (into the
  completed index) and `IntentTerminated` (into the cancelled index) — the same branch
  that already writes the termination counter and the lifecycle trail. Replay rebuilds
  both.
- **No element-instance key in the entry**, unlike `piByEl`: the fact is "this instance
  left here this way", so a loop leaving an element five times rewrites one key. The index
  sizes with (instance, element) pairs, not with iterations.
- **Running and finished instances in one order.** A departure says nothing about whether
  the instance has ended since, so the listing reads each record from whichever family
  holds it, and `?at=passed|cancelled` takes no `?state=` — filtering one index by half
  would read past the other half. Its cursor is a bare instance key.
- **Purged with the instance.** The entries are keyed by element, so no prefix over the
  instance reaches them; `PurgeInstanceHistory` names them from the elements the instance
  is on record as having touched — its visit counters and its lifecycle trail (the trail
  covers a token that completed after a migration, whose visit was counted under the
  version it started on).
- **Seeded once at open** (`backfillDepartureIndexIfNeeded`, marker
  `element_departure_index_v1`) from the counters: any termination count marks a
  cancellation, and *visits − cancelled − live tokens > 0* marks a completion — exactly the
  subtraction the gray badge makes. Because this one is sized by history rather than by
  the live population, it commits in chunks, with the marker in the last, synced batch; a
  crash in between leaves a partial index the next open rewrites.
- **The panel lists sections, not a merged list**: *sitting here now*, *cancelled here*,
  *completed here and moved on*, in that fixed order, one per switched-on legend count and
  each paged on its own cursor. The switches keep being remembered per browser; one thrown
  while an element is filtered re-reads the panel at once. With all three off the panel
  says so instead of listing nothing.

The MCP tool `atlas_list_instances` gains `at`, and refuses it without `element` at the
tool boundary, as it already refuses `element` without `process`.

### Consequences

- **Positive:** All three numbers on a shape now lead to their instances with one click,
  at a cost of the page shown — the same property ADR-0261 bought for the green one.
- **Positive:** An agent over MCP can ask "which instances were cancelled at this task"
  from the element's own index instead of sieving pages.
- **Negative / trade-offs accepted:** The completed index holds about as many keys as
  `elVisit`, and grows with history just as unboundedly unless history retention is
  configured (ADR-0146). One more valueless `Set` per element completion and termination
  on the processor path, built like the `piByEl` entry beside it.
- **Negative / trade-offs accepted:** The section counts and the badge are different
  numbers on purpose. The badge counts **tokens** (a loop's several times) from
  aggregates that survive retention; a section lists **instances**, once each, and only
  those the server still holds. The section titles and the heading dots (rather than
  numbered badges) are there so the two are not read as one.
- **Negative / trade-offs accepted:** A token migrated mid-task (ADR-0162) is handled
  differently by the backfill and by live recording: the backfill, reading the badge's
  subtraction, counts it as having moved on from the source version; live recording writes
  its departure under the version it actually left from. The badge makes the first
  reading too. Pre-migration entries of an instance later purged under its target
  version are not reached by the purge — as its `elVisit` rows are not today — and the
  listing skips an entry whose instance is gone.
- **Follow-ups / risks to watch:** The open question above: measure the footprint and the
  backfill time on a large store. A retention/compaction policy for the per-element
  history families as a whole is still the ADR-0022 follow-up it was.

## Pros and cons of the options

### Option 1 — scan the per-instance counters
- Good: no new state, no backfill, no purge path.
- Bad: O(history of the version) per request, on a 1.5-second poll. Worst for exactly the
  rare element (a *Löschfrist* branch taken 2 741 times out of 2.4 million) an operator is
  most likely to ask about: finding fifty rows, or establishing there are no more, reads
  the whole history. This is ADR-0261's rejected option 2 again.

### Option 2 — one element-major index per departure (chosen)
- Good: each section is one prefix scan, newest first, cursor paged like every other
  instance listing; the backfill can derive both indexes from state already held.
- Good: the facts are written where they become true — at completion and termination —
  so the live path is exact, and only the one-time backfill approximates (the badge's own
  approximation).
- Bad: two more derived families, a history-sized backfill, and a purge path that has to
  name entries one by one.

### Option 3 — one visit index, departure derived per row
- Good: one family instead of two.
- Bad: the page is no longer the cost. A section of "completed here" has to skip every
  visitor that was cancelled or is still sitting there, reading two counters per skipped
  row — and on an event-gateway branch that lost most races, most visitors are skipped.
  The paging property the index exists for would hold only for some elements.

### Option 4 — the exported event log only
- Good: no state in the engine at all; the log holds everything.
- Bad: the exporter is optional and often not configured; where it is, it is another
  service's latency and availability under a view that polls. It stays what ADR-0247 made
  it: the fallback for instances retention has removed, not the primary answer.

## Links

- extends ADR-0261 (instances on an element) from the live count to the history counts
- builds on ADR-0022 (element visit history) and ADR-0249 (termination counters), which it
  reads for the backfill and whose subtraction it reproduces
- relates to ADR-0146 (history retention), whose purge now also drops these entries
- relates to ADR-0162 (instance migration) for the attribution caveat above
- relates to ADR-0114 / ADR-0247 (event log export and archive search)
