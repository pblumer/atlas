# ADR-0065: Multi-token process replay and causal token lineage

- **Status:** Accepted
- **Date:** 2026-07-26
- **Deciders:** Atlas maintainers

## Context

ADR-0046 retained only activated element ids. That answers activation order, but
not token state: the sequential processor records two fork branches one after the
other even though BPMN says both tokens coexist. Timestamps cannot repair this;
batching and equal timestamps do not encode causality or consumption at a join.

## Decision

Element-instance events carry a durable token id, optional parent token id, and
the sequence-flow index that caused activation. A non-forking transition keeps
the token id. A multi-flow fork gives every target a new id and records the
parent. A parallel join retains every arrival as a waiting element instance,
consumes all arrivals when complete, and creates one new continuation token. An
exclusive gateway never synchronizes: each arriving token keeps its own lineage,
while an exclusive split still selects exactly one flow.

`applyToState` derives a per-instance lifecycle history from Activated and
Completed/Terminated facts. The timeline endpoint folds that history by log
position into frames containing the complete active token set. Processor
execution remains single-writer and sequential; only the represented BPMN state
is logically parallel. Recovery uses the same applier and therefore rebuilds the
same ids, relationships, ordering, and frames.

Old element-instance payloads remain decodable. Their missing lineage fields are
zero, and the existing linear `steps` response remains available as the legacy
fallback; new events additionally produce causal `frames`.

## Consequences

- Replay no longer guesses concurrency from timestamps or browser ordering.
- Parallel arrivals can remain visibly waiting across processor batches and
  restart, then disappear together before one continuation appears.
- The response retains the activation table while adding deterministic frames.
- Each lifecycle fact adds one derived-state write; no browser-only identity and
  no separate runtime token entity are introduced.

## Links

- supersedes the single-token visualization assumption in ADR-0046
- extends ADR-0024's parallel-join synchronization facts
- follows ADR-0048's event-derived, recovery-safe history pattern
