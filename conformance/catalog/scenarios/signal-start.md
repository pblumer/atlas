# signal-start

Conformance micro-fixture: a signal start event.

Two processes. "on-signal" begins with a signal start event for "launch" (signalStart "on_launch" -&gt; script "handle" -&gt; End); deploying it arms the subscription. "thrower" is the trigger (Start -&gt; signal throw "launch" -&gt; End). The scenario instantiates the thrower; its broadcast births a fresh "on-signal" instance, which runs to completion. Root is "on-signal", so the captured trace is the signal-started instance's; the thrower instance is filtered out by definition key.

![signal-start diagram](../diagrams/signal-start.png)

- **Model:** [`signal-start.bpmn`](../../models/signal-start.bpmn)
- **Features:** `signal-start` (Signal start event (broadcast births an instance))

## How it is driven

**Start:** signal start — instantiating the `thrower` process broadcasts the signal that births the instance.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `on_launch` → `handle` → `end`
- **Variables:** `handled = 1`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/signal-start'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/signal-start`](../../tck/cases/signal-start) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
