# boundary-error

Conformance micro-fixture: an interrupting boundary error event.

Start -&gt; serviceTask "call" -&gt; End "done", with a boundary error event catching error code "BOOM" on "call" and routing to End "handled". The driver plays a worker that throws the business error (ThrowError) instead of completing the job; the boundary catches it, interrupts the host, and the token leaves via the error path to "handled". Driver: ThrowError("call", "BOOM").

![boundary-error diagram](../diagrams/boundary-error.png)

- **Model:** [`boundary-error.bpmn`](../../models/boundary-error.bpmn)
- **Features:** `boundary-error` (Interrupting boundary error event)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Throw business error `BOOM` from job `call` so a boundary error event catches it.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `call` → `on_error` → `handled`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/boundary-error'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/boundary-error`](../../tck/cases/boundary-error) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
