# compensation

Conformance micro-fixture: compensation.

Start -&gt; serviceTask "charge" (compensable) -&gt; intermediate throw "cancel" -&gt; End "done". The "charge" activity carries a compensation boundary event linked by a BPMN &lt;association&gt; to the "refund" handler (isForCompensation). Once charge has completed, reaching the "cancel" throw compensates it: the engine runs the refund handler for the completed charge, and only when it finishes does the throw continue to "done". Driver: Complete("charge") then Complete("refund").

![compensation diagram](../diagrams/compensation.png)

- **Model:** [`compensation.bpmn`](../../models/compensation.bpmn)
- **Features:** `compensation` (Compensation via a boundary and a compensation throw)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Complete job `charge`.
2. Complete job `refund`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `charge` → `cancel` → `refund` → `done`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/compensation'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/compensation`](../../tck/cases/compensation) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
