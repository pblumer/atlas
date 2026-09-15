# ADR-0339: The definition key space never goes backwards

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers

## Context and problem statement

Every process definition and every decision deployment is issued a key from one
monotonic counter, `Server.nextKey`. That key is not a label — it is the identity a
great deal of durable state is filed under:

| where | keyed by |
|---|---|
| `piDoneByDef` | completed instances of a definition |
| `defDone` / `defInst` | finished and live instance counts (ADR-0083) |
| `elVisAgg`, `elTermAgg` | per-element visit and termination aggregates (ADR-0080) |
| `defAct` | when the definition last did anything |
| `msgFlow` | message-flow history by receiver |
| a release manifest | the member's `key` (ADR-0128) |
| a decision deployment's pin | the key a latest-bound reference resolved to (ADR-0327) |

The counter is rebuilt at startup as **`max(key over surviving records) + 1`**. That
is correct exactly as long as no record is ever removed. `DELETE
/api/v1/processes/{key}` has existed since [ADR-0019](0019-durable-deployments.md),
and [ADR-0336](0336-cleaning-up-the-decision-store.md) has just added the decision
counterpart — so it has not been correct for a long time, and is now reachable from
two routes.

**Measured, not argued.** Deploy a process, run one instance to completion, delete
the definition, restart, deploy a *different* process:

```
first definition key = 1
alpha runtime before delete = {"instances":0,"finished":1,"elements":[{"elementId":"s","visits":1}, …]}
second definition key = 1   ← the same key
beta runtime after reuse   = {"instances":0,"finished":1,"elements":[{"elementId":"s","visits":1}, …]}
```

`beta` had just been deployed and had never run. It reported a finished instance and
a visit on an element no token had ever reached, because `Undeploy` drops in-memory
routing and the state store keeps every row filed under key 1.

The engine even says so, in a comment that was an assumption dressed as a fact:

> Drop any deactivation for this key so a future definition reusing it *(there is
> none today — keys are monotonic)* never inherits a stale inactive flag.

## Decision drivers

- **A key is spent when it is issued, not while a record holds it.** Everything
  above outlives the record.
- **The principle is already in the tree.** The job-type table states it outright —
  "an index, once issued, is permanent: jobs already on disk carry it, so the
  registry never recycles one, not even after a record is removed by hand" — and
  Atlas failed to apply it to the key space that carries more.
- **One answer for both kinds.** Process definitions and decision deployments draw
  from one counter; a fix for one that left the other would be a worse bug, because
  the two would then collide.
- **Gaps are free, reuse is not.** Keys are opaque and only their order is read
  (`s.order`, the stores' sort, the registry's rebuild). A missing number costs
  nothing; a repeated one costs history.
- **An installation that predates the fix must not be broken by it.** The first boot
  after an upgrade finds no floor, and its keys must still be safe.

## Considered options

1. **A durable floor**: persist the highest key ever issued, raise it *before* the
   keys are used, and start the counter above it.
2. **Raise the floor at delete time** instead: the only way a key is lost is a
   deletion, so bump a mark before removing a record.
3. **Refuse to delete the highest-keyed record.**
4. **Stop deriving the counter from records at all** — make keys non-sequential
   (time-based or random), so they cannot repeat.
5. **Purge the state store for a deleted definition**, so there is no history to
   inherit.

## Decision outcome

Chosen option: **1 — a durable floor, raised before the keys it covers are used.**

A one-record store (`keyspace/`) holds the highest definition key this installation
has ever issued. `Server.reserveKeys(n)` persists the new high-water mark and *then*
returns the first of the n keys; both mint sites — the BPMN deploy and
`deployDecisions` — go through it, and nothing else writes the counter except
recovery. At startup, `nextKey` is `max(floor, max over surviving records) + 1`.

### Why the order is the design

The floor is written **first**, before any record claims a key.

- Crash between the floor write and the record write: a key nobody claims. A gap in
  the numbering, which costs nothing.
- Crash the other way round — record first, floor second: a key a record claimed
  and the floor does not know about. Delete that record later and the key comes
  back. That is the bug, reintroduced by ordering alone.

This is the same shape as **durable before visible (I2)**: the fact that constrains
the future goes to disk before the thing it constrains.

### Why not at delete time (option 2)

It looks minimal — deletion is the only way a key is lost — and it has a window that
cannot be closed. Deploy key 5; crash before the floor is written; restart (counter
comes back as 6 from the record); delete key 5; restart. The floor never learned
about 5, the record is gone, and 5 is issued again. Raising the floor at *mint* time
has no such window, and costs one small write on an operation that already compiles
BPMN and fsyncs a record.

It is also the weaker invariant to maintain: "every future delete path must remember
to raise the floor" is a rule a person can forget, while "keys come from
`reserveKeys`" is one place.

### Why not the other three

**Refusing to delete the highest-keyed record** (3) makes the newest definition
undeletable for a reason an operator cannot see, and does nothing for the case where
*everything* is deleted — which restarts the counter at 1.

**Non-sequential keys** (4) would remove the problem rather than fix it, and take
the key space's ordering with it. Ascending key order is load-bearing in at least
three places: the deployment stores sort by it, `loadDeployments` replays the two
record kinds in one merged key order, and `Registry.UndeployDecision` rebuilds its
"newest provider" pointers by walking the survivors in it (ADR-0336). Keys would also
stop being the small readable numbers every screen and every test shows.

**Purging the state store** (5) is not a fix but a second, larger decision: it would
make deleting a definition destroy its instance history, which is the opposite of
what an audit trail is for. A completed instance is a fact about what the
installation did; the definition record going away does not unmake it. That the
history is currently *orphaned* rather than deleted is right — what was wrong is
that a new definition could adopt it.

### Consequences

- **Positive:** A deleted definition's key is never issued again, so the two delete
  routes are safe to use. The measured corruption does not reproduce.
- **Positive:** One rule for both record kinds, so process and decision deployments
  cannot collide with each other after a delete on either.
- **Positive:** `Undeploy`'s comment becomes true instead of aspirational.
- **Negative / trade-offs accepted:** One small durable write per deploy. It is a
  single reservation per deploy, not per definition, so a collaboration of five
  pools or a publish of five decisions costs one write.
- **Negative:** Keys can now have gaps — after a crash mid-deploy, or a refused
  deploy that had already reserved. Nothing reads a key as a count.
- **Negative:** An installation upgrading finds no floor on its first boot, so
  until its next deploy it is exactly as exposed as it was before. It cannot be
  otherwise: the information was never written down.
- **Follow-ups / risks to watch:** The job-type table derives its counter the same
  way and states the same principle. It holds there only because no route deletes a
  job type — the identical assumption, one route away from the identical bug. It is
  not fixed here because it is not reachable, and fixing an unreachable case inside
  this slice would hide that it is the same case.

  **Corrected by
  [ADR-draft-the-job-type-index-space-never-goes-backwards](draft-the-job-type-index-space-never-goes-backwards.md).**
  "Not reachable" was wrong. Measuring the table rather than reasoning about its
  routes found a second way its counter falls, which needs no delete at all: an entry
  whose *name* a later build turns into a built-in is dropped on load and took its
  claim on its index with it. An ordinary upgrade reaches it.

## Pros and cons of the options

### Option 1 — a durable floor, raised before use *(chosen)*
- Good: no window, one place, both record kinds.
- Good: the failure mode of a crash is a gap, which is free.
- Bad: a write per deploy, and a new store in the inventory.

### Option 2 — raise it at delete time
- Good: writes only on the rare operation.
- Bad: a crash between a deploy and its floor write leaves a key the floor never
  learns about, and the later delete of that record reissues it.
- Bad: it is a rule every future delete path has to remember.

### Option 3 — refuse to delete the highest-keyed record
- Good: no new state.
- Bad: an arbitrary-looking refusal, and no answer at all when every record goes.

### Option 4 — non-sequential keys
- Good: reuse becomes impossible rather than prevented.
- Bad: ascending key order is registration order, and three mechanisms depend on
  that — including the decision registry's rebuild.
- Bad: every key an operator reads gets longer and less comparable.

### Option 5 — purge the state store on delete
- Good: nothing left to inherit.
- Bad: it deletes history that is a record of what the installation actually did,
  as a side effect of removing a definition — a much larger decision, and probably
  the wrong one.

## Links

- extends [ADR-0019](0019-durable-deployments.md) — the deployment records and the delete this makes safe
- extends [ADR-0336](0336-cleaning-up-the-decision-store.md) — the decision delete that made a second route reach it
- relates to [ADR-0007](0007-job-worker-protocol.md) — the job-type table that states this principle and shares the derivation
- corrected by [ADR-draft-the-job-type-index-space-never-goes-backwards](draft-the-job-type-index-space-never-goes-backwards.md) — the follow-up above, and why "not reachable" was wrong
- relates to [ADR-0080](0080-runtime-aggregate-counters.md) — the per-element aggregates a reused key inherited
- relates to [ADR-0083](0083-o1-instance-summary.md) — the finished count a reused key inherited
- relates to [ADR-0128](0128-process-applications.md) — the release manifest that quotes a key
- relates to [ADR-0282](0282-store-registry.md) — the inventory the new store is classified in
