# parallel-independent

Conformance micro-fixture: parallel fork/join over two independent effects.

Start -&gt; parallel fork -&gt; (set_x: = 1 -&gt; x) &amp; (set_y: = 2 -&gt; y) -&gt; parallel join -&gt; End. Realizes WCP-2 (Parallel Split) and WCP-3 (Synchronization).

Metamorphic partner of linear-independent.bpmn: two independent effect tasks run concurrently here and sequentially there, so the two models must reach the same effect projection (final variables + terminal state) despite different control-flow shapes. See the "equivalence" oracle in the runner.

![parallel-independent diagram](../diagrams/parallel-independent.png)

- **Model:** [`parallel-independent.bpmn`](../../models/parallel-independent.bpmn)
- **Features:** `parallel-gateway` (Parallel fork and synchronizing join), `script-task` (Inline FEEL script task (in-engine, no worker))
- **Control-flow patterns:** WCP-2 (Parallel Split), WCP-3 (Synchronization)
- **Metamorphic class:** `independent-effects`

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `fork` → `set_x` → `set_y` → `join` → `join` → `end`
- **Variables:** `x = 1`, `y = 2`

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
- **Metamorphic equivalence** — this scenario is in the `independent-effects` group; it must
  produce the same observable effect as `linear-independent`, even though the models differ.
- **Differential oracle** — the same case is run against an independent BPMN
  engine (Node's `bpmn-engine`) and both must agree on the outcome
  (opt-in: `go test -tags differential ./conformance/differential`).

## Run it yourself

- **Locally:** `go test ./conformance -run 'TestScenarios/parallel-independent'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/parallel-independent`](../../tck/cases/parallel-independent) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
