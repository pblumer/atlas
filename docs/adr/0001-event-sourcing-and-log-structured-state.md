# ADR-0001: Event sourcing and log-structured state

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

Chrampfer must be durable (survive crashes, run multi-week process instances) *and* high-throughput (many instances per second). The naive approach — interpret BPMN and write process state to a SQL database, one `UPDATE` per state transition — fails on throughput: every update is a disk round-trip with lock contention, and it introduces a two-phase consistency hazard (the in-memory state and the persisted state can disagree after a crash).

We need a persistence model that delivers durability without paying a synchronous random-write per transition, and that makes crash recovery simple and provably consistent.

## Decision drivers

- High write throughput under durability
- Crash recovery that is simple and correct by construction
- No divergence between in-memory state and persisted state
- A natural audit trail for a workflow engine (compliance, debugging)
- Foundation for downstream analytics / event export

## Considered options

1. **Mutable state in SQL** — classic engines (relational row per instance/token, updated in place)
2. **Mutable state in an embedded KV store** — same model, faster store
3. **Event sourcing over an append-only log, with state as a materialized fold**

## Decision outcome

Chosen option: **event sourcing over an append-only write-ahead log, with the live state held in an embedded KV store as a materialization of that log.**

State is never written in place. Every meaningful transition is an immutable **event** appended to the log. The current state is a fold of the log. The log is the single source of truth.

### Consequences

- **Positive:** Writes become sequential appends (the fastest thing a disk can do), enabling group commit. Recovery is a log replay through the same `applyToState` used live, so state and log cannot diverge. We get a complete, ordered audit trail for free. Non-deterministic effects (timestamps, generated keys) are frozen into events, keeping replay deterministic.
- **Negative / trade-offs accepted:** Two representations to keep in sync conceptually (log + materialized state); a compaction/snapshotting strategy is needed so replay does not start from the beginning of time. Queries must go through materialized indexes, not ad-hoc SQL.
- **Follow-ups:** Snapshotting/log-compaction design; exported-log stream for analytics.

## Pros and cons of the options

### Mutable state in SQL
- Good: familiar, ad-hoc queryable.
- Bad: synchronous random writes, lock contention, two-phase consistency hazard, throughput ceiling.

### Mutable state in embedded KV
- Good: faster than SQL.
- Bad: still in-place mutation; recovery and consistency story remains awkward.

### Event sourcing over a log
- Good: sequential writes, group commit, trivial deterministic recovery, audit trail, analytics feed.
- Bad: needs snapshotting; queries via materialized indexes only.

## Links

- enables ADR-0005 (group commit)
- pairs with ADR-0002 (single writer) and ADR-0003 (Pebble)
