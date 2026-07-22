# ADR-0018: Test-driven development as the default workflow

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Core team

## Context and problem statement

Atlas's correctness rests on properties that are cheap to break and expensive to detect after the fact: recovery must reconstruct exactly the live state, the processor must stay allocation-free on the hot path, and `applyToState` must remain deterministic and side-effect-free. These are not properties a reviewer can eyeball reliably — they need executable checks that run on every change.

Until now the repository *required* tests for new behavior (see [`AGENTS.md`](../../AGENTS.md) and [`CONTRIBUTING.md`](../../CONTRIBUTING.md)) but did not say *when* they are written relative to the code. In practice this let tests be written after the fact, which tends to (a) test the implementation that happens to exist rather than the intended behavior, (b) leave error and recovery paths — exactly the risky ones — under-covered, and (c) make coverage regressions easy to miss. We want a workflow that pins behavior *before* it is implemented and keeps the safety net tight over time.

## Decision drivers

- The core correctness property — *state after replay == state built live* — must be expressed as a test that exists before the behavior it guards.
- Error, recovery, and edge paths must be covered as deliberately as happy paths; these are where defects hide in an event-sourced engine.
- A regression in coverage should be visible in review, not discovered months later.
- The workflow must not slow down obviously-trivial changes to the point of being ignored.

## Considered options

1. **Keep "tests required, timing unspecified."** Tests must exist to merge, but may be written after the code.
2. **Test-driven development (TDD) as the stated default:** write a failing test that specifies the behavior, make it pass, then refactor — with narrow, honest exceptions.
3. **Mandatory strict TDD with a per-change coverage floor enforced in CI**, no exceptions.

## Decision outcome

Chosen option: **TDD is the default workflow for Atlas (option 2).** For any change to engine behavior, persistence, recovery, the compiler, or a public API, the expected practice is:

1. **Red** — write a test that states the intended behavior and fails for the right reason. For anything that emits events, this includes a recovery/replay assertion.
2. **Green** — write the minimum production code to make it pass.
3. **Refactor** — clean up with the test as a safety net, keeping `go test -race ./...` green.

The bar for "done" is unchanged and still enforced by CI (`go build`, `go test -race`, `go vet`, `gofmt -l`); TDD is about the *order* of work, not an additional gate.

**Honest exceptions** (state them in the PR rather than pretending):

- Pure mechanical changes with no behavioral surface — renames, comment/doc edits, gofmt, dependency bumps.
- Spikes to explore a design — but the spike is thrown away and the work is re-done test-first before merge.
- Bug fixes start with a **failing regression test** that reproduces the bug; this is TDD, not an exception to it.

We deliberately did **not** adopt option 3 (a hard per-change coverage-delta gate). It rewards coverage theatre (tests that execute lines without asserting behavior) and punishes legitimately hard-to-reach defensive code. Instead we hold a **repository-wide statement-coverage floor of 95%**, checked in CI as a single number, leaving contributors free to argue that a specific unreachable branch isn't worth a contrived test.

### Consequences

- **Positive:** Behavior is pinned before it exists, so tests describe intent rather than implementation. Recovery and error paths get first-class coverage because they are written first. The 95% floor makes coverage regressions a visible CI failure instead of silent drift. New contributors have an unambiguous answer to "when do I write the test?".
- **Negative / trade-offs accepted:** Slightly more up-front effort per change, and occasional friction when a genuinely untestable-without-refactor path meets the coverage floor — handled by the stated-exception escape hatch, not by lowering the bar silently. A repo-wide floor can hide a poorly-covered new package behind well-covered old ones; reviewers still check that *new* code carries its own tests.
- **Follow-ups / risks to watch:** Wire the 95% floor into CI as an explicit check. Watch for coverage theatre in review — a covered line with no meaningful assertion is worse than an honest gap. Revisit the floor if it ever pushes contributors toward contrived tests instead of better design.

## Pros and cons of the options

### Keep "tests required, timing unspecified"
- Good: lowest process overhead; already the status quo.
- Bad: tests trail the code and mirror it; error/recovery paths stay thin; coverage drifts down unnoticed.

### TDD as the default (with exceptions + repo-wide floor)
- Good: behavior pinned first; risky paths covered deliberately; regressions visible; clear rule with honest escape hatches.
- Bad: more up-front effort; a global floor can mask a weak new package.

### Strict TDD + per-change coverage gate
- Good: strongest guarantee on paper.
- Bad: incentivizes assertion-free coverage theatre; punishes unreachable defensive code; high friction that invites gaming.

## Links

- reinforces the testing conventions in [`AGENTS.md`](../../AGENTS.md) and [`CONTRIBUTING.md`](../../CONTRIBUTING.md)
- guards the recovery property behind ADR-0001 (event sourcing) and the invariants in [`docs/architecture/invariants.md`](../architecture/invariants.md)
- the "one `applyToState`" invariant (ADR-0001) is the property TDD's recovery tests exist to protect
