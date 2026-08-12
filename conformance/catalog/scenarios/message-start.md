# message-start

Conformance micro-fixture: a message start event.

A message named "order-placed" starts a new instance: messageStart "on_order" -&gt; script "record" -&gt; End. There is no CreateInstance call — the instance is born from the message. The suite's Start hook publishes it with MessageStart("order-placed", ""); the inline script then self-completes the run.

![message-start diagram](../diagrams/message-start.png)

- **Model:** [`message-start.bpmn`](../../models/message-start.bpmn)
- **Features:** `message-start` (Message start event)

## How it is driven

**Start:** message start — publishing `order-placed` births the instance.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `on_order` → `record` → `end`
- **Variables:** `recorded = 1`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/message-start'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/message-start`](../../tck/cases/message-start) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
