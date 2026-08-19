# exclusive-gateway

Conformance micro-fixture: data-based exclusive choice with a default flow.

Start -&gt; script "set_betrag" (= 250 -&gt; betrag) -&gt; exclusive gateway. The gateway takes "f_high" when betrag &gt; 1000, otherwise the default "f_low". Seeded with betrag = 250, so the default branch fires deterministically and grade becomes "L". Realizes WCP-4 (Exclusive Choice) and WCP-5 (Simple Merge).

![exclusive-gateway diagram](../diagrams/exclusive-gateway.png)

- **Model:** [`exclusive-gateway.bpmn`](../../models/exclusive-gateway.bpmn)
- **Features:** `exclusive-gateway` (Data-based exclusive gateway with default flow), `script-task` (Inline FEEL script task (in-engine, no worker))
- **Control-flow patterns:** WCP-4 (Exclusive Choice), WCP-5 (Simple Merge)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `set_betrag` → `gw` → `low` → `end`
- **Variables:** `betrag = 250`, `grade = L`

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
- **Differential oracle** — the same case is run against an independent BPMN
  engine (Node's `bpmn-engine`) and both must agree on the outcome
  (opt-in: `go test -tags differential ./conformance/differential`).

## Run it yourself

- **Locally:** `go test ./conformance -run 'TestScenarios/exclusive-gateway'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/exclusive-gateway`](../../tck/cases/exclusive-gateway) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
