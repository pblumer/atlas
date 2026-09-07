# ADR-DRAFT: The runtime counts leave the run loop, and take the write paths with them

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-draft-login-off-the-run-loop took the login off the run loop after nobody could
sign in to a server holding ~50.000 active instances. It left the obvious question
open: the login reads no engine state and was merely *queued*, so what was it queued
behind?

`/stats`. The endpoint looks like an aggregate and reads like one, and it is neither:

```go
func (q queries) ActiveProcessInstanceCount() (int, error) {
	return q.countPrefix([]byte{byte(cfProcessInstance)})
}
```

All three counts are `countPrefix` — a full walk of a column family. At the observed
population one `/stats` call walked ~50.000 instance keys plus ~201.000 element-instance
keys: a quarter of a million keys, dispatched onto the single writer.

This is worth stating plainly because the opposite is easy to assume, and was assumed
during the incident: ADR-0080 *did* introduce maintained counters, but for the
per-definition sums the Prometheus path uses. `state.Store`'s own comment draws the
line — the scan is "fine for a request, wrong for something a Prometheus scrape takes
every fifteen seconds". Nobody had asked the third question: what if a *request* takes
it on the loop.

The answer is worse than one slow endpoint, because `/stats` is not the main caller.
Of the eight callers of `readStats`, **seven are write paths** that report the counts
back in their response — starting an instance, publishing a message, cancelling or
terminating a batch by filter, a CSV upload — and each took a run-loop turn of its own
purely for that read-back:

```go
if driveNeeded {
	if runErr = s.drive(); runErr == nil {
		s.do(func() { stats, statErr = s.readStats() })   // a quarter-million-key scan
	}
}
```

So every instance start paid a full population scan on the single writer. With load
generators starting and finishing instances continuously, the loop was not
*occasionally* busy — it was executing a population-sized scan per write, indefinitely.
That is the mechanism behind the outage, and it explains why the parked instances
looked like the cause while doing nothing themselves: they were not the load, they were
the *size* that made each write's read-back expensive.

`/incidents` is the same defect in its milder, ADR-0239-shaped form: it had two rows to
return and still did its whole walk, its per-instance lookups and its deployment-map
reads inside `do`.

## Decision drivers

- **ADR-0239's rule, applied where it was missed.** No read whose cost grows with the
  instance population may hold the single writer. A read-back inside a write path is
  still a read.
- **A write must not cost more than the write.** Paying O(instances + tokens) to report
  a count alongside an O(1) instance start inverts the cost of the operation.
- **Do not quietly change what a number means.** `/stats` is the authoritative count;
  swapping it for a maintained sum is a different answer, not a faster one.
- **An operator reaches for `/incidents` exactly when the engine is busy.** Its
  availability must not depend on the condition it is used to diagnose.

## Considered options

1. **Make `/stats` O(1) with maintained counters** in the ADR-0080 style and leave it on
   the loop.
2. **Move `readStats` and the incident page off the loop through `readOffLoop`
   (chosen).**
3. **Stop reporting counts in write responses**, so only the endpoint pays the scan.

## Decision outcome

Chosen: **option 2.**

- **`readStats` takes a `*state.ReadView` instead of the live store, and the signature
  is the enforcement.** There is no longer a way to spell the on-loop version: a caller
  must obtain a view, which means going through `readOffLoop`. The three queries are
  unchanged, so the answer is unchanged.
- **`Server.statsOffLoop` is the one way to ask.** All eight call sites use it, so the
  seven write paths were fixed by the same change as the endpoint rather than left for
  a later pass. Each still takes one loop turn — the bounded one `readOffLoop`
  describes, taking the view and copying the deployment metadata — and does the
  counting with the loop free.
- **`handleListIncidents` runs inside `readOffLoop`**, reading instances from the view
  and definitions from the copied `defIndex`. Its connector resolver reads a durable
  sidecar, which ADR-draft-login-off-the-run-loop established is safe off the loop.
- **Both endpoints now answer 503 while the loop is closing** instead of a 200 the
  caller cannot distinguish from a true empty answer.

### Consequences

- **Positive:** No write pays a population-sized scan on the single writer any more.
  This is the change with the largest effect on the incident that prompted it: the
  engine's queue is no longer lengthened by its own throughput.
- **Positive:** Both reads are now snapshots, so a page cannot mix an instance counted
  before a write with a token counted after it, and an incident resolved mid-walk can
  no longer appear half-described.
- **Negative / trade-offs accepted:** The seven write paths now surface a failed
  read-back where the old code silently reported zeros. That is the intended
  correction — `do()` skipping its closure during shutdown produced
  `{"activeProcessInstances":0,…}`, which reads as "the engine is empty" — but it is a
  behaviour change: an operation whose write succeeded can now report an error raised
  by the count that followed it.
- **Negative:** `/stats` is no faster. It still walks the population for its caller; it
  simply no longer walks it for everybody else. Making it *fast* means maintained
  counters and the different meaning option 1 describes, which this record deliberately
  does not adopt.
- **Follow-ups / risks to watch:** `readOffLoop` copies the deployment metadata on
  every call, and `statsOffLoop` does not use it. That copy is O(deployments) — trivial
  beside the scan it replaced, but it is now paid on every write, so it is the next
  thing to notice if a server carries a very large number of definitions; ADR-0239
  already names the atomically-swapped snapshot that would remove it.
  Roughly 220 `s.do` dispatch sites remain in `api/`.
  The engine's timer tick still runs through `s.do` every second, so engine work and API
  reads remain co-tenants of one queue rather than having lanes of their own; that is
  the structural question neither this record nor ADR-0239 answers.

## Pros and cons of the options

### Option 1 — maintained counters, still on the loop
- Good: the cheapest possible answer, and the loop turn becomes genuinely O(1).
- Bad: it answers a different question. `ActiveProcessInstanceCount` is authoritative;
  a sum of per-definition counters is a sum of counters, and ADR-0080's own note that
  un-compacted merge operands make it O(recent writes) means it is not reliably O(1)
  either.
- Bad: it cannot cover incidents at all. An incident leaves state two ways — resolved,
  and dropped with the element instance it sits on — so a maintained count drifts while
  a scan cannot, which is exactly why `IncidentCount` scans today.
- Bad: it leaves `/incidents` untouched, and that endpoint's problem was never cost.

### Option 2 — read off the loop
- Good: fixes the endpoint and the seven write paths with one change, because it fixes
  the function they share.
- Good: reuses the mechanism, the tests and the reasoning ADR-0239 already established;
  nothing new is invented.
- Good: the compiler enforces it — a `*state.ReadView` cannot be produced on the live
  store.
- Bad: the caller still waits for the walk, so a `/stats` on a huge store is still slow
  to answer.
- Bad: a `ReadView` pins the Pebble state it was taken from, so it holds back compaction
  while open; the views here are per-request and closed by `defer`.

### Option 3 — drop the counts from write responses
- Good: removes the work rather than moving it, and would be the cheapest of all.
- Bad: the counts are what the UI redraws from after an operation, so this trades a
  server problem for a client one — the client would poll `/stats` instead, and pay the
  same scan on a timer.

## Links

- follows ADR-draft-login-off-the-run-loop — the same incident, the other half; it also
  established that a durable sidecar may be read off the loop
- applies ADR-0239 — read-only queries off the run loop, on a consistent view
- relates to ADR-0080 — the maintained counters, and the line between them and the
  authoritative scan
- relates to [ADR-0061](0061-incident-model.md) — why an incident count is scanned rather than maintained
- constrained by [ADR-0002](0002-single-writer-partition-model.md) (I3) — writes stay on the loop
