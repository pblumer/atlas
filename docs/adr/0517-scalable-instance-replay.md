# ADR-0517: Bounded instance history and causal detail replay

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-11-01
- **Deciders:** atlas maintainers, Operations History at Scale programme owner
- **Approved by:** programme owner, 2026-11-01

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

**Accepted:** A separate, versioned bounded query surface
for multi-instance body summaries, cursor-paged iteration discovery and an exact
causal replay window, leaving the old full timeline contract intact.
A group is identified by its *specific body activation*, never by BPMN element ID
alone. Use the event's body/iteration lineage and log positions. Keep the summary
on the cheap read path; avoid copying snapshots/frames until a user selects a
specific iteration.

## OHS2 implementation contract

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

**Decision:** The programme owner approved this architecture on 2026-11-01.
Implementation remains subject to the listed tests, performance and recovery gates.

### Consequences

- **Positive:** The first view, search and selected-step replay can be bounded
  independently of 50,000 iterations, without changing BPMN execution.
- **Negative / trade-offs accepted:** Additional query contracts and possibly
  projection state; old unpaged clients remain inherently expensive unless
  separately warned or gated.
- **Follow-ups / risks to watch:** Mixed old/new events, nested and overlapping
  body executions, replay causality, incident history versus current incidents,
  canceled rounds, scope-variable retention, privacy and export-gated purge.

## As built: deterministic index projection (OHS2 increment 1)

PR #1487 reserves MI index key families. PR #1488 adds a versioned Pebble
projection for *newly applied* MI body and iteration activations, their durable
activation/end positions and token lineage, keyed to the concrete body activation.
A body-element reverse key allows a round's `FlowScopeKey` to resolve the exact
body activation through a point read of the indexed transaction, including
activations in the same processor batch. `applyToState` is the only writer;
independent WAL recovery of parallel and sequential MI is tested against live
projection output. Purge removes the new per-instance index ranges by range
tombstones. Shared Store/ReadView queries support bounded reads, but the public
cursor-paged HTTP surface is not implemented yet.

**OHS2 increment 2 (PR following #1488):** The store now persists a
versioned, durable `mi_history_index_v1` readiness marker only when the retained
instance-history families are demonstrably empty before event replay. A
pre-existing store with an applied event position or any retained lifecycle
facts is deliberately **not** marked complete. Every MI index query (on both
Store and ReadView) fails with `ErrMIHistoryIndexUnavailable` while the marker
is absent; individual keys from a partially upgraded store must not look like
a complete timeline. The startup check performs bounded prefix seeks, not
historical scans. A corrupt marker fails closed. This is a safety gate, **not**
a historical backfill or a migration-complete flag for old state.

**OHS2 increment 3 — index schema v2 and the bounded read API (2026-11-02).**
The index schema moved to v2 (marker `mi_history_index_v2`); a store marked only
for v1 is treated as legacy and stays unavailable, exactly like a pre-index
store. Implemented:

- *Iteration number.* Each iteration row records the `loopCounter` the engine
  bound into the iteration's scope. The variable event precedes the iteration's
  Activated event in the log, so the fold reads a prior durable fact (I6); an
  iteration without one records 0 ("not recorded"), never a guess. A new family
  `cfMIRoundOrder` orders a body activation's iterations by
  `(loopCounter, iterationKey)`, so "iteration 37,421" is a seek, not a skip.
- *Lifecycle filter.* `cfMIRoundByState` holds the same order per lifecycle state,
  moved (delete + set) by the end fact. The state filter is a seek in that family.
- *Incidents.* `IncidentCreated` is attributed to the innermost MI iteration or
  body whose live scope chain holds the incident's element (bounded walk);
  `IncidentResolved` closes the attribution. `cfMIRoundIncident` orders iterations
  that ever had an incident (historical, monotonic); per-owner records keep raised
  and resolved positions and a "superseded" outcome. An incident cleared because
  its element ended (ADR-0061, no event) stays unresolved in the index; readers
  distinguish *open* from *cleared* with a point read of the current incident
  table, so "current" and "historical" incidents are never conflated.
- *Counters.* Seeded, completed, terminated, incidents raised, iterations with
  incidents and a change count per body activation are write-only Pebble merges
  (`cfMIBodyCounter`), like the active-children counters: recording an iteration
  does not read the body. The planned total of a *sequential* body is not a
  recorded fact and is not reported.
- *Nesting.* Each body records the innermost enclosing MI iteration
  (`cfMIBodyByParent`), so the overview lists top-level bodies without visiting a
  nested body seeded once per outer iteration.
- *HTTP and MCP.* `api/mihistory` serves
  `GET /api/v1/instances/{key}/multi-instances`,
  `.../multi-instances/{bodyKey}`, `.../{bodyKey}/iterations` and
  `.../iterations/{iterationKey}` (MCP tools `atlas_instance_multi_instances`,
  `atlas_multi_instance_body`, `atlas_multi_instance_iterations`,
  `atlas_multi_instance_iteration`), role Operator like the timeline. Reads run on
  a `state.ReadView` opened off the writer. Pages default to 100, maximum 200; each
  iteration page examines at most a fixed scan budget of index keys and resumes
  through its cursor, so no filter combination becomes a walk. The cursor is
  `base64url(payload ‖ HMAC-SHA-256/128)` under a per-process random key with a
  one-hour TTL; it binds kind, instance, body activation, a SHA-256 digest of the
  canonical filter set, the resume position, the frozen high-water log position
  and the body's change count. A state-filtered page sequence answers
  `409 cursor_stale` once the body changed; unfiltered and incident-filtered pages
  hide rows past the high-water mark. An index that is not verified complete
  answers `409 history_unavailable` with an explanation, never an empty list.

*Measured (2026-11-02, 4-vCPU cloud container, full server over HTTP,
`api/mihistory/acceptance_test.go`).* A parallel MI of 50,000 iterations with ten
real incidents: overview 382 bytes in 0.6 ms (396 bytes for a 2,000-iteration
instance); a 100-row page about 19 KB in 1.4 ms; jump to iteration 37,421 1.4 ms
and examined exactly 100 index keys; `state=active` examined 10 keys; worst
start-instance latency 3 ms alone and 7 ms under concurrent page reads; WAL replay
of the whole log into a fresh store 2.9 s with identical answers. For comparison
the legacy `/timeline` grows with the square of the iteration count: 139 MB at
1,000 iterations, 553 MB at 2,000, 2.2 GB and 59 s on the writer at 4,000; at
50,000 the test process was killed for memory. Hot path
(`BenchmarkMultiInstanceIterations`, 100 iterations per instance): about +2.8
allocations per iteration (one additional point read for the loopCounter), time
within run-to-run noise; `BenchmarkInstanceLifecycle` unchanged at 221 allocations.

**OHS2 increment 4 — Multi-Instance Explorer (2026-11-02).** The Operations
replay asks the bounded API first. When an execution has more than 500
iterations (or the top-level listing does not fit one page), it does not load
the legacy timeline: it shows the diagram, the executions as collapsed groups
with the server's counters and the Multi-Instance Explorer (`api/web/mi-explorer.js`):
one page of 100 iterations, jump to an iteration number, state and incident
filters, keyboard navigation, loading/empty/error/retry states and an
explanation for an unavailable index. Smaller instances keep the classic replay
and gain *Explore iterations* on the grouped row. The bodies listing now also
carries the instance's definition key and state, so the bounded view needs no
timeline. Playwright specs `e2e/mi-explorer.spec.mjs` and `e2e/mi-history.spec.mjs`
assert that the bounded view never requests `/timeline` and never holds more
than one page of rows.

**Still not built:** a verified historical rebuild for legacy (pre-v2) stores —
they stay `history_unavailable`; a bounded outline of the *other* steps of a
large instance (the bounded view shows only its multi-instance executions);
OHS3's scoped detail replay of one iteration (variables, decisions, step frames). A historical reconstruction must use durable event payloads,
including the MI role and `FlowScopeKey`. The old `ElementReplayValue` alone
does not record either field; inferring them from the token trail is unsafe,
especially for repeated/nested MI bodies and migrations. A history with
compacted/unavailable required WAL must remain unavailable rather than be
silently declared complete.
An old live body without an indexed activation does not acquire guessed lineage:
rounds observed after upgrade are left absent from the new projection until the
historical backfill has reconstructed their parent activation. Do not expose
incomplete projections as if they covered a whole instance.

*Known limitation found while measuring:* completing the iterations of a parallel
multi-instance **subprocess** is quadratic in the engine today, independent of
this index — every scope completion disarms event-subprocess triggers by walking
all live element instances of the instance (`disarmEventSubprocesses`). 1,000
such iterations took 24 s; 50,000 do not finish in reasonable time. The
acceptance fixture therefore uses a multi-instance script task. Tracked in
[Issue #1494](https://github.com/pblumer/atlas/issues/1494).

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
