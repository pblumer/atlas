# ADR-0517: Bounded instance history and causal detail replay

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-01
- **Deciders:** atlas maintainers, Operations History at Scale programme owner

## Context and problem statement

The existing `GET /api/v1/instances/{key}/timeline` builds and returns the full
activation list, replay frames and variable snapshots. Its implementation currently
scans event-derived history under `Server.do`, holding the single writer for work
whose cost depends on the number of steps and variable changes. The browser renders
one DOM node per activation. Multi-instance tasks with 50,000 iterations make both
reads and UI intractable. OHS1 groups the *presentation* using already-durable
parent-token lineage but does not solve the unbounded read.

## Decision drivers

- Preserve exact causal event order (ADR-0065), MI body/iteration semantics
  (ADR-0077) and historical replay including migrations (ADR-0162).
- Queries must be bounded and run off the single-writer loop (invariant I3).
- All durable projections are deterministic event-derived state, written via one
  `applyToState` (I1/I4/I6), with no extra per-command allocations.
- Keep existing timeline API backward compatible for older clients.
- Respect retention purge and access controls; no arbitrary unindexed JSON search.

## Considered options

1. Client-only virtual list over the existing full timeline.
2. Limit/truncate the current timeline response.
3. New bounded summary/iteration/detail read endpoints built over indexed
   derived state where existing access paths are insufficient (proposed).

## Decision outcome

**Proposed (not yet accepted):** A separate, versioned bounded query surface
for multi-instance body summaries, cursor-paged iteration discovery and an exact
causal replay window, leaving the old full timeline contract intact.
A group is identified by its *specific body activation*, never by BPMN element ID
alone. Use the event's body/iteration lineage and log positions. Keep the summary
on the cheap read path; avoid copying snapshots/frames until a user selects a
specific iteration.

## Proposed OHS2 implementation contract (approval requested)

### Durable index

Use a versioned Pebble key prefix keyed by
`(processInstanceKey, bodyElementInstanceKey, activationPosition, roundElementInstanceKey)`
for iterations, and a separate body summary prefix keyed by
`(processInstanceKey, bodyActivationPosition, bodyElementInstanceKey)`.
Values retain durable body token, parent token, definition key/version, round
number, lifecycle state and positions needed for exact lookup. Store a
secondary key for `(processInstanceKey, roundElementInstanceKey)` to resolve
exact element-instance lookups. No arbitrary free-text search.

Write the index exclusively from the existing deterministic `applyToState`
event fold, with a reusable encoding buffer and no new per-command allocation.
On recovery the same events rebuild the same index. A schema/version marker
allows a one-time bounded background backfill for pre-index histories; until
backfill completes, the endpoint explicitly reports index unavailable, rather
than scanning all historical events in an HTTP request.

### Snapshot and pagination

Each request takes a `state.ReadView` off the writer loop. The cursor contains
version, instance key, body key, canonical filter digest, last returned ordered
key, and a high-watermark position. Encode and authenticate the opaque cursor
to prevent accidental or malicious tampering. No long-lived Pebble snapshot
is held across HTTP requests. Subsequent pages scan keys only up to the frozen
high-watermark; already returned records are never reordered. Status/incident
filters that depend on mutable current state require either an as-of projection
or an explicit restart-on-change cursor error, not silent page drift.

Default page size 100, maximum 200. Iterator seeks must use bounded index
prefixes and cursor keys. Stop after `limit+1` matching rows; for any filter,
the access path must bound work, not just the serialized response size. If
status/incident filtering needs additional indexes, implement and benchmark
them before enabling those filters.

### API and compatibility

- `GET /api/v1/instances/{key}/multi-instances?limit=100&cursor=...`
  returns body executions, not merged BPMN element names.
- `GET /api/v1/instances/{key}/multi-instances/{bodyKey}/iterations`
  supports exact iteration, element-instance key, state and incident filters
  through indexed reads. Cursor binds the entire filter set.
- No variable snapshots or full replay frames are built for these endpoints.
- Legacy `/timeline` remains unchanged. OHS3 will add scoped detail replay.
- Purged instances return not found/gone according to existing retention API
  semantics; index keys are removed consistently with purge events.

### Acceptance and recovery

Tests cover repeated/nested/parallel MI bodies, migration, termination,
50,000 iterations, cursor boundaries and concurrent writes. Persist, restart,
replay WAL and compare index keys and query results to live state. Benchmark
response size, read latency, allocations and single-writer responsiveness.

**Decision gate:** This section specifies the recommended approach, but
requires maintainer acceptance before implementation of the new durable index.
Do not treat this proposed ADR as an accepted architectural change.

### Consequences

- **Positive:** The first view, search and selected-step replay can be bounded
  independently of 50,000 iterations, without changing BPMN execution.
- **Negative / trade-offs accepted:** Additional query contracts and possibly
  projection state; old unpaged clients remain inherently expensive unless
  separately warned or gated.
- **Follow-ups / risks to watch:** Mixed old/new events, nested and overlapping
  body executions, replay causality, incident history versus current incidents,
  canceled rounds, scope-variable retention, privacy and export-gated purge.

## Pros and cons of the options

### Option 1

- Good: no server changes.
- Bad: full timeline payload and on-writer reads remain unbounded.

### Option 2

- Good: small API patch.
- Bad: silently omits historical facts, breaks replay and variable semantics.

### Option 3

- Good: bounded, auditable operator access, compatible with existing API.
- Bad: larger implementation, requires benchmark and recovery verification.

## Links

- [OHS programme](../planning/operations-history-at-scale-program.md)
- [Issue #1481](https://github.com/pblumer/atlas/issues/1481)
- ADR-0046, ADR-0065, ADR-0077, ADR-0115, ADR-0162, ADR-0244.
