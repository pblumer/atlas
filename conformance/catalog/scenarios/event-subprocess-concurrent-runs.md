# event-subprocess-concurrent-runs

Conformance micro-fixture: concurrent runs of one non-interrupting event subprocess, each correlating on its own local key (ADR-draft-scope-aware-messaging-and-compensation).

Start -&gt; userTask "work" -&gt; End, with a non-interrupting message event subprocess "finding" whose every run maps the finding's id into its own scope, waits at a receive task for the answer to that finding (correlation key "=findingId", evaluated in the run's scope), and does the extra work only when the answer, declared locally as null, says yes.

Driver: two findings back to back (both runs must start, each with its own id), the answer "yes" to the second, "no" to the first, then the main work. The trace shows the second run doing the extra work and the first one declined — the outcome a key read on the instance root, or an answer written behind the local null, gets wrong.

![event-subprocess-concurrent-runs diagram](../diagrams/event-subprocess-concurrent-runs.png)

- **Model:** [`event-subprocess-concurrent-runs.bpmn`](../../models/event-subprocess-concurrent-runs.bpmn)
- **Features:** `event-subprocess-concurrent-runs` (Concurrent runs of a non-interrupting event subprocess, each correlating on its own local key)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Publish message `finding` (key `O1`) with `findingId = F1`.
2. Publish message `finding` (key `O1`) with `findingId = F2`.
3. Publish message `answer` (key `F2`) with `approved = yes`.
4. Publish message `answer` (key `F1`) with `approved = no`.
5. Complete job `work`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `finding` → `work` → `finding` → `finding` → `found` → `await_answer` → `finding` → `finding` → `found` → `await_answer` → `approved_q` → `extra` → `extra_done` → `approved_q` → `declined` → `end`
- **Variables:** `findingId = F2`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/event-subprocess-concurrent-runs'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/event-subprocess-concurrent-runs`](../../tck/cases/event-subprocess-concurrent-runs) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
