# multi-instance

Conformance micro-fixture: a parallel multi-instance activity.

Start -&gt; script "seed" (items = [10, 20, 30]) -&gt; multi-instance script "double" -&gt; End. The multi-instance runs the body once per element of the input collection, binding each element to "item"; each iteration's inline script doubles it, and the outputElement aggregates into the "results" collection. The body is in-engine, so all iterations self-complete and the parallel join fires once they all finish.

![multi-instance diagram](../diagrams/multi-instance.png)

- **Model:** [`multi-instance.bpmn`](../../models/multi-instance.bpmn)
- **Features:** `multi-instance` (Parallel multi-instance activity with output collection)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `seed` → `double` → `double` → `double` → `double` → `end`
- **Variables:** `items = [10,20,30]`, `results = [20,40,60]`

## What is verified

The outcome above is not asserted once — it is cross-checked by several
independent oracles that must all agree, so a wrong result cannot slip through:

- **Golden trace** — the exact path, variables, and data objects above are
  compared byte-for-byte against a committed golden file; any drift fails the suite.
- **Replay equivalence (invariant I4)** — the scenario runs live, then replays
  from its event log; both must reach an identical state, proving recovery is
  deterministic and loses nothing.
- **Structural invariants** — the finished run must leave no orphan tokens and
  honor the engine's six state invariants (see `docs/architecture/invariants.md`).

## Run it yourself

- **Locally:** `go test ./conformance -run 'TestScenarios/multi-instance'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/multi-instance`](../../tck/cases/multi-instance) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
