# service-task

Conformance micro-fixture: a worker-backed service task that parks a token.

Start -&gt; serviceTask "charge" (job type "payment") -&gt; End. The token parks on the job until an external worker completes it. The driver plays the worker with Complete("charge", Str("status", "captured")), and the output variable flows into the instance scope. Exercises the job-completion driver path with outputs.

![service-task diagram](../diagrams/service-task.png)

- **Model:** [`service-task.bpmn`](../../models/service-task.bpmn)
- **Features:** `service-task` (Service task (worker-completed job with outputs))

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Complete job `charge` with `status = captured`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `charge` → `end`
- **Variables:** `status = captured`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/service-task'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/service-task`](../../tck/cases/service-task) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
