# ADR-0018: Test-driven development as the default workflow

- **Status:** Accepted (amended 2026-09-09 and 2026-09-16 — the floor stands at 94%; see the amendments below)
- **Implementation:** Landed
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

## Amendment (2026-09-09): the floor stands at 94%, and the number it checks was wrong

Two things came to light together, and only one of them is about the floor.

**The measurement was not reproducible.** `check-coverage.sh` summed every line of the
merged coverage profile, and a merged profile may list the same block more than once —
so those statements were counted twice on both sides of the ratio. It is not
hypothetical: two runs over an unchanged tree reported 95.0062% (39610/41692) and
95.0132% (39706/41790), the entire difference being 36 blocks of one package appearing
twice in the second profile. Counted per block, both runs report 95.0062%. The script
now keys its sums by block, which is the arithmetic this record always meant.

**The margin is two statements.** With the number stable, the repository sits at
95.0062% — two covered statements above its own floor, out of 41692. That is not a
floor any more. The next merge that brings a handful of uncovered lines turns `make
cover` red, and it does so on whichever change happens to be next rather than on the
one that spent the margin; the pressure that creates is to write something that
executes the lines and asserts nothing, which is precisely the coverage theatre the
decision above rejects. This record's own follow-up anticipated it: *revisit the floor
if it ever pushes contributors toward contrived tests instead of better design.*

So the floor is 94% until the gap is closed — about 420 statements of headroom, enough
that a failure again means somebody actually dropped the net rather than that they
merged on the wrong day.

**What has not changed:** test-driven development is still the default, tests are still
written first, and 95% is still what this repository intends to hold. The floor is the
alarm, not the target, and an alarm that fires on the innocent gets ignored.

**What raises it back:** the uncovered blocks are not spread evenly — `api/` carries
roughly 300 of them, ahead of `engine/` (50) and `playground/` (40). When the repo-wide
number holds comfortably above 95% again — say thirty statements of margin, sustained
across a few merges — the floor goes back to 95 in one line, and this amendment records
that it was always meant to.

## Amendment (2026-09-16): the 95% plan does not survive contact with the measurement

The previous amendment lowered the floor to 94% to buy roughly 420 statements of
headroom, and said what would raise it back:

> When the repo-wide number holds comfortably above 95% again — say thirty statements
> of margin, sustained across a few merges — the floor goes back to 95 in one line.

**That headroom lasted seven days.** On 2026-09-16 the repository sat at 94.0714%
(47 237/50 214) — 35 statements of margin — having been at 95.0062% (39 610/41 692) a
week earlier.

### Why, measured

```
statements added since the last amendment : 8 522
of which covered                          : 7 627
→ new code arrived at 89.5% coverage, against a repo average of ~94–95%
```

In that window: 289 commits, +31 290 lines of non-test Go, **+42 740 lines of test
Go**. A test-to-code ratio of 1.37:1. This is not a repository that stopped writing
tests; the aggregate falls anyway, because anything arriving below the average pulls
the average down. The floor then fails whichever change happens to cross the line,
not the one that spent the margin — the same unfairness the last amendment named.

### What is actually uncovered

`api` carries 1 586 of the repository's 2 977 uncovered statements. Classifying its
1 213 uncovered blocks by what the code *is*:

| statements | share | kind |
|---:|---:|---|
| 810 | 51.1% | `if err != nil { … }` — needs fault injection to reach |
| 398 | 25.1% | other guards, mostly `if x, err = store.Load(); err != nil` |
| 201 | 12.7% | HTTP error responses |
| 78 | 4.9% | other |
| 54 | 3.4% | switch/case arms |
| 40 | 2.5% | bare returns in comparators |
| **5** | **0.3%** | **whole uncovered function bodies** |

Repository-wide there are **14 functions at 0% coverage, about 77 statements**.
Reaching 94.5% needs ~216; 95% needs ~466.

**So the 95% target is not reachable by testing behaviour.** It is reachable only by
fault-injecting several hundred error paths — the coverage theatre the decision above
rejects. Go's `if err != nil` idiom adds roughly two statements per I/O call, and a
codebase that grows by 8 500 statements of which a large share is error plumbing
arrives near 89% however well its behaviour is tested. The number the floor watches
drifts down as a function of growth, not of diligence.

### What this amendment changes

**The genuinely untested functions are covered**, as ordinary work rather than to
move a number: `state.Tx.SetVariableIndexed` (three branches, one of them the replay
no-op the recovery property needs), `api.scalarOf` (a number keeps its exact text, so
10.10 does not come back as 10.1), the discrepancy actions' shared refusals,
`api.reconcileTooManyObservations`, `compiler.Builder.SetAdHocResultCollection`,
`mcp.WithTLSRoots` (it rebuilds the http.Client, so the timeout lives or dies by one
field being copied), `model.VariableIndexValue.ValueType`.

Measured on this tree: 47 274/50 215 = 94.1432%, **margin 71** — up from 35.

Four of the fourteen are deliberately left, which is the "state it rather than
pretend" the decision above asks for:

| left | why |
|---|---|
| `connector/script.runSandbox`, `CheckSandboxLanguages` | need a real Linux sandbox; a stub would assert the stub |
| `engine.Processor.Partition`, `api/infomodel.Service.ModelsOnLoop` | pure delegation — a test would assert that a field is returned |

The floor stays at 94%. This amendment does not lower it again — a second lowering in
seven days would make it a ratchet rather than a floor — and it does not raise it,
because the measurement above says 95% is not honestly reachable.

### Three options measured and **not** taken

**Crediting cross-package execution with `-coverpkg`.** Without it Go instruments only
the package under test, so a library function exercised through an integration test
elsewhere reads as never run: `compiler.BundleBoundDecisions` reports 0% while three
covered call sites in `api` use it on every deploy. That undercount is real. Passing
`-coverpkg` over the same package list nevertheless makes the number **worse**, and
the arithmetic says why:

```
                 without -coverpkg      with -coverpkg
covered              47 274                47 394      (+120 credited cross-package)
statements           50 215                50 399      (+184 added to the denominator)
coverage            94.1432%              94.0376%
margin                  71                    18
```

`-coverpkg` counts every statement of every listed package in every test binary's
profile — including packages that have no test files of their own and therefore
previously contributed nothing at all. This repository has enough of those
(`benchmarks`, `examples`, `conformance/differential`, the connector mocks) that they
outweigh what cross-package credit gains. Narrowing the list to packages that do have
tests would recover the gain, and would be gaming the denominator, which is worse than
the undercount.

Recorded here with numbers so the next person does not have to rediscover it. It is
also a caution about the measurement itself: an earlier run of this comparison
reported 94.34% *for* `-coverpkg`, and that run had aborted on a failing test, so the
failing package's statements never entered the merged profile and flattered the
result. A coverage number from a run that did not finish is not a coverage number.

**Excluding pure error-propagation blocks** (`if err != nil { return …err }` with a
single-statement body). Measured: 1 475 statements repo-wide, **66.5% of them already
covered**; excluding them from both sides moves 94.0714% → 94.9055%. Worth 0.83
points, still short of 95, and it buys that by making a bug inside an error branch
invisible to the metric while adding a source-parsing classifier to maintain.

**A ratchet on the aggregate** (coverage may never fall). That is the per-change delta
gate this record rejected, wearing an aggregate's clothes, and it still never names
the code that is untested.

**The open question is the drift, and it is a process question, not a script one.**
Nothing here changes the rate at which new code arrives covered, so the margin will
erode again. Tracked in [issue #979](https://github.com/pblumer/atlas/issues/979).

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
