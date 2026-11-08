# CON0 — Workflow Control-Flow Patterns inventory (43/43)

**Programme:** [BPMN 2.x Conformance](bpmn-conformance-program.md) · [CON0 issue](https://github.com/pblumer/atlas/issues/1615)  
**Reference:** N. Russell, A. ter Hofstede, W. van der Aalst, N. Mulyar, *Workflow Control-Flow Patterns: A Revised View* (BPM Center Report BPM-06-22, 2006); [Workflow Patterns Initiative](https://www.workflowpatterns.com/patterns/control/) and [pattern title cross-list](https://docs.flowcentric.com/howDoI/wfPatterns/index.html).  
**Status:** Full **identifier/title inventory**, not a reviewed WCP **support** matrix (2026-11-08). Mapping to BPMN and test sufficiency require case-by-case assessment.

This list enumerates each of the 43 classic control-flow patterns; the taxonomy is **not** an OMG BPMN 2.0.2 conformance requirement. The nine mapped rows are **registrations** in `conformance/scenario.go`, not independently proved WCP conformance. A feature's name does not prove complex token semantics.

| ID | Canonical name | Category | Current suite | Semantics to review | Proven WCP support |
| --- | --- | --- | --- | --- | --- |
| WCP-1 | Sequence | Basic control-flow | Mapped: `sequence-flow` | Stepwise sequence | Unreviewed |
| WCP-2 | Parallel Split | Basic control-flow | Mapped: `parallel-gateway` | Parallel fan-out | Unreviewed |
| WCP-3 | Synchronization | Basic control-flow | Mapped: `parallel-gateway` | AND join | Unreviewed |
| WCP-4 | Exclusive Choice | Basic control-flow | Mapped: `exclusive-gateway` | Exclusive branch | Unreviewed |
| WCP-5 | Simple Merge | Basic control-flow | Mapped: `exclusive-gateway` | Simple merge | Unreviewed |
| WCP-6 | Multi-Choice | Advanced branching and synchronization | Mapped: `inclusive-gateway` | Inclusive branch | Unreviewed |
| WCP-7 | Structured Synchronizing Merge | Advanced branching and synchronization | Mapped: `inclusive-gateway` | Structured OR join | Unreviewed |
| WCP-8 | Multi-Merge | Advanced branching and synchronization | Not registered | Unstructured merge; repeated arrival semantics | Unreviewed |
| WCP-9 | Structured Discriminator | Advanced branching and synchronization | Not registered | First-of-N discriminator (structured) | Unreviewed |
| WCP-10 | Arbitrary Cycles | Iteration / termination | Not registered | Arbitrary back-edges; distinct from standard loop | Unreviewed |
| WCP-11 | Implicit Termination | Iteration / termination | Not registered | Implicit completion when all paths terminate | Unreviewed |
| WCP-12 | Multiple Instances without Synchronization | Multiple instances | Not registered | Concurrent MI with no synchronization | Unreviewed |
| WCP-13 | Multiple Instances with a Priori Design-Time Knowledge | Multiple instances | Not registered | Compile-time fixed MI cardinality | Unreviewed |
| WCP-14 | Multiple Instances with a Priori Run-Time Knowledge | Multiple instances | Not registered | Run-time-known MI cardinality | Unreviewed |
| WCP-15 | Multiple Instances without a Priori Run-Time Knowledge | Multiple instances | Not registered | MI creation without prior cardinality | Unreviewed |
| WCP-16 | Deferred Choice | State-based | Mapped: `event-based-gateway` | Event-first race | Unreviewed |
| WCP-17 | Interleaved Parallel Routing | State-based | Not registered | Unordered sequential task execution | Unreviewed |
| WCP-18 | Milestone | State-based | Not registered | Enabled-only-inside-state-window | Unreviewed |
| WCP-19 | Cancel Activity | Cancellation | Not registered | Interrupting boundary / cancellation of activity | Unreviewed |
| WCP-20 | Cancel Case | Cancellation | Not registered | Whole-instance cancellation / terminate | Unreviewed |
| WCP-21 | Structured Loop | Iteration | Mapped: `standard-loop` | Standard structured loop | Unreviewed |
| WCP-22 | Recursion | Iteration | Not registered | Recursive process instantiation | Unreviewed |
| WCP-23 | Transient Trigger | Triggers | Not registered | Transient event trigger semantics | Unreviewed |
| WCP-24 | Persistent Trigger | Triggers | Not registered | Durable event trigger semantics | Unreviewed |
| WCP-25 | Cancel Region | Cancellation / multiple instances | Not registered | Cancellation limited to a subregion | Unreviewed |
| WCP-26 | Cancel Multiple Instance Activity | Cancellation / multiple instances | Not registered | Cancel all MI rounds of an activity | Unreviewed |
| WCP-27 | Complete Multiple Instance Activity | Cancellation / multiple instances | Not registered | Complete remaining MI by condition / early exit | Unreviewed |
| WCP-28 | Blocking Discriminator | Advanced branching and synchronization | Not registered | Blocking discriminator | Unreviewed |
| WCP-29 | Cancelling Discriminator | Advanced branching and synchronization | Not registered | Cancelling discriminator | Unreviewed |
| WCP-30 | Structured Partial Join | Advanced branching and synchronization | Not registered | Structured N-out-of-M join | Unreviewed |
| WCP-31 | Blocking Partial Join | Advanced branching and synchronization | Not registered | Blocking N-out-of-M join | Unreviewed |
| WCP-32 | Cancelling Partial Join | Advanced branching and synchronization | Not registered | Cancelling N-out-of-M join | Unreviewed |
| WCP-33 | Generalised AND-Join | Advanced branching and synchronization | Not registered | Generalised AND join | Unreviewed |
| WCP-34 | Static Partial Join for Multiple Instances | Multiple instances | Not registered | Static partial join for MI | Unreviewed |
| WCP-35 | Cancelling Partial Join for Multiple Instances | Multiple instances | Not registered | Cancelling static partial join for MI | Unreviewed |
| WCP-36 | Dynamic Partial Join for Multiple Instances | Multiple instances | Not registered | Dynamic partial join for MI | Unreviewed |
| WCP-37 | Local Synchronizing Merge | Advanced synchronization | Not registered | Local OR merge | Unreviewed |
| WCP-38 | General Synchronizing Merge | Advanced synchronization | Not registered | General OR merge | Unreviewed |
| WCP-39 | Critical Section | State-based | Not registered | Mutual exclusion / critical region | Unreviewed |
| WCP-40 | Interleaved Routing | State-based | Not registered | Interleaved execution order | Unreviewed |
| WCP-41 | Thread Merge | Thread management | Not registered | Thread merging without sync | Unreviewed |
| WCP-42 | Thread Split | Thread management | Not registered | Thread splitting without sync | Unreviewed |
| WCP-43 | Explicit Termination | Termination | Not registered | Explicit termination; candidate terminate end | Unreviewed |

## Review contract

Every row must eventually carry a testable formal WCP definition, BPMN realization if one is valid, scope/topology assumptions, case reference, exact expected token paths, crash/replay evidence when relevant, and one of `proven`, `partial`, `unsupported`, `not-representable-in-BPMN`, `unverified`. **Do not automatically label unregistered patterns unsupported**; neither do they count as supported. Different implementations can implement a WCP using combinations of BPMN elements; do not mistake “no dedicated icon” for “impossible”.

### High-risk review queue

- **WCP-8, -9, -28 through -33, -37, -38:** distinguish multi-merge, discriminator and structured/acyclic/general OR synchronization; ADR-0033 explicitly limits cyclic inclusive joins.
- **WCP-12 through -15, -26, -27, -34 through -36:** distinguish MI cardinality known at build, at activation or dynamically, and early completion/cancellation. Separate activity MI versus subprocess MI.
- **WCP-10, -21, -22:** arbitrary cycles, structured loops and recursion must not share a “loop supported” check.
- **WCP-16, -23, -24:** deferred selection and trigger persistence cannot be proven by a single timer or message fixture.
- **WCP-19, -20, -25, -43:** activity, case, region and explicit termination differ in token cancellation and scope semantics. A Terminate End may realize one WCP variant, but needs a checked oracle.
- **WCP-39–42:** mutual exclusion, interleaving and thread semantics may require constructs not natively provided by BPMN; do not force them into the BPMN execution denominator.

## Verified counts (registration only)

- **43** pattern identifiers/titles enumerated in this document.
- **9** patterns registered in `conformance/scenario.go`: WCP-1, -2, -3, -4, -5, -6, -7, -16 and -21.
- **34** patterns have **no registration** in that suite. This is an inventory observation, **not** a statement that atlas cannot implement them.
- **0/43** rows independently assessed and signed off for WCP behavioral conformance in this CON0 draft.

**Next:** CON1 should link stable WCP rows to reviewed evidence without deriving a BPMN compliance percentage from them.
