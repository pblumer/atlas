# call-activity

Conformance micro-fixture: a call activity invoking a child process.

Two processes in one model. The root "call-parent" runs Start -&gt; callActivity "call" -&gt; End; reaching the call activity creates an instance of "call-child" (Start -&gt; script -&gt; End) and waits for it. The child is in-engine, so it self-completes, and the parent's call activity then completes. The scenario's Root is "call-parent"; the trace captured is the parent's, and the child instance is filtered out by definition key.

![call-activity diagram](../diagrams/call-activity.png)

- **Model:** [`call-activity.bpmn`](../../models/call-activity.bpmn)
- **Features:** `call-activity` (Call activity invoking a child process)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `call` → `end`
- **Variables:** `child_done = 1`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/call-activity'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/call-activity`](../../tck/cases/call-activity) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
