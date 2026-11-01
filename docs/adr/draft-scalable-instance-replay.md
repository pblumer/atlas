# ADR-DRAFT: Bounded instance history and causal detail replay

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

The precise on-disk index, cursor stability model, read snapshot lifetime,
historical-data fallback and HTTP response shape must be resolved and reviewed
before OHS2 begins. Do not treat this proposal as authorization for a new index
or a new engine write path.

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
