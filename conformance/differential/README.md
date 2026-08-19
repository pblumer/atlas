# Differential oracle

The conformance suite's four other oracles — golden traces, replay equivalence,
structural invariants, metamorphic equivalence — all trust Atlas alone: they prove
Atlas is *self-consistent*, not that it is *right*. This one runs the same process
on an **independent** BPMN engine and asserts the outcomes agree, which is the only
check that can catch a bug where Atlas is confidently, consistently wrong.

## What it compares

The reference engine is [`bpmn-engine`](https://github.com/paed01/bpmn-engine), a
mature, independent JavaScript BPMN 2.0 executor. For each scenario the suite runs
Atlas and the reference and compares a **normalized projection**:

- **did the instance complete**, and
- **the set of activities that ran** (deduplicated, sorted).

Variables are deliberately excluded — engines format values differently (Atlas
FEEL vs. the reference's JavaScript), so the reliable cross-engine signal is the
control-flow set, not the data. The activity set is what proves the interesting
semantics: which gateway branch was taken, that a parallel join synchronized, that
an inclusive split opened exactly the right branches and suppressed the default.

## Why only a subset

BPMN portability stops at the executable extensions. Atlas speaks the `zeebe:`
dialect and FEEL; no other engine does. So each reference model under
[`reference/models/`](reference/models/) is a **hand-translation** that keeps the
same element ids and control flow but expresses scripts and conditions in the
reference's dialect (JavaScript). Only pure control-flow scenarios are translated so
far:

| Scenario | Semantics cross-checked |
|----------|-------------------------|
| `sequence` | plain token flow |
| `exclusive-gateway` | data-based XOR + default flow |
| `parallel-independent` | AND fork and synchronizing join |
| `inclusive-gateway` | OR multi-choice split + synchronizing merge |

Translating the parking, boundary, and structural scenarios (jobs, timers,
messages, subprocess, …) is the way to grow this list — each needs a faithful
reference encoding and a driver the reference can replay.

The control-flow independence holds even though the inline scripts are ours: the
reference evaluates our value-computing scripts in a `vm` sandbox, but **its own
engine decides every routing, fork, and join** — that is the independence the
oracle rests on.

## Running it

The live comparison is behind the `differential` build tag, so the default
`go test ./...` and the repo-wide coverage gate never need Node.

```bash
cd conformance/differential/reference && npm ci && cd -
go test -tags differential ./conformance/differential/
```

Without the tag, `go test ./conformance/differential/` runs only the pure
projection unit tests (no Node required) — those are what the coverage gate sees.

## Adding a reference scenario

1. Hand-translate the Atlas model under [`reference/models/`](reference/models/),
   keeping every element id identical and expressing scripts/conditions as
   JavaScript (`environment.variables.x = …; next();`, conditions
   `next(null, <bool>);`).
2. Add the Atlas-scenario-name → reference-file entry to `subset` in
   `differential_test.go`.
3. `go test -tags differential ./conformance/differential/` and confirm agreement.
   A mismatch is either a translation bug or a real divergence worth investigating.
