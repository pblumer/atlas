# BPMN conformance suite

A curated collection of small BPMN models plus the oracles that prove Atlas
executes them correctly — the Milestone-1 roadmap item *"Conformance tests
against a curated BPMN model set"*. See the package doc in [`doc.go`](doc.go) for
the design rationale; this file is the how-to.

The suite keeps two questions apart:

- **Completeness** — *do we cover every BPMN execution feature?* Answered by an
  explicit register ([`scenario.go`](scenario.go)) mapped onto the recognized
  [workflow control-flow patterns](http://www.workflowpatterns.com/). A feature
  with no covering scenario shows up as a gap in [`COVERAGE.md`](COVERAGE.md), so
  "we cover everything" is a checkable claim, not a vibe.
- **Correctness** — *does the engine produce the right result, not merely a
  self-consistent one?* Answered by four layered oracles.

## The four oracles

| Oracle | What it proves | Where |
|--------|----------------|-------|
| **Golden trace** | The token path and variables a model produces match a reviewed baseline. | `golden/*.trace`, compared in `TestScenarios` |
| **Replay equivalence** | State rebuilt from the log alone equals the live state — invariant I4, on every model for free. | `Run` → `replayLog` |
| **Structural invariants** | Model-independent truths, e.g. a completed instance leaves no orphan tokens. | `executeLive` |
| **Metamorphic** | Behaviorally equivalent models (concurrent vs. sequential independent effects) reach the same effect projection despite different shapes — no reference engine needed. | `TestMetamorphic` |

Because the models are self-completing (inline FEEL scripts, no parked tokens),
every run is deterministic and reproducible — the precondition for golden files.

## Running

```bash
go test ./conformance/                 # assert against the committed goldens
go test ./conformance/ -update         # regenerate goldens + COVERAGE.md
go test -race ./conformance/           # with the race detector
```

A regenerated golden is a **behavior change**: review the diff before committing.
`COVERAGE.md` is generated too — never hand-edit it; a test fails if it drifts.

## Adding a scenario

1. Drop a **self-completing** BPMN model under [`models/`](models/) — drive it to
   an end event with inline `<zeebe:script>` tasks so no token parks. Fixtures
   intentionally omit BPMN-DI (they are executed, not rendered).
2. Register it in `Scenarios` in [`scenario.go`](scenario.go), listing the
   `Features` it exercises. Add new `Feature`/`Pattern` entries if needed. For a
   metamorphic pair, give both members the same `EquivClass`.
3. `go test ./conformance/ -update` to mint the golden and refresh `COVERAGE.md`.
4. Review both generated files, then commit.

## What's next

The scaffold covers self-completing control flow. The planned extensions, roughly
in order:

- A **driver** so scenarios can exercise parking features (user/service tasks,
  timers, messages) deterministically — inject the clock, feed fixed inputs and
  job completions, capture the trace across the wait.
- **Negative models** that must be rejected at compile, not at runtime (the
  category `TestRunRejectsMalformedModel` stands in for today).
- Broader pattern coverage (inclusive gateways, event-based gateway, boundary
  events, subprocess, multi-instance, compensation) — each a row that flips from
  🔲 to ✅ in `COVERAGE.md`.
- Optionally, a **differential** job comparing outcomes against a reference
  engine (Camunda/Zeebe) as the strongest external oracle.
