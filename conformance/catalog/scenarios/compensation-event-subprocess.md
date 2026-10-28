# compensation-event-subprocess

Conformance micro-fixture: compensation thrown from an event subprocess, waiting for its handlers (ADR-draft-scope-aware-messaging-and-compensation, building on ADR-0103).

Start -&gt; serviceTask "charge" (compensable, handler "refund") -&gt; userTask "wait" -&gt; End, with an interrupting message event subprocess "cancellation" { message start -&gt; compensation throw (no activityRef, waitForCompletion by default) -&gt; script "bill" -&gt; End }. The throw sits in the event subprocess; the charge it compensates in the scope around it.

Driver: complete charge, publish the cancellation while "wait" is parked, complete the refund. The trace shows the refund before "bill": the throw compensates the enclosing scope and goes on only once the handler is done.

![compensation-event-subprocess diagram](../diagrams/compensation-event-subprocess.png)

- **Model:** [`compensation-event-subprocess.bpmn`](../../models/compensation-event-subprocess.bpmn)
- **Features:** `compensation-event-subprocess` (Compensation thrown from an event subprocess, waiting for its handlers)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Complete job `charge`.
2. Publish message `cancellation` (key `O1`).
3. Complete job `refund`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `cancellation` → `charge` → `wait` → `cancellation` → `cs` → `undo` → `refund` → `bill` → `ce`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/compensation-event-subprocess'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/compensation-event-subprocess`](../../tck/cases/compensation-event-subprocess) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
