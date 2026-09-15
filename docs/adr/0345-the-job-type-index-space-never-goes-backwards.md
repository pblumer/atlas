# ADR-0345: The job-type index space never goes backwards

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers

## Context and problem statement

The engine-wide job-type table maps a model-authored job type (a
`<zeebe:taskDefinition type>`) to the integer index a job on disk carries. It exists
because the compiler interns strings per compiled process
([ADR-0004](0004-compile-bpmn-to-indexed-graph.md)) while the activatable-job index
is engine-wide, and [ADR-0007](0007-job-worker-protocol.md) names it as the
correctness prerequisite for the type-keyed pull: a worker asks for a *name*, the
server resolves it to an *index*, and scans the jobs parked under it.

The package has stated the rule that makes this safe since it was written:

> An index, once issued, is permanent: jobs already on disk carry it, so the registry
> never recycles one, **not even after a record is removed by hand**.

It was not enforced. `next` was rebuilt at every open as `max(index over the entries
this build could still use) + 1` — a derivation from survivors, not a memory of what
was issued. Two things lower it, and both put an index that parked jobs still carry
back into circulation.

**One: an entry file that is gone.** Removed by hand, lost to a partial restore, or
dropped by any future route that prunes an unused job type. Measured:

```
charge-card = 1000, send-email = 1001
after removing send-email's record and reopening, ship-parcel = 1001 — REUSED: true
index 1001 now means "ship-parcel"
```

**Two: an entry the load refuses, which needs no editing at all.** `remember` drops a
stored entry whose *name* a later build has turned into a built-in — correctly, since
the constant owns that name's index now — and the refusal also dropped the entry's
claim on its index. A model may author any task type, `io.atlas.` names included;
nothing rejects one. So an ordinary upgrade is enough. Measured, with a store holding
a now-built-in name at index 1005:

```
Dropped() = []                     ← the load does not even report this one
Intern("e-task") = 1005            ← reissued, to a different name
index 1005 now means "e-task"
```

This is ADR-0007's failure reached from the other direction. That record built the
table so that a worker subscribing by job type is not handed another type's work;
here the table itself hands it over, because a parked job carries the number and not
the name.

### What this corrects in ADR-0339

[ADR-0339](0339-the-definition-key-space-never-goes-backwards.md) gave the definition
key space a durable floor and named this table as a follow-up, with the reasoning:

> It holds there only because no route deletes a job type — the identical assumption,
> one route away from the identical bug. It is not fixed here because it is not
> reachable.

The first half is right and the second is wrong. The route-free upgrade path above
was found by measuring rather than by reasoning about routes, and it does not need a
delete at all. The follow-up understated what it was pointing at.

## Decision drivers

- **An index is spent when it is issued, not while a record holds it.** The jobs that
  carry it outlive the record, which is the whole reason the rule was written down.
- **The rule is already stated; enforcing it is not a new policy.** This makes an
  existing sentence in the package doc true.
- **A derivation cannot express "ever".** Any counter rebuilt from what survives is a
  counter that falls whenever something does not survive, and the load already has
  two ways not to keep an entry.
- **Gaps are free, reuse is not.** Indices are opaque: compared, never counted or
  iterated, and the dynamic half starts at 1000 in an `int32`.
- **An installation upgrading must be covered on the boot it upgrades**, not on its
  next deploy — its entries still say how far it got, and only at that moment.

## Considered options

1. **A durable high-water mark**, raised before the index it covers is issued, plus
   counting every loaded entry toward the counter whether or not the load keeps it.
2. **Count every loaded entry only** — no durable mark. Fixes the refusal path and
   leaves the missing-record path.
3. **Refuse to open a table whose entries do not account for the counter** — treat a
   gap as corruption and make an operator repair it.
4. **Derive the counter from the jobs on disk** rather than from the table, since
   those are what actually carry indices.
5. **Extract the mechanism from ADR-0339's key space** into one shared
   "durable monotonic counter" and use it in both places.
6. **Do nothing**, and rely on nothing deleting a job type.

## Decision outcome

Chosen option: **1 — a durable high-water mark, raised before use, and a load that
counts every entry it reads.**

A one-record mark (`jobtypes/highest.json`) holds the largest index this installation
has ever issued to a model-authored job type. `Intern` raises it and *then* writes the
entry. The load takes `next` to be the maximum of the mark, every index the store
showed it, and the fixed dynamic floor — and writes the mark back when the entries
alone established a higher one, which is the upgrade path.

### Why the order is the design

The mark is written **first**, before the entry claims the index.

- Crash between the mark and the entry: an index nothing claims. A gap, which costs
  nothing.
- Crash the other way round — entry first, mark second: an index an entry claims and
  the mark has never heard of. Lose that entry and the index comes back. That is the
  defect, reintroduced by ordering alone.

Same shape as **durable before visible (I2)**: the fact that constrains the future
goes to disk before the thing it constrains. It is tested as the outcome it buys
rather than as an assertion about write order — a directory standing where the entry's
file must go makes the second write fail, and the index must still be gone.

### Why the load counts entries it refuses

The mark alone would cover this, for a store the mark has always been on. It does not
cover the store of an installation that has been issuing indices since before the mark
existed: there, the entries are the only record of how far the table got, and an entry
the load refuses is one it would otherwise read that claim out of. Counting every
index the store shows is one line and removes a second, independent way for the
counter to fall.

Note what it does *not* change: the entry is still refused. A reserved name keeps its
constant. Only its claim on the index survives it.

### Why a write on the open path

`NewRegistry` writes the mark when the entries establish a higher one than is stored.
A write on a read path is worth stating rather than hiding, so: it happens only when
the mark would rise, so it is idempotent and silent on every boot after the first, and
it cannot rise above what the entries had already forced the counter to for that boot.
It therefore makes a stored index permanent one deploy sooner than `Intern` would
have; it does not make one reachable that was not. The alternative — waiting for the
next `Intern` — loses exactly the window an upgraded installation is in, which is the
window this is for. The offline check (`atlas check-job-types`) does not build a
registry and stays read-only.

### Why the mark lives beside the entries

One directory, `jobtypes/`. The mark is part of the table, not a second thing to back
up, restore and classify — the store inventory
([ADR-0282](0282-store-registry.md)) still has one line for it and it still means the
same thing. They share the directory safely because entry files are named by
hex-encoding the job type and the entry store ignores every stem that is not hex,
which is what `sidecar.Names` exists for. That is a coupling between two stores, so it
is pinned by a test rather than left to hold by accident.

### Why not the others

**Counting entries only** (2) is half the fix and the cheaper half. It would leave the
measured missing-record case untouched, and that case is the one the package doc names
out loud.

**Refusing to open** (3) takes an instance down over a condition whose repair has not
been decided, which is the judgement `collision.go` already made for the neighbouring
case and made correctly. It also cannot distinguish a lost entry from a gap the design
now creates deliberately.

**Deriving from the jobs on disk** (4) is the most direct reading of "what is actually
at risk" and is wrong in both directions. A job type with no jobs parked right now
would be forgotten, and its index reissued while a *future* job of the old type is
still to come from a running instance; and it would tie a design-time table to a scan
of runtime state at every boot, which is a cost that grows with the backlog.

**Extracting a shared abstraction** (5) is the tempting one, and is deferred rather
than rejected. The key space's version is one week old and its invariant is newly
proven; generalising it from inside a slice about job types would put that at risk to
save perhaps thirty lines, and would have to reconcile two genuinely different shapes
— `uint64` keys reserved in spans of n at deploy time versus `int32` indices issued
one at a time, with a reserved range and a fixed floor underneath. The duplication is
visible and small, and both files point at each other. If a third counter appears, the
shape will be clear enough to extract with evidence.

**Doing nothing** (6) was ADR-0339's answer, on the grounds that no route reaches it.
The upgrade path does not need a route.

### Consequences

- **Positive:** The sentence the package has always carried is now true. An index is
  never issued twice, whatever happens to the entry that held it.
- **Positive:** The upgrade path is closed on the boot that upgrades, not on the next
  deploy.
- **Positive:** A future route that prunes an unused job type is safe to write. It was
  not before.
- **Negative / trade-offs accepted:** One small durable write per *new* job type ever
  interned, and one at open time when the mark rises. Neither is on the engine's hot
  path (I1); `Intern` runs at deploy and at reload.
- **Negative:** Indices can now have gaps — after a crash mid-`Intern`, or a failed
  entry write. Nothing reads an index as a count.
- **Negative:** A corrupt entry carrying an absurdly high index now poisons the
  counter permanently, where removing the file used to recover it. The exposure is not
  new — that entry already sets the counter for the boot, and the first `Intern`
  would persist it — but the window in which removing the file still helps is now the
  moments before the first open.
- **Follow-ups / risks to watch:** The load's *other* refusal is still silent.
  `Dropped()` reports an entry whose stored index is inside the reserved range, and
  reports nothing for an entry whose stored *name* a build has taken over — the case
  measured above. Its parked jobs are stranded under an index the table no longer
  names, which the high-water mark keeps from being handed to anyone else but does not
  make visible. That is a reporting change across a log line, an HTTP field, the
  Workers view and `atlas check-job-types`, and it is a separate decision about what
  an operator should be told to do about it.

## Pros and cons of the options

### Option 1 — a durable mark, raised before use, plus a load that counts everything *(chosen)*
- Good: no window, one place, and both ways the counter fell are closed.
- Good: the failure mode of a crash is a gap, which is free.
- Bad: a durable write per new job type, and a second store shape sharing a directory.
- Bad: makes a corrupt high index permanent slightly sooner.

### Option 2 — count every loaded entry, no durable mark
- Good: one line, no new file, no new write.
- Bad: leaves the missing-record case, which is the one the package doc names.

### Option 3 — refuse to open a table with an unaccounted-for counter
- Good: nothing silent.
- Bad: takes an instance down over an undecided repair.
- Bad: cannot tell a lost entry from a deliberate gap.

### Option 4 — derive the counter from the jobs on disk
- Good: reads the thing that is actually at risk.
- Bad: forgets a type with no jobs parked right now, whose running instances will
  still create some.
- Bad: a runtime scan at every boot, growing with the backlog.

### Option 5 — extract a shared counter with the key space
- Good: one mechanism, one place to get right.
- Bad: refactors a one-week-old invariant from inside an unrelated slice.
- Bad: the two shapes differ enough (span reservation vs. single issue, a reserved
  range and a floor) that the abstraction would be mostly parameters.

### Option 6 — do nothing
- Good: no change.
- Bad: the upgrade path reaches it without any route, which is what this record
  measured.

## Links

- extends [ADR-0339](0339-the-definition-key-space-never-goes-backwards.md) — the same
  ordering argument for the definition key space, and the follow-up this corrects
- relates to [ADR-0007](0007-job-worker-protocol.md) — the type-keyed pull this table
  is the correctness prerequisite for
- relates to [ADR-0004](0004-compile-bpmn-to-indexed-graph.md) — the per-process
  interning that makes an engine-wide table necessary
- relates to [ADR-0157](0157-worker-processes-supervision-and-console.md) — the worker
  processes that subscribe by job type
- relates to [ADR-0282](0282-store-registry.md) — the inventory the table is
  classified in, unchanged by this
