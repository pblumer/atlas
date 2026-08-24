# user-task

Conformance micro-fixture: a human task that parks a token.

Start -&gt; userTask "approve" -&gt; End. A user task parks a job (its "worker" is a human); the token waits until the job is completed. The driver completes it with Complete("approve"). Exercises the job-completion driver path against a user task.

![user-task diagram](../diagrams/user-task.png)

- **Model:** [`user-task.bpmn`](../../models/user-task.bpmn)
- **Features:** `user-task` (User task (human-completed job))

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Complete job `approve`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `approve` → `end`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/user-task'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/user-task`](../../tck/cases/user-task) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
