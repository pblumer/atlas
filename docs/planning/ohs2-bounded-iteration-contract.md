# OHS2 bounded iteration discovery: implementation contract

This document records the required test-first contract for OHS2 (Issue #1481).
It does **not** claim the API is implemented.

## Read API contract (proposal)

- `GET /api/v1/instances/{instanceKey}/multi-instances`:
  bounded page of MI *body executions* (not element IDs). Return body element
  instance key, token lineage, process definition version, total and status counts
  only when those counts are derivable from retained events, and a continuation
  cursor. Default limit 100; reject limit above 200.
- `GET /api/v1/instances/{instanceKey}/multi-instances/{bodyKey}/iterations`:
  bounded page ordered by durable activation position and element instance key.
  Filters: `iteration` (exact), `elementInstanceKey` (exact),
  `state`, `incident` (current vs historical must be explicit). A cursor must
  bind to the body, filter set and a stable snapshot/high-watermark.
- Every cursor is opaque; malformed or mismatched cursors return HTTP 400.
  A deleted instance or purged history must never silently return fabricated rows.
  Snapshot expiry/compaction is explicit; no unbounded scan under the writer.
- Use `state.ReadView` and immutable compiled metadata captured on the writer,
  then perform all population-dependent reads off the writer loop.
- A request must not materialize full replay frames, variable snapshots, or the
  full event history. A limit of 100 must cap work as well as response size;
  filtering 50,000 records after reading them all is not sufficient.
- Any new projection is derived solely from durable events through the existing
  deterministic `applyToState` path. No new per-command hot-path allocation.
- Existing `/timeline` response is unchanged for compatibility.

## Required regression and performance gates

1. Two visits to the same MI task yield distinct body groups.
2. Nested and parallel MI iterations retain exact body lineage.
3. A page boundary (100/101) never duplicates or skips an iteration.
4. A request for iteration 37,421 of 50,000 returns the exact persisted record
   without reading 37,420 preceding records.
5. Concurrent writes during pagination cannot silently duplicate/omit rows.
6. Current incidents and historical incidents are not conflated.
7. Terminated, active and completed rounds are distinguished.
8. Purge, migration and legacy missing-lineage cases have explicit semantics.
9. Crash/recovery reconstitutes identical query results and index contents.
10. Benchmark 50,000 iterations: bounded response size, allocations, read
    latency, and writer-loop responsiveness.

## Architecture gate

[ADR-0517](../adr/0517-scalable-instance-replay.md) remains **Proposed**.
Before adding a durable index or public API, approve a concrete schema,
snapshot/cursor contract and retention strategy in that ADR. In particular,
do not implement a full-history scan and call it pagination.
