# subprocess

Conformance micro-fixture: an embedded subprocess.

Start -&gt; subProcess "sub" { inner_start -&gt; script "inner_work" -&gt; inner_end } -&gt; End. A token enters the subprocess, runs its inner flow to the inner end event, then leaves the subprocess and continues. The inner script is in-engine, so the whole thing self-completes.

![subprocess diagram](../diagrams/subprocess.png)

- **Model:** [`subprocess.bpmn`](../../models/subprocess.bpmn)
- **Features:** `embedded-subprocess` (Embedded subprocess)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `sub` → `inner_start` → `inner_work` → `inner_end` → `end`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/subprocess'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/subprocess`](../../tck/cases/subprocess) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
