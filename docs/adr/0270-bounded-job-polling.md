# ADR-0270: A poll costs a page, not a backlog

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

Two paths read the activatable-job index, and both read all of it.

`POST /api/v1/jobs/activate` is a worker's heartbeat. It asks for `maxJobs` (one,
by default) and collected them like this:

```go
s.store.ActivatableJobs(jobType, func(k uint64) error {
    if len(keys) < want { keys = append(keys, k) }
    return nil
})
```

The callback keeps returning nil after it is full, and `ActivatableJobs` walks the
whole slice for that type. So a worker asking for one job pays for every job
waiting — the cost of a heartbeat grows with exactly the backlog the heartbeat
exists to drain, and it grows for every worker on every poll.

`job.Runner.Claim` is the in-process equivalent and is worse in kind: it collects
*every* activatable job of *every* type it serves and then reads each job record —
on the single writer (invariant I3). A backlog of a hundred thousand holds the
writer for a hundred thousand point reads before any handler runs, and every other
instance, timer and readiness probe waits behind a round that was never going to
finish all of it anyway.

Neither is a missing capability. `scanRangeWith` already ends the scan on a non-nil
error from the callback, and the API layer already has the idiom
(`errListTruncated` + `unlessTruncated`) for a bounded list. The polling paths
simply did not use it.

## Decision drivers

- **A poll's cost must be its page.** The index is ordered; stopping at the nth
  entry should visit n entries, whatever is behind them.
- **Nothing may be lost or leased twice.** Stopping early is only safe if the work
  behind the stop is still there for the next round.
- **Fairness across job types.** A type flooded by its neighbour must still be
  claimed.
- **No new index and no new query.** The mechanism exists; the fix is to use it.

## Considered options

1. Use the callback's early-stop contract at both call sites, and cap the
   in-process claim per round.
2. Add a `limit` parameter to `ActivatableJobs` (and a second query for the
   unbounded case).
3. Leave the scans and cache the index in memory.

## Decision outcome

Chosen option: **option 1.**

**The worker pull stops at the page.** The callback returns `errListTruncated` once
it has `want` keys, and the result goes through `unlessTruncated` — the same pair
the capped list handlers already use.

**The in-process claim takes a round's worth.** `Runner.Claim` collects at most
`DefaultClaimBatch` (256) jobs, settable with `SetClaimBatch`. Every caller —
`Runner.Drive` and the server's `drive` — already loops until a claim comes back
empty, so the cap costs a round, not a job. What it buys is that the writer is
released between rounds, so a burst no longer holds it end to end.

**Each type gets an equal share of the round.** Ranging a map is randomly ordered,
so leaving the split to chance would be fair on average — and "on average" is not
what a job type flooded by its neighbour needs. The share is
`batch / number of served types`, at least one.

Option 2 was rejected because it adds API surface for something the callback
contract already expresses, and because it would leave the *next* caller free to
make the same mistake — the doc comment on `ActivatableJobs` now says what the
contract is, which is the durable half of this fix. Option 3 was rejected outright:
a cached index is a second source of truth for what is activatable, and the failure
mode is handing a job to two workers.

### Consequences

- **Positive:** a worker's poll visits as many index entries as it asked for jobs,
  measured at 1, 100 and 10,000 waiting jobs and true by construction beyond that.
- **Positive:** the single writer is released between claim rounds, so an in-process
  burst no longer blocks unrelated instances, timers and probes for its duration.
- **Negative / trade-offs accepted:** peak concurrency for a large burst is now
  bounded by the round rather than by the backlog — 10,000 parked jobs are worked
  256 at a time instead of all at once. That is the intended trade: the handlers'
  concurrency was already capped (`Concurrency`), so the uncapped claim was only
  ever building a bigger queue in memory.
- **Negative:** an equal share per type wastes the share a quiet type does not use,
  so a round can come back part-full while one type still has thousands waiting. The
  next round takes them; a scheme that redistributed the unused share would claim
  more per round and lose the guarantee that the quiet type is never starved.
- **Follow-ups / risks to watch:** the plan also asks for queue **age** to be
  observed rather than only queue length. That is a metrics question and is
  unaddressed here.

## Pros and cons of the options

### Option 1 — use the early-stop contract, cap the claim (chosen)
- Good: no new query, no new index; bounded by construction; the contract is now
  written down where the next caller will read it.
- Bad: the caps are numbers, and a number is a guess.

### Option 2 — a limit parameter on the query
- Good: impossible to misuse.
- Bad: two queries where one has always been enough, and it does not stop the same
  mistake being made against the next scan.

### Option 3 — cache the activatable index
- Good: the cheapest possible poll.
- Bad: a second source of truth for what is leasable; the failure mode is a job
  handed to two workers.

## Links

- relates to [ADR-0157](0157-worker-processes-supervision-and-console.md) (the in-process runner whose
  claim this bounds)
- relates to [ADR-0007](0007-job-worker-protocol.md) (the pull protocol and its
  leases)
- relates to [ADR-0002](0002-single-writer-partition-model.md) (the writer a claim
  round holds)
- reported as F15 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
