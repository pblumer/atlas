# data-object-fields

Conformance micro-fixture: field-level data-object writes (ADR-0060).

Start -&gt; task "set_id" -&gt; task "set_total" -&gt; End. The &lt;dataObject&gt; "order" starts unset. Each pass-through task's data output association targets a single member via &lt;assignment&gt;&lt;to&gt;: "set_id" writes order.id, "set_total" writes order.total. The engine reads the object's current JSON, sets that one member, and writes the merged value back — so the object accrues field by field, and the first member write creates it from unset. The captured trace shows the merged order={"id":"ORD-1","total":100}. Self-completing (pass-through tasks).

![data-object-fields diagram](../diagrams/data-object-fields.png)

- **Model:** [`data-object-fields.bpmn`](../../models/data-object-fields.bpmn)
- **Features:** `field-level-data-object` (Field-level data-object writes (accrue members))

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `set_id` → `set_total` → `end`
- **Data objects:** `order={"id":"ORD-1","total":100}`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/data-object-fields'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/data-object-fields`](../../tck/cases/data-object-fields) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
