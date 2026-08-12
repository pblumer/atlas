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

Runs are deterministic — the precondition for golden files. Self-completing
models (inline FEEL scripts) need no help; models that **park a token** carry a
`Driver`: an ordered list of steps that advance the wait deterministically. How
the instance is **born** is a separate axis — an explicit start by default, or a
message/timer start event that springs it from a trigger.

## The driver

A scenario's `Driver` is a list of steps, applied to the live run in order with a
`RunUntilIdle` after each. The steps only touch the live run — replay rebuilds
from the log the events they produced, so replay equivalence still holds
unchanged. Three step kinds cover the parking mechanisms:

| Constructor | Drives | Engine call |
|-------------|--------|-------------|
| `Complete("task", Str("k","v")…)` | a parked job (user or service task), writing outputs | `CompleteJob` |
| `Publish("msg", "corrKey", …)` | a waiting message subscription | `PublishMessage` |
| `Wait(30*time.Second)` | the clock past a timer's due date, firing it | clock advance + `TickTimers` |

`Complete` resolves the job by the task's BPMN id (job → element instance →
compiled element), and fails loudly if the id names no parked job or an ambiguous
one — a mis-authored step is a test error, not a wrong-token run.

For start events the instance has no `CreateInstance` at all; the scenario's
`Start` field says how it is born:

| Start | Births the instance by | Engine call |
|-------|------------------------|-------------|
| (zero value) | an explicit create — the default for a none start event | `CreateInstance` |
| `MessageStart("msg", "corrKey")` | publishing to a message start event | `PublishMessage` |
| `TimerStart(30*time.Second)` | arming the start timer and advancing the clock past it | `ArmStartTimers` + `TickTimers` |

## Running

```bash
go test ./conformance/                 # assert against the committed goldens
go test ./conformance/ -update         # regenerate goldens + COVERAGE.md
go test -race ./conformance/           # with the race detector
```

A regenerated golden is a **behavior change**: review the diff before committing.
`COVERAGE.md` is generated too — never hand-edit it; a test fails if it drifts.

## Adding a scenario

1. Drop a BPMN model under [`models/`](models/). If it parks a token, note which
   step advances each wait; otherwise make it self-completing with inline
   `<zeebe:script>` tasks. Fixtures intentionally omit BPMN-DI (they are executed,
   not rendered).
2. Register it in `Scenarios` in [`scenario.go`](scenario.go), listing the
   `Features` it exercises and, for a parking model, the `Driver` steps that carry
   it to an end. Add new `Feature`/`Pattern` entries if needed. For a metamorphic
   pair, give both members the same `EquivClass`.
3. `go test ./conformance/ -update` to mint the golden and refresh `COVERAGE.md`.
4. Review both generated files, then commit.

## What's next

The scaffold covers self-completing control flow plus the parking features the
driver reaches: user/service tasks, message and timer catch events, receive
tasks, interrupting/non-interrupting boundary events, the event-based gateway
(deferred choice), and message/timer start events. Planned extensions, roughly in
order:

- More driver reach: boundary **error/escalation/signal** events, **signal**
  broadcast, and job **failure/incident** steps (`FailJob` → `ResolveIncident`).
- **Negative models** that must be rejected at compile, not at runtime (the
  category `TestRunRejectsMalformedModel` stands in for today).
- Broader pattern coverage (inclusive gateways, subprocess, multi-instance,
  compensation) — each a row that flips from 🔲 to ✅ in `COVERAGE.md`.
- Optionally, a **differential** job comparing outcomes against a reference
  engine (Camunda/Zeebe) as the strongest external oracle.
