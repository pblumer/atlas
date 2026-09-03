# ADR-DRAFT: Read-only queries run off the run loop, on a consistent view

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Atlas engine team

## Context and problem statement

Every API handler reaches engine state through `Server.do`, which dispatches a
closure onto the single-writer run loop (I3, ADR-0002/0006). The loop runs one
closure at a time, so a closure owns the engine for its whole duration:
while it runs, no command is processed and no other request is served.

That is the right rule for a write. It is the wrong cost for a read whose work
grows with the instance population. ADR-0080 established this for the runtime
views — it replaced their instance scans with maintained counters after a store
with ~529k parked instances made every runtime, stats and list request time out —
but it converted only the views that *could* become counters. The queries that
must still walk rows kept the old shape, and a September 2026 identity-lifecycle
test on a shared server rediscovered the same failure at a far smaller
population: at ~11k active instances the instance search
(`GET /api/v1/instances/search`) timed out, both for a status query and for a
point lookup by variable, while `/stats` and the runtime view answered instantly.
The search walks every active *and* every finished instance, reads each one's
variables, and does it inside `do` — so it did not merely answer slowly, it held
the engine still for the length of the walk.

Two properties are missing, and they are separable:

1. **A long read must not stop the engine.** The user requirement is explicit:
   an operation may take a while, but the UI must stay responsive throughout.
2. **A long read should see one coherent state.** A scan that interleaves with
   writes reads some rows before a change and some after, and reports a state
   that never existed as a whole.

## Decision drivers

- **The run loop is for writes.** Holding the single writer to answer a question
  is what makes one operator's query everybody's outage.
- **Bounded work on the loop, unbounded work off it.** Whatever a query still has
  to touch on the loop must be sized by design-time facts (how many definitions
  are deployed), never by runtime population.
- **Reuse what exists.** ADR-0149/0157 already introduced `state.ReadView`, a
  consistent read-only view over a Pebble snapshot, so an in-process job handler
  moved off the loop could still read coherently. The same mechanism answers this.
- **No second implementation of a query.** A read must not exist twice, once for
  the live store and once for a view; that is how the two drift.
- **Determinism is untouched.** This is a read-side decision. Nothing here changes
  what is written, in what order, or how it replays (I4/I6).

## Considered options

1. **Leave reads on the loop and make each one sublinear.** Extend ADR-0080's
   approach: give every query a maintained index or counter so it never walks.
2. **Run read-only queries off the loop, on a `ReadView` (chosen).** The loop is
   used only to take the view and copy the deployment metadata; the walk happens
   with the loop free.
3. **A second, read-only store replica.** Export state to a separate query store
   (the ADR-0114 OpenSearch exporter is the existing shape of this) and answer
   operator queries from there.

## Decision outcome

Chosen: **option 2, with option 1 applied wherever the answer was already
counted.** They are complements, not alternatives:

- **`state.queries` is the read surface, written once.** Every scan and point read
  moves onto an unexported `queries` type parameterised by a Pebble reader.
  `Store` embeds it (reading the live database) and `ReadView` embeds it (reading
  the snapshot), so both expose the identical set of queries from one
  implementation and no call site changes.
- **`Server.readOffLoop` is the query shape.** It dispatches once to take a
  `ReadView` and copy the deployment metadata a read may look at (`defIndex`),
  then runs the query body with the loop free. What happens on the loop is O(1) in
  the instance population and O(deployments) in the model — design-time size.
- **Loop-owned pointers do not escape.** `defIndex` holds values, not
  `*deployment`: the loop keeps mutating those. The compiled process is the one
  pointer shared, because it is immutable once deployed (ADR-0004).
- **A bulk write splits in two.** Cancel-, terminate- and migrate-by-filter select
  their batch off the loop and perform the writes on it, bounded by the batch
  limit. An instance that finishes in between is the no-op those paths already
  tolerate for an explicitly-keyed batch.
- **Where the answer was already counted, count it.** Deleting a definition,
  cancelling one instance, counting a pool's instances, counting a definition's
  instances for the panorama and the Workers view, and the single-instance token
  overlay all walked a family to find something the engine already maintains
  (ADR-0083 per-definition counters, ADR-0080 per-element counters, the elByProc
  index) or to find one key a point read returns. Those became O(1) or
  O(that instance) rather than moving off the loop.
- **Where only the newest page is shown, scan only that.** The cross-instance data
  view collected every instance and sorted afterwards to display a bounded page;
  it now scans both instance families descending, bounded by the same limit.

### Measured

On a store seeded with 50.000 active plus 50.000 finished instances carrying eight
variables each (800.000 variable records), searching by variable over all of them:

| | |
|---|---|
| the search itself | ~2,0 s (unchanged — it still walks) |
| worst wait for anything else on the run loop, while it runs | **200–479 µs** |
| the same walk dispatched onto the loop, as before | **1,8–1,9 s**, for everything |

The query did not get faster. What changed is that the rest of the engine no
longer waits for it — which is the property the change is for.

### Consequences

- **Positive:** No API read holds the run loop for work proportional to the
  instance population. A slow query is slow for its caller alone. Queries also
  became *consistent* — previously a long scan could interleave with writes.
- **Positive:** Several endpoints stopped scanning altogether and are now O(1).
- **Negative / trade-offs accepted:** A `ReadView` pins the Pebble state it was
  taken from, holding back compaction of everything written while it is open. A
  query that runs for minutes therefore costs disk amplification; the views are
  closed by `defer` and live only for the request.
- **Negative:** A read off the loop can be *stale* by the time its answer is
  rendered — it reports the state as of when it started. For an operator view
  that is what a page already is.
- **Negative:** `readOffLoop` copies the deployment metadata per call. That is
  O(deployments) and small, but it is not free; a server with a very large number
  of definitions should move to an atomically-swapped snapshot of that map.
- **Follow-ups / risks to watch:** The instance search is now non-blocking but
  still O(instances + history) for its caller. Making it *fast* needs an index
  over variable values, maintained on the write path — the natural next record.
  The replay's reverse call-activity link map is the other remaining walk: it
  wants an index from parent element instance to child instance. Both are
  additions to the write path and need their own recovery tests.

## Pros and cons of the options

### Option 1 — sublinear everything, still on the loop
- Good: the fastest possible answer, and no new concurrency surface at all.
- Good: it is the existing doctrine (ADR-0080), and it applies cleanly wherever
  the answer is an aggregate.
- Bad: it does not cover free-form queries. A search over variable *values*
  cannot be a counter; it needs an index, and an index answers only the queries it
  was built for. Something will always have to walk.
- Bad: it leaves the failure mode in place until every last query is converted —
  one unconverted walk is enough to stop the engine.

### Option 2 — off-loop reads on a consistent view
- Good: it fixes the *class* of problem rather than one query at a time. A walk
  that has not been optimised yet is now merely slow.
- Good: it reuses `ReadView`, already in the codebase for the same reason
  (ADR-0149/0157), and Pebble snapshots are cheap to take.
- Bad: it widens what runs concurrently with the loop, so the rule "fn touches
  nothing loop-owned" has to be held by review rather than by the type system.
- Bad: an open view holds back compaction.

### Option 3 — a read replica
- Good: unlimited query power, and zero load on the engine process.
- Good: the exporter exists (ADR-0114), so this is deployable today for sites
  that want it.
- Bad: a second system to run, and eventual consistency in the operator's view.
- Bad: it does not help the built-in Console, which must work with no exporter
  configured.

## Links

- relates to ADR-0080 (sublinear runtime views via maintained counters) — this
  record extends its rule to the queries that must still walk
- relates to ADR-0083 (O(1) instances summary), whose counters several converted
  endpoints now read
- relates to ADR-0149 / ADR-0157 (in-process job handlers off the loop), which
  introduced `state.ReadView`
- relates to ADR-0002 / ADR-0006 (single writer per partition): unchanged — this
  record is about readers, which mutate nothing
- relates to ADR-0114 (OpenSearch event export) as the option-3 escape hatch
