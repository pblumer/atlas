# receive-task

Conformance micro-fixture: a receive task (a message wait modeled as an activity).

Start -&gt; receiveTask "await_reply" -&gt; End. Semantically like an intermediate message catch, but an activity — it can carry boundary events and I/O mappings. The token parks on the subscription (name "reply", constant correlation key "K") until a matching message arrives. Driver: Publish("reply", "K").

![receive-task diagram](../diagrams/receive-task.png)

- **Model:** [`receive-task.bpmn`](../../models/receive-task.bpmn)
- **Features:** `receive-task` (Receive task (message wait as an activity))

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Publish message `reply` (key `K`).

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `await_reply` → `end`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/receive-task'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/receive-task`](../../tck/cases/receive-task) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
