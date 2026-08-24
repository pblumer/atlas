# timer-catch

Conformance micro-fixture: an intermediate timer catch event.

Start -&gt; intermediateCatchEvent "wait" (duration PT30S) -&gt; End. The token parks until the timer is due. The driver advances the clock past the due date with Wait(31s) and fires due timers. Exercises the timer driver path.

![timer-catch diagram](../diagrams/timer-catch.png)

- **Model:** [`timer-catch.bpmn`](../../models/timer-catch.bpmn)
- **Features:** `timer-catch` (Intermediate timer catch event)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Advance the clock by `31s`, firing any timer that comes due.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `wait` → `end`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/timer-catch'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/timer-catch`](../../tck/cases/timer-catch) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
