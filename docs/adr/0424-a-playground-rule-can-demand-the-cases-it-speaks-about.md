# ADR-0424: A Playground rule can demand the cases it speaks about

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-28
- **Deciders:** Patrick Blumer
- **Open question:** Whether a rule whose `when` reads a variable no case carried should fail by
  default — the way a queue bound on a pool the run does not have already fails — rather than
  only when the rule asks for a minimum. Deferred until the minimum and the report of unread
  names below have been in use, because that report is what shows how often it would fire, and
  on what.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0215](0215-modeler-playground.md) made a Playground rule a pair of FEEL expressions — a
`when` that selects cases and a `then` they have to show — and decided three things about how
one is judged. The first is the one this record is about:

> A `when` that does not evaluate to true does not select the case. […] it is why a rule
> naming a variable no case carries selects nothing rather than failing everything. The
> outcome reports the matched count, so a rule that selected nothing says so instead of
> passing quietly.

It says so, and it still passes. `RuleOutcome.Passed` (`playground/rules.go`) is
`Violated == 0`, and a rule that selected nothing has violated nothing. Reproduced on
`0.7.0-dev`, 200 generated cases, seed 42:

| rule | verdict | `atlas playground` |
|---|---|---|
| `when dauer > 30` · `then end = "abgelehnt"` | `109 of 109 held` — passed | exit 0 |
| `when duaer > 30` · `then end = "abgelehnt"` | `no case of 200 matched` — passed | exit 0 |

The second rule checks nothing and cannot fail. Its only trace is a sentence in a report nobody
opens while the build is green — and the build is where ADR-0215 put the runner, so that "one
thing decides whether a build goes red". A rule that has gone vacuous is invisible to exactly
that thing.

A rule goes vacuous in more ways than one typo:

1. a misspelt name in `when` (`duaer`);
2. a name the model renamed after the rule was written (`dauer` → `dauerTage`) — the drift a
   scenario exists to catch, silenced by the change it should catch;
3. a misspelt value (`status = "Abgelehnt"` against `"abgelehnt"`);
4. a dataset that holds no such case (generated `dauer` from 1 to 30, a rule about more than 30);
5. a misspelt name in a `then` stated negatively. A misspelt name reads as null, and
   `freigegebn != false` is **true** for null, as is `not(freigegebn = false)` — measured with
   `expr.CompileAuto`. The same typo in a positive statement, `freigegebn = true`, is false,
   so every case violates the rule and it fails loudly. Only the negative form hides.

The same package already refuses the quiet version of this mistake one screen further down.
`Expectations.Judge` (`playground/expect.go`) fails a queue bound on a pool the run does not
have — `no such pool in this run` — and says why:

> A bound on a pool the run does not have is a mistake in the scenario, not a silent pass. It
> usually means the pool was renamed and the assertion was left behind — exactly when an
> assertion must not go quiet.

A rule naming a variable no case carries is that mistake with a different noun, and it is
treated the opposite way.

One property of the Playground makes this sharper than it looks. A saved scenario is
reproducible: the same dataset, settings and seed produce the same report (ADR-0215). Whether a
rule selects any case is therefore not luck but a fixed fact of the scenario. A rule that
selects nothing today selects nothing on every run until the scenario or the model changes. It
is not a test that happens to be quiet; it is a test that cannot fail.

The question: **how does a scenario say "this rule has to speak about at least N cases", so
that a rule that has gone vacuous fails the verdict — and how does the verdict say why —
without changing what existing scenarios already judge?**

## Decision drivers

- **A test that cannot fail must not pass as one.** The runner's exit status is the product; a
  green build that includes a vacuous rule states something false.
- **One judge.** The Modeler's panel and `atlas playground` send the same bodies to the same
  endpoints and must read the same verdict (ADR-0215). Strictness that lives only in the runner
  is a second answer to one question.
- **No silent change to existing verdicts.** Scenarios are stored and run in pipelines. A server
  upgrade must not turn a green build red without a change to the scenario or the model.
- **Say why, not only that.** "No case matched" sends a reader looking; the likely cause — a
  name no case carries — is known to the judge and should be said.
- **Inside the sandbox.** This is the Playground's judge and nothing else: nothing reaches the
  engine, the log, `applyToState` or any of the six invariants.

## Considered options

1. **Keep the rule as it is** — the handbook's test chapter documents the trap, and advises
   watching every new rule go red once.
2. **A minimum per rule**, `minMatched`, stored with the scenario and judged by the server;
   omitted means 0, which is today's judgement.
3. **A runner flag**, `atlas playground --require-matches`, failing any rule that matched nothing.
4. **Flip the default**: a rule that selects nothing fails unless it says it may.
5. **Refuse unread names**: a rule whose `when` reads a variable no case carried fails by
   default, as a bound on an unknown pool does.

## Decision outcome

Chosen option: **2, with the cause reported** — a rule may state `minMatched`, the fewest cases
its `when` has to select; the verdict names every variable a rule reads that no case carried;
and the Modeler writes `minMatched: 1` into every rule it adds. Existing scenarios judge exactly
as before.

### The minimum

- **Wire.** `rules[].minMatched`, a non-negative integer in the `expect` body of
  `POST /api/v1/playground/sessions/{id}/verdict` — and therefore in a saved scenario and in a
  `--file`. Omitted or 0 is today's judgement.
- **Domain.** `playground.Rule` gains `MinMatched`; `RuleOutcome.Passed()` becomes
  `Violated == 0 && Matched >= Rule.MinMatched`.
- **It counts matched cases, not decided ones.** An unfinished case the `when` selected counts.
  ADR-0215 left such a case undecided so that one problem is not reported twice, under a name
  that does not describe it; the completion expectation reports it, and the minimum must not
  report it again.
- **A negative minimum is refused where rules are refused** — in `compileRules`, with the
  rule's number, as ADR-0392 has every rule refused at the one door that already refuses. A
  minimum above the number of cases is not refused: it is a verdict (the run was too small),
  not a malformed rule.
- **The check** keeps one row per rule. Its `want` reads `every matching case, at least N`;
  its `got` is today's sentence, plus the shortfall when there is one —
  `no case of 200 matched, fewer than 1`.

### The cause

- **What is reported.** `JudgeRules` already puts every case's variables into a scope; it also
  collects the names it saw across all cases. For each half of each rule, the names it reads —
  `expr.Compiled.Inputs()`, the FEEL compiler's own list of free variables, so loop-bound
  names, path members and built-ins never appear — minus `end` and `durationSeconds`, minus
  every name some case carried, are the rule's **unread** names: `unreadWhen` and `unreadThen`
  on the outcome and in the verdict response.
- **It never changes a verdict.** It explains one. The panel shows it beside the rule;
  `atlas playground` prints it under the rule's check line (`     duaer is carried by no case`).
- **Both halves, labelled.** An unread name in the `when` is almost always a mistake (causes 1
  and 2). An unread name in the `then` is either the negated typo of cause 5 or an assertion of
  absence (`fehler = null`) doing its job. Both are reported, because the report states a fact
  the reader can judge — and it is the only thing in this record that reaches cause 5 at all.

### The Modeler

- **`Add a rule` writes `minMatched: 1`.** The row shows it as `at least [1] cases`; setting it
  to 0 is visible and deliberate. A rule opened from an existing scenario keeps what it has —
  no minimum if it had none. This is the half of the decision that makes the protection the
  normal case for new work, without making it the server's default for old work.

### The runner

- **The verdict response echoes each rule's `minMatched`.** When the scenario asks for a minimum
  and the verdict does not echo it, the server predates this record and ignored the field —
  the service's JSON decoder (`decode` in `api/playground/service.go`) skips a field it does
  not know. `atlas playground` then stops with status 1, `the server does not judge
  minMatched`, instead of reporting a pass the server never checked. The runner in a pipeline
  can be newer than the test instance it talks to, and a silently weaker verdict is the failure
  this record exists to end.

### Consequences

- **Positive:** a rule that has gone vacuous can be made to fail, and does by default for every
  rule written in the Modeler from now on. The failure names its most likely cause. The minimum
  also states coverage beyond "at least one" — "at least 20 cases over 30 days" — which the
  run-wide bounds cannot say about a rule's population. Nothing is migrated: the `expect` body
  is stored opaquely (ADR-0215), and existing scenarios judge exactly as before.
- **Negative / trade-offs accepted:** on the wire the minimum is opt-in, so a hand-written
  `--file` without it is as quiet as today; the handbook has to say so plainly. A rule row in a
  330 px panel gains a field. Cause 5 is reported, not failed. The runner gains a version check
  it did not need before.
- **Follow-ups / risks to watch:** the handbook's test chapter (`#szenarien`, the two traps)
  changes with the implementation — the first trap becomes "set a minimum", and the advice to
  state a `then` positively joins it. The course plan's module M4 teaches the minimum.
  The open question above is answered by the report of unread names: once it has been in use,
  how often an unread `when` name was a mistake and how often it was an untested path is a
  count, not an argument.

### What the implementation starts with (ADR-0018)

- A rule with `minMatched: 1` that selects nothing fails; the same rule selecting one case
  passes; without the field, both pass as today.
- `minMatched` counts a matched case that did not finish.
- A negative `minMatched` is refused with the rule's number.
- `unreadWhen` names `duaer` for `duaer > 30`; it does not name a loop-bound name
  (`some x in items satisfies x > 1`), `end`, `durationSeconds`, or a name one case carried.
  `unreadThen` names `fehler` for `fehler = null` when no case carries it.
- The verdict echoes `minMatched`; the runner exits 1 when a requested minimum is not echoed,
  and prints unread names under the rule's line.
- e2e: a rule added in the panel carries `minMatched: 1`; a rule opened from a scenario without
  it shows 0.

## Pros and cons of the options

### 1 — Keep the rule as it is

- Good: nothing to build. It keeps the reading ADR-0215 chose on purpose — the same reading a
  sequence-flow condition gets — and a universal statement over no cases is, in logic, true.
- Bad: it rests on discipline, and discipline protects the day the rule was written, not the
  rename six months later (cause 2). "True over no cases" is the right logic for a statement
  and the wrong one for a test, whose whole job is to be able to fail.

### 2 — A minimum per rule (chosen)

- Good: explicit, reviewable in a pull request, stored with the scenario, judged by the one
  judge; covers causes 1 to 4 whenever it is set; also states coverage; changes nothing that
  exists.
- Bad: opt-in on the wire. The Modeler default and the handbook carry the burden of making it
  the normal case.

### 3 — A runner flag

- Good: one switch in a pipeline, no scenario to edit.
- Bad: two judges — the panel green, the build red, for the same scenario. ADR-0215's runner
  replays the three requests precisely so that this cannot happen. It would also make the
  pipeline's command line part of the test's meaning, which a reviewer reading the scenario
  cannot see.

### 4 — Flip the default

- Good, and this is the strongest argument against the chosen option: a saved scenario is
  reproducible, so a rule that selects nothing will never select anything. Failing it is
  simply true, and needs no author to remember anything.
- Bad: it turns stored green builds red on upgrade with no change to model or scenario; a
  hand-written scenario without a seed is seeded from the clock, and there the count is not a
  fixed fact, so a rule about a rare case would flicker; and it inverts ADR-0215's reading
  without evidence that the reading misleads more often than it helps. The chosen option makes
  new rules strict and gathers exactly that evidence.

### 5 — Refuse unread names by default

- Good: it catches the two commonest causes automatically, and the precedent is in the same
  file — a bound on an unknown pool fails for the reason given above.
- Bad: a `when` over a variable that only one path sets (`ablehnungsgrund != null`) fails when
  the dataset never takes that path — sometimes rightly, because the path is untested, sometimes
  not; and it changes existing verdicts. Kept as the open question, to be answered from the
  unread-name reports rather than from this record's reasoning.

## Links

- extends [ADR-0215](0215-modeler-playground.md) — the rule, its reading of `when`, the
  three-request runner and the single verdict this keeps
- relates to [ADR-0392](0392-the-same-refusal-at-every-door-that-already-refuses.md) — rules are
  refused where they are compiled, and a negative minimum is refused there too
- relates to [ADR-0388](0388-a-call-that-can-only-be-null-is-refused-at-deploy.md) — the other
  way a rule can compile and still be unable to work
- relates to [ADR-0293](0293-open-questions-in-records-expire.md) — the open question in the
  front matter
- relates to [ADR-0018](0018-test-driven-development.md) — the tests the implementation starts
  with
