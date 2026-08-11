// Package conformance is Atlas's curated BPMN conformance suite: a collection of
// small, deterministic process models plus the oracles that prove the engine
// executes them correctly. It fills the Milestone-1 roadmap item "Conformance
// tests against a curated BPMN model set".
//
// The suite answers two separate questions that must not be conflated:
//
//   - Completeness — do we cover every BPMN execution feature? Tracked by an
//     explicit register (Features, Patterns, Scenarios) mapped onto the
//     externally recognized workflow control-flow patterns, so a feature with no
//     scenario is a visible gap in COVERAGE.md rather than a blind spot.
//   - Correctness — does the engine produce the right result, not merely a
//     self-consistent one? Checked by four layered oracles in the runner.
//
// The four oracles, weakest-to-strongest independence:
//
//  1. Golden trace. The engine is event-sourced, so a run's observable behavior
//     is its token path plus the variables it wrote. Each scenario's RunResult is
//     serialized and compared against a committed golden file. Regenerate with
//     `go test ./conformance -update`; a changed golden is a behavior change and
//     must be reviewed in the diff.
//  2. Replay equivalence. Every scenario is replayed from its own log into a
//     fresh store and must reproduce the live result byte-for-byte — the engine's
//     load-bearing invariant "state after replay == state built live" (I4),
//     exercised for free on every model.
//  3. Structural invariants. Model-independent properties that must hold for any
//     run: a completed instance leaves no orphan tokens, for example.
//  4. Metamorphic equivalence. Behaviorally equivalent models (e.g. independent
//     effects run concurrently vs. sequentially) must reach the same effect
//     projection despite different control-flow shapes — a correctness check that
//     needs no reference engine.
//
// Adding a scenario: drop a self-completing BPMN model under models/, register it
// in Scenarios (and any new Feature/Pattern), run `go test ./conformance -update`
// to mint its golden and refresh COVERAGE.md, and review both.
package conformance
