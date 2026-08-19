# boundary-timer-interrupting

Conformance micro-fixture: an interrupting boundary timer.

Start -&gt; serviceTask "work" -&gt; End "done", with an interrupting boundary timer (PT30S) on "work" routing to End "escalated". The token parks on the service job; when the timer comes due it interrupts the host (its job is discarded) and the token leaves via the boundary event to "escalated". Driver: Wait(31s) — the host job is never completed, so reaching "escalated" is the whole point.

![boundary-timer-interrupting diagram](../diagrams/boundary-timer-interrupting.png)

- **Model:** [`boundary-timer-interrupting.bpmn`](../../models/boundary-timer-interrupting.bpmn)
- **Features:** `boundary-timer-interrupting` (Interrupting boundary timer event)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Advance the clock by `31s`, firing any timer that comes due.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `work` → `timeout` → `escalated`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/boundary-timer-interrupting'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/boundary-timer-interrupting`](../../tck/cases/boundary-timer-interrupting) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
