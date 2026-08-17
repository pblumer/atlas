# standard-loop

Conformance micro-fixture: a BPMN standard loop (ADR-0131).

Start -&gt; script "seed" (n = 0) -&gt; script task "step" marked with a standard loop -&gt; End. "step" recomputes n = n + 1 and repeats while its &lt;loopCondition&gt; (n &lt; 3) holds, so it runs three times and the token leaves with n = 3 — the loop's own work is what ends it, which is the whole point of the marker. It is self-completing (inline FEEL, no worker), so no driver step is needed, and the trace shows the repetition directly: one activation of the loop body plus one per run.

Carries hand-authored BPMN-DI so it renders cleanly in the catalog; the layout is diagram-only and does not affect execution (the compiler ignores it).

![standard-loop diagram](../diagrams/standard-loop.png)

- **Model:** [`standard-loop.bpmn`](../../models/standard-loop.bpmn)
- **Features:** `standard-loop` (Standard loop activity (repeat while a condition holds))
- **Control-flow patterns:** WCP-21 (Structured Loop)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `seed` → `step` → `step` → `step` → `step` → `end`
- **Variables:** `n = 3`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/standard-loop'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/standard-loop`](../../tck/cases/standard-loop) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
