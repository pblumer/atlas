# data-object

Conformance micro-fixture: a first-class data object, written then read.

Start -&gt; script "seed" (amount = 100) -&gt; task "record" -&gt; task "load" -&gt; End. A &lt;dataObject&gt; "order" is seeded at instance creation with data state "received". The "record" task's data OUTPUT association writes it from the "amount" variable and, via a reference carrying data state "approved", advances its state received -&gt; approved. The "load" task's data INPUT association then reads the object back into the "order_copy" variable. Data flows variable -&gt; data object -&gt; variable, and the object's state advances along the way — the captured trace shows both order_copy=100 and order[approved]=100. The record and load tasks are pass-through (no execution semantics), so the model self-completes.

![data-object diagram](../diagrams/data-object.png)

- **Model:** [`data-object.bpmn`](../../models/data-object.bpmn)
- **Features:** `data-object` (First-class data object: output/input associations and data state)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `seed` → `record` → `load` → `end`
- **Variables:** `amount = 100`, `order_copy = 100`
- **Data objects:** `order[approved]=100`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/data-object'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/data-object`](../../tck/cases/data-object) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
