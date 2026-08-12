# sequence

Conformance micro-fixture: a plain sequence.

Start -&gt; script "set_a" (= 1 -&gt; a) -&gt; End. The smallest self-completing model: one inline FEEL script means the token never parks, so an instance runs straight to the end event with no worker attached. Realizes WCP-1 (Sequence).

Carries hand-authored BPMN-DI so it renders cleanly in the catalog; the layout is diagram-only and does not affect execution (the compiler ignores it).

![sequence diagram](../diagrams/sequence.png)

- **Model:** [`sequence.bpmn`](../../models/sequence.bpmn)
- **Features:** `start-end-event` (None start and end events), `sequence-flow` (Sequence flow between activities), `script-task` (Inline FEEL script task (in-engine, no worker))
- **Control-flow patterns:** WCP-1 (Sequence)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `set_a` → `end`
- **Variables:** `a = 1`

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
- **Differential oracle** — the same case is run against an independent BPMN
  engine (Node's `bpmn-engine`) and both must agree on the outcome
  (opt-in: `go test -tags differential ./conformance/differential`).

## Run it yourself

- **Locally:** `go test ./conformance -run 'TestScenarios/sequence'`
- **Portable case:** the language-neutral form lives in [`../../tck/cases/sequence`](../../tck/cases/sequence) (`model.bpmn` + `case.json` + `expected.json`), so another engine can replay it without reading Go.

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
