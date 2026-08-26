# ADR-0187: Database change events — captured in the database, read on a worker, deduplicated in the engine

- **Status:** Proposed
- **Date:** 2026-08-25
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0173](0173-generic-sql-connector.md) gave Atlas three *outbound* SQL connectors:
a modeled task runs one statement against SQL Server, MariaDB or PostgreSQL on a
worker that holds the connection string. The complementary direction is not served at
all. An operator wants a row appearing in — or changing in, or vanishing from — a
table to **start** process instances and to **wake** instances waiting at a message
catch. In identity work that is the ordinary trigger: the authoritative list of
employees is a view or a table in an HR database, and "someone was hired" is a row
that was not there yesterday.

Atlas has both halves of the destination already. Message correlation
([ADR-0020](0020-message-correlation.md)) takes one published message and both starts
every matching message-start process ([ADR-0035](0035-message-start-events.md)) and
correlates every waiting subscription. And it has the inbound *shape*:
[ADR-0075](0075-clio-inbound-event-bridge.md) built a bridge that consumes clio events
and republishes each as an Atlas message, with a durable engine-side idempotency mark
so an at-least-once source cannot double-start a process. The generic half of that —
`InboundDeliveryValue{SourceID, SourceSeq}`, `Processor.PublishInbound`, the
`InboundHighWater` guard in `handleMessagePublished`, the high-water fold in
`applyToState` — deliberately names no connector. It was built to be reused.

So the question is not "can Atlas receive an event". It is **where the database gets
read, and what makes the reading trustworthy**, and those two are harder here than
they were for clio for three reasons.

- **The clio bridge holds a credential in the engine.** `resolveInboundSubs` looks the
  subscription's client up in `s.clioRegistry` — the engine's own registry, built from
  the engine's own connector store. Doing the same for PostgreSQL would put a DSN back
  in the engine's address space, which is the single thing ADR-0173 was written to
  prevent. That record's argument was not stylistic: a DSN cannot be split into a
  public address and a secret reference, so "the engine holds the DSN" and "the engine
  holds the database credential" are the same sentence.
- **The obvious workaround inverts the credential rather than removing it.** A trigger
  calling `pg_net.http_post` against `/api/v1/messages` works today, with no change to
  Atlas at all — but it requires an Atlas bearer token *stored in the customer's
  database*. That is the mirror image of the thing ADR-0173 refused, and it is worse in
  one respect: a DSN in a worker's environment reaches one database, while an Atlas
  token reaches every process the engine runs.
- **The dedup mark demands an ordering the obvious capture does not provide.** The
  guard is `if src.SourceSeq <= hw { return }`. It is correct only if deliveries
  arrive in increasing sequence order. An outbox table with a `bigint generated always
  as identity` primary key does **not** give that, because an id is assigned at INSERT
  and the row becomes visible at COMMIT. Measured on PostgreSQL 16.13: two concurrent
  transactions, T1 inserting first (id 1) and committing last, T2 inserting second
  (id 2) and committing first. A poller running between the two commits sees only
  id 2 and advances to 2; when T1 commits, `WHERE id > 2` returns nothing, and row 1
  is never delivered. Not late — never. A design that does not answer this loses
  events silently, which is the worst way to lose them.

## Decision drivers

- **The engine holds no database credential.** ADR-0173's central promise must survive
  the inbound direction, or it was never a promise.
- **No Atlas credential in the customer's database**, for the same reason in reverse.
- **Effectively-once into processes.** Any capture-and-forward is at-least-once; a
  replay must not double-start a message-start process.
- **Reuse correlation.** A row change funnels into the existing `correlateMessage`
  path, not a parallel start/wake mechanism.
- **Invariants.** The database read is a network call: off the run loop (I3), never in
  `applyToState` (I4). The publish is durable before it is acted on (I2), and replay
  rebuilds the mark deterministically (I6).
- **Atlas must not own an operational hazard in a database it does not operate.**
- **No CGO** ([ADR-0010](0010-go-and-no-cgo.md)), which constrains the driver set here
  exactly as it did for the outbound half.

## Considered options

Two independent questions. **Where the reader runs:**

1. **An engine-side bridge**, mirroring `api/inboundBridge`: a ticker in the server
   reads the database directly.
2. **A worker-side reader**, with a new inbound delivery endpoint: the worker holds
   the DSN and posts what it read.
3. **The database delivers**, via a trigger and `pg_net` against the HTTP API.

**What gets read:**

- **A.** **Logical replication** — a slot and a publication, decoded with
  `pglogrepl`.
- **B.** **A trigger-written outbox table**, polled over the `database/sql` pool the
  outbound connector already opens.
- **C.** **`LISTEN`/`NOTIFY`** from a trigger.
- **D.** **A timestamp column**, polled (`WHERE updated_at > cursor`).

## Decision outcome

Chosen: **a worker-side reader** (option 2) reading **a trigger-written outbox table**
(option B), delivering through a new inbound endpoint that carries the source identity
so the engine's existing high-water mark deduplicates it.

### Why the reader is a worker

Option 1 is rejected on ADR-0173's terms, which have not weakened: the engine would
need the DSN, and a DSN is a credential. It is worth being precise that this is the
*only* thing wrong with it — the clio bridge's structure is right, and the worker-side
reader copies it almost line for line. What moves is the process the credential lives
in.

Option 3 is rejected because it does not remove a credential from a place it should
not be, it relocates it: the database ends up holding a bearer token for Atlas. It
also fails on the mechanics. `pg_net` is fire-and-forget — the queue insert is
transactional, so a rollback sends nothing, but there is no retry, no ordering, and no
backpressure. A delivery lost to a network blip is lost, and a delivery retried by a
proxy arrives with no way for the engine to know it is the same one.

So the split of ADR-0168 applies unchanged, read in the other direction:

- **The worker reads.** It resolves the watch's connector *name* against the registry
  it built from its own environment — the same `ATLAS_POSTGRES_<NAME>_DSN` the
  outbound half uses, the same pool, no second configuration.
- **The engine correlates.** It composes the source identity, evaluates the watch's
  correlation-key FEEL over the delivered payload, and calls `PublishInbound`. FEEL
  evaluation stays where every other inbound key is evaluated.

A worker posting to its server is not new — `worker/connectors.go`'s `httpOutbox` does
it for mail previews. What *is* new is a worker doing work no leased job asked for.
Modelling the poll as a job was considered and does not fit: a `JobValue` carries a
`ProcessInstanceKey` and an `ElementInstanceKey`, and there is no instance here. So the
worker grows a second loop beside its job loop, on the same ticker discipline as the
clio bridge — leased, but on a watch rather than on a job, which is the one thing about
this loop a worker already knows how to reason about.

### Why an outbox and not logical replication

Logical replication is the better *capture*, and this record should not pretend
otherwise. It needs no schema change, costs the writer nothing, sees changes a trigger
cannot, and — the point that matters most below — its LSN order **is** commit order,
so the ordering trap simply does not exist.

It is still not what ships first, for one reason that outweighs the rest: **a
replication slot is a resource Atlas would hold inside a database it does not
operate, whose failure mode is that database filling its disk.** A slot that stops
being consumed — Atlas down, a worker misconfigured, a watch disabled and forgotten —
makes PostgreSQL retain WAL indefinitely. The blast radius of an Atlas outage would
extend into the customer's production database, and on a small managed instance
(a Supabase free project, say) that is measured in hours. An outbox that stops being
drained grows a table the operator can see in their own schema and truncate. Those are
not the same class of risk, and choosing the second is not a close call.

Three smaller things point the same way. A slot needs `wal_level = logical`, a role
with `REPLICATION`, a publication, and `REPLICA IDENTITY FULL` on every watched table
if old values are wanted — configuration in a database somebody else administers,
where the outbox needs one table and one trigger the operator can read. It needs
`pglogrepl` and a `pgoutput` decoder, where the outbox needs a `SELECT` over the pool
that already exists. And a trigger is a per-table, auditable opt-in, where a
publication is easy to point at more than was intended.

The honest gaps of trigger capture, which are what a later logical-replication source
would close: `TRUNCATE` does not fire row triggers (a statement-level trigger can
catch it); `session_replication_role = 'replica'` suppresses triggers entirely, which
is the mode logical-replication apply workers run in; DDL is not captured; and the
trigger is a write inside the user's transaction, which a bulk load pays for. `COPY`
does fire row triggers, so the most-feared case is covered.

Options C and D are rejected outright. `NOTIFY` is not durable — a disconnected
listener loses notifications with no record that they existed — so it can be a
latency hint on top of a durable queue but never the queue. A timestamp column cannot
see a delete at all, and cannot distinguish two updates within its resolution.

### The ordering trap, and where the order actually comes from

This is the part a reader should not have to rediscover.

The engine's guard is a scalar high-water mark. It is correct only under
monotonically increasing delivery, and an outbox's insert-time identity does not
provide that — the measurement is in the problem statement above, and it loses a row
permanently. The fix is to stop taking the order from the writer and take it from the
reader: **the delivery sequence is assigned when the row is claimed, not when it is
inserted.**

```sql
update atlas_outbox o set seq = nextval('atlas_outbox_seq')
 where o.id in (select id from atlas_outbox where seq is null
                 order by id limit $1 for update skip locked)
 returning o.*;
```

A row can only be claimed once it is visible, so the sequence it receives is in commit
order by construction — exactly the contract the mark wants. Re-running the scenario
that lost a row: T2 (committed first) is claimed as `seq` 1, T1 (inserted first,
committed later) is claimed as `seq` 2 on the next poll and delivered normally.
Verified on PostgreSQL 16.13. Gaps are fine and will occur — a rolled-back claim burns
sequence values, and `nextval` does not give them back — because the guard compares
against a mark rather than counting.

What is *not* fine is two readers of one watch. `for update skip locked` keeps their
claims from blocking each other, but it does not make them safe: two workers can be
handed disjoint row sets whose sequences interleave, and the one that delivers the
higher sequence first advances the mark past the other's rows, which are then dropped
as duplicates. A scalar high-water mark makes **one reader per watch a requirement,
not a preference** — so the watch is leased, below, rather than merely published.

### What crosses the wire

A watch is a design-time record, like a clio inbound subscription, and holds no
secret:

```
{ id, connector: "supabase", table: "public.personen",
  messageName: "personChanged", correlationKey: "= record.mail",
  enabled, startFromTip }
```

`connector` is a **name**, not a foreign key to a connector-store record — there is no
such record for these kinds, and there deliberately never will be. Which names are
real is the half only a worker knows; the Workers view already subtracts the names
workers report from the names deployed models reference (ADR-0168), and a watch's name
joins that same set.

Two endpoints, both authenticated as a worker:

- `POST /api/v1/inbound/watches/lease` answers with the enabled watches whose connector
  name this worker reports holding, **leased** — one holder at a time, renewed by the
  same call, expiring like a job lease so a worker that dies releases its watches
  rather than stranding them. This is the mechanism behind the one-reader requirement
  above; without it the sequences interleave and deliveries are lost.
- `POST /api/v1/inbound/deliveries` takes one bounded batch —
  `{watchId, deliveries: [{seq, op, record, oldRecord}]}` — and answers only once the
  publish is durable (I2), which is what makes the worker's prune safe. A delivery for
  a watch this worker does not hold the lease on is refused, so an expired lease fails
  the batch instead of racing its successor.

**The server composes `SourceID` as `"postgres:" + watchID`; it is never read from the
body.** A caller therefore cannot reach another watch's mark. What a caller *can* do
is burn its own watch forward by sending an inflated `seq` — a residual risk accepted
knowingly, because the only caller that can reach this endpoint for a given watch is a
worker that already holds that database's DSN and could simply lie about the rows.

The batch is capped for the reason `defaultInboundBatch` exists: every delivery that
correlates can start an instance, so an undrained backlog handed to the run loop in
one batch is an unbounded publish storm. One HTTP round trip is one publish batch is
one fsync.

### The cursor is the mark

The clio bridge keeps a best-effort sidecar cursor and is careful to say it is not the
correctness authority. This design does not need one: the engine's high-water mark
*is* the durable cursor, and the worker can ask for it (it comes back with the watch)
and hold it in memory between polls. One less piece of state, and no way for a cursor
and a mark to disagree.

Pruning follows from it: after a batch is acknowledged durable, the worker deletes
`WHERE seq <= <acked>`. That needs a `delete` grant on the outbox table and on nothing
else. `startFromTip` on a newly enabled watch is the same operation without the
publish — claim and delete the backlog to the tip — so it needs no new engine concept.

### What is not covered

MariaDB and SQL Server get nothing here. The seam is shaped so they can follow — the
outbox, the claim, the delivery endpoint and the mark are all product-neutral, and
only the claim statement's syntax differs — but ADR-0173's instinct applies: review
the seam on one product before a second rides on it. Logical replication is
deliberately left as a *second source behind the same endpoint*, not as a competitor
to it; adding it later changes what fills `seq` (an LSN) and nothing downstream.

### Consequences

- **Positive:** the engine still holds no database credential, in either direction,
  and the customer's database holds no Atlas credential. Both halves of ADR-0173's
  promise survive contact with the inbound case.
- **Positive:** ADR-0075's idempotency machinery gets its second consumer, which is
  what turns "generic" from an intention into a property — the engine still names no
  connector.
- **Positive:** the outbound and inbound halves share one DSN, one pool and one
  connector name. An operator configures a database once.
- **Positive:** one piece of state disappears relative to clio — the sidecar cursor —
  because the durable mark can be read back.
- **Negative / trade-offs accepted:**
  - **Schema in someone else's database.** A table, a sequence and a trigger per
    watched table. Where an operator may not create them, this connector cannot be
    used, and logical replication would need *more* privilege rather than less.
  - **A write per changed row**, inside the user's transaction. Bulk loads pay it.
  - **Trigger blind spots**: `TRUNCATE`, `session_replication_role = 'replica'`, DDL.
  - **A worker does work no job asked for**, which is a new shape for the worker and a
    second loop to reason about at shutdown.
  - **A watch does not scale out.** The scalar mark admits one reader, so throughput
    for one table is one worker's throughput. Watches spread across workers; a single
    hot table does not.
  - **A worker can burn its own watch's mark forward**, as above.
  - **Latency is a poll interval**, not a push. The clio bridge's 2s cadence is the
    obvious starting default, and `NOTIFY` as a wake-up hint is the obvious later
    optimization — on top of the outbox, never instead of it.
- **Follow-ups / risks to watch:** the claim must stay one statement; splitting the
  select from the update puts two readers back into the window `for update skip locked`
  closes. The lease is the *only* thing standing between this design and silently
  dropped deliveries, so its expiry needs a test that asserts the losing worker's batch
  is refused rather than applied. And the outbox is unbounded if a watch is disabled
  while its trigger stays installed — the operator-facing view should say how many rows
  are waiting, because "the queue nobody drains" is this design's version of the WAL
  retention it refused.

## Pros and cons of the options

### Option 1 — engine-side bridge
- Good: the shortest path; `api/inboundBridge` already has the shape, the ticker, the
  batch cap and the run-loop discipline.
- Bad: puts a DSN in the engine, which is the one thing ADR-0173 exists to prevent.

### Option 2 — worker-side reader (chosen)
- Good: the credential stays where it is used, and where the outbound half already
  keeps it; one configuration serves both directions.
- Bad: needs a new endpoint and a new worker loop; a worker is trusted for its own
  source's sequence.

### Option 3 — the database delivers (trigger + `pg_net`)
- Good: works today with no Atlas change at all, and is the right thing to reach for
  as a stopgap.
- Bad: stores an Atlas bearer token in the customer's database; no retry, no ordering,
  no dedup — a replay double-starts a process.

### A — logical replication
- Good: no schema change, no write amplification, sees everything, and LSN order is
  commit order, so the ordering trap does not exist.
- Bad: a slot is a resource in a database Atlas does not operate whose failure mode is
  that database's disk; more privilege to set up; a new dependency and a decoder.

### B — trigger-written outbox (chosen)
- Good: no new dependency, no slot, an auditable per-table opt-in, and a failure mode
  the owning operator can see and truncate.
- Bad: schema and a per-row write in someone else's database; blind to `TRUNCATE`,
  DDL and trigger-suppressed sessions; needs the claim-time ordering above to be
  correct at all.

### C — `LISTEN`/`NOTIFY`
- Good: push, so latency is not a poll interval; no table to drain.
- Bad: not durable — a disconnected listener loses notifications silently, which
  disqualifies it as a delivery mechanism.

### D — timestamp-column polling
- Good: needs no DDL and no trigger; the least privilege of any option.
- Bad: cannot see a delete, cannot separate two updates inside its resolution, and has
  no sequence the mark could use.

## Relationship to other records

- extends [ADR-0173](0173-generic-sql-connector.md) with the inbound direction, and
  keeps its promise that the engine never holds a database credential
- reuses [ADR-0075](0075-clio-inbound-event-bridge.md)'s `InboundDeliveryValue`
  high-water mark and `PublishInbound` — its second consumer, and the reason the
  mechanism was made generic
- applies [ADR-0168](0168-connector-work-on-a-worker.md)'s engine-resolves /
  worker-executes split in the other direction, and joins its worker-reported
  connector-name coverage
- follows [ADR-0164](0164-no-in-process-service-tasks.md)'s rule that new connector
  work is built worker-first
- funnels into [ADR-0020](0020-message-correlation.md)'s correlation and
  [ADR-0035](0035-message-start-events.md)'s message starts rather than a parallel
  start path
- honors [ADR-0010](0010-go-and-no-cgo.md), which is also why logical replication's
  driver choice stays a pure-Go one when it arrives
