# Operations History at Scale (OHS) delivery programme

**Programme ID:** OHS.  
**Baseline:** `pblumer/atlas` main, 2026-11-01.  
**Strategic tracks:** Milestone 4 (Operability) and Milestone S (Operations UI).  
**Owner:** pblumer (feature scope approved 2026-11-01).  
**Source issue:** [#1481](https://github.com/pblumer/atlas/issues/1481).  
**Operational status:** [programme manifest](../programs/ohs/program.json).

## Outcome

An operator can understand a process containing 50,000 multi-instance iterations without opening 50,000 rows, find a specific iteration, and inspect only that iteration's durable input/output, incidents, decisions and causal replay. The grouped overview is the normal representation at *any* MI cardinality.

## Decisions and invariants

- Existing [ADR-0077](../adr/0077-multi-instance-activities.md) defines MI body and inner execution. [ADR-0065](../adr/0065-multi-token-process-replay.md) defines durable token lineage; [ADR-0046](../adr/0046-single-process-step-replay.md) defines step history.
- The initial UI grouping uses those existing facts only. Repeated or nested bodies may not be identified by BPMN element id alone. Older records without sufficient lineage remain ungrouped rather than incorrectly attributed.
- The later bounded query API needs its own [draft decision](../adr/draft-scalable-instance-replay.md) before implementation.
- I1/I3/I4/I6: no hot-path allocations, no data-dependent scans on the single-writer loop, one deterministic applier, and no fabricated facts. A grouped display is not aggregated execution or a new BPMN semantic.
- The full timeline endpoint is currently unbounded, so OHS1 explicitly **does not** claim suitability for 50,000 iterations. The upper bound becomes a hard acceptance condition of OHS2/OHS3.

## Vertical delivery slices

### OHS1 – Default grouped History

A replay displays a single collapsed row per uniquely identified multi-instance body, with iteration count and individual round navigation on expansion. It uses an additive parent-token field in the existing timeline, no new state or persistence. E2E tests verify distinct bodies and expansion. Limitation: the API and frames are still fully materialized.

### OHS2 – Bounded iteration discovery

Introduce a read-only, cursor-paged API to discover body groups and find iterations by exact iteration number, element-instance key, state and incident. Use off-writer snapshot reads, bounded results (e.g. 100) and a sound grouping index if necessary. Provide a usable search/next-page UI. Acceptance: 50,000 iterations do not cause an unbounded initial payload or writer stall. New index only through deterministic event-derived state with recovery tests.

### OHS3 – Scalable detail replay

Load the selected iteration's causal replay window and its exact inputs, outputs, decisions, incidents and variables without materializing every frame/snapshot for the full process. Confirm semantics for nested MI, migration, terminated rounds, retention purge, parallel branches, and backwards compatibility. Test with a 50,000-iteration fixture and report memory/latency/writer responsiveness before merge.

## Release and gates

Each slice uses TDD, Playwright tests where the UI changes, the mandatory Go checks, review, and programme handoff. An implementation is not complete simply because its PR merges. Do not claim the 50,000-iteration user experience until OHS3 is independently verified.
