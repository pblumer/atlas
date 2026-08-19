# signal-throw-catch

Conformance micro-fixture: a signal broadcast within one instance.

Start -&gt; parallel fork -&gt; two branches that rendezvous on a signal: A: intermediate signal catch "abort" -&gt; join B: script "prep" -&gt; intermediate signal throw "abort" -&gt; join When branch B's throw is reached it broadcasts "abort", firing branch A's armed catch; the join then synchronizes and the instance ends. The script on the throw branch keeps the throw one hop behind the catch's arming, so the catch is always subscribed before the broadcast — self-completing, no driver needed.

![signal-throw-catch diagram](../diagrams/signal-throw-catch.png)

- **Model:** [`signal-throw-catch.bpmn`](../../models/signal-throw-catch.bpmn)
- **Features:** `signal` (Signal throw and catch (1:n broadcast))

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `fork` → `await_abort` → `prep` → `raise_abort` → `join` → `join` → `end`
- **Variables:** `ready = 1`

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

- **Locally:** `go test ./conformance -run 'TestScenarios/signal-throw-catch'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/signal-throw-catch`](../../tck/cases/signal-throw-catch) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
