# ADR-0388: A call that can only be null is refused at deploy

- **Status:** Accepted (amended 2026-09-17 — the refusal is a deploy gate, not a condition for loading a stored definition; see the amendment note below)
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Patrick Blumer

## Context and problem statement

The FEEL engine compiles a call to a name it does not know into a constant null. It is not
an oversight — the engine says so where it does it:

> Invoking an unknown name is not a compile error: FEEL invocation is a total function, so a
> call whose callee cannot be a function evaluates to null and keeps the decision executable.

That is the DMN rule, and the TCK asks for it: a decision service must answer. Atlas
evaluates DMN through this same engine, so the behaviour has to stay.

For a BPMN model it produces a defect with no visible surface at any point:

1. `= is defined(kunde.geburtsdatum)` deploys clean. No error, no warning, nothing in the
   Problems panel.
2. It evaluates to null. The arguments are not even evaluated — the expression never reads
   `kunde` at all, so the null carries no trace of where it came from.
3. A gateway condition on that null is not true, so the token takes the default flow.

Three steps, nothing said, and in the model this record comes from a customer was set
INACTIV who should have been ACTIVE. The same model carried a second instance of the same
fault: `date()` with no argument, which the engine binds to null exactly as it binds an
unknown name.

`is defined` is a Camunda/Zeebe extension, and one of the first things somebody arriving
from there writes. But the dialect is not what makes this a defect — a misspelling produces
the identical silence, and is more common.

Worse, the silence is not even consistent. Two of the same foreign functions fail to
*parse*, because `else` and `of` are FEEL keywords:

```
get or else(s, "fallback")              → compile error: unexpected "else"
last day of month(date("2026-02-01"))   → compile error: unexpected "of" after expression
is defined(s)                           → null, silently
```

So whether the author is told depended on whether the missing name happened to collide with
a keyword.

## Decision drivers

- **A deploy is a moment where refusing is free.** The model is not running, and its author
  is looking at it. Nothing else in the lifecycle of that expression has both properties.
- **DMN's totality must not be touched.** The engine is shared with the decision service,
  where answering null is required behaviour, and the conformance suite pins it.
- **One rule, not two.** Whatever decides arity here has to be the engine's own rule, or the
  two drift and the check starts refusing what the engine would have run.
- **A false refusal is worse than a missed one.** It blocks a model that works, and the
  author has no way to argue with it. A missed one leaves today's behaviour in place.
- **"No" is not a useful answer on its own.** Somebody who wrote `is defined` and is told it
  does not exist will guess again, and the next guess is usually another Camunda function.

## Considered options

1. **Change the engine** so an unknown callee is a compile error.
2. **Refuse in `expr.Compile`/`CompileAuto`**, the shared Atlas wrapper around the engine.
3. **Refuse on the BPMN compile path only**, as a check the compiler runs before compiling
   each of a model's expressions.
4. **Report it as a warning** rather than refusing, beside the data-flow findings.
5. **Implement the foreign functions** so the commonest case stops arising.

## Decision outcome

Chosen option: **"Refuse on the BPMN compile path only"**. `expr.CheckCalls` reads the same
AST the engine compiles and reports the calls that can only ever be null; `compiler.compileFEEL`
is the one door every one of a model's expressions goes through, and it refuses before
compiling. Evaluation semantics are untouched — a DMN decision through the same engine still
behaves exactly as the specification requires, which a test pins.

Two faults are reported:

- **A callee no built-in has.** Where the name belongs to another engine's dialect, the
  message names the standard way to say the same thing: *`is defined` → write `x != null`*,
  *`put` → `context put`, which this build has*. That is the cheap half of the
  compatibility question and may be the whole answer to it; the rest is its own decision.
- **A built-in called with a count its signature cannot take.** The rule is the engine's own
  (`MinArgs`, `MaxArgs`, variadic), read off the registry rather than restated.

It errs quiet, deliberately. A callee that any reading could bind to something callable — a
function parameter, a `for` or quantifier iterator, a context key, a filter's `item`, a
variable the caller declares — is left alone. A named call is left alone too, because an
overloaded built-in binds against whichever signature covers the names given and
second-guessing that would be the second copy of a rule. A source that does not parse yields
nothing here: the syntax error is the real fault and the compiler already reports it.

### Consequences

- **Positive:** the whole class goes loud at once — the foreign dialect, the misspelling, the
  wrong arity — and the inconsistency where a keyword collision decided whether you were
  told is gone.
- **Positive:** the refusal reaches the Problems panel through the existing `RuleCompile`
  path, so it is visible while editing and not only on deploy.
- **Negative / trade-offs accepted:** a model that deploys today and contains such a call
  will stop deploying. That is the intent — it cannot ever have worked — but it is a
  behaviour change for somebody whose broken call sits on a path nobody walks. What this
  record did *not* consider is the model that was already deployed: see the amendment below.
- **Negative:** the refusal is a compile error, so it has no element anchor of its own in the
  panel. The message carries the element id because the caller wraps it, which is enough to
  find it and less than an anchored Problem would be.
- **Follow-ups / risks to watch:** the other surfaces that compile FEEL, settled since by
  [ADR-0392](0392-the-same-refusal-at-every-door-that-already-refuses.md),
  which also corrects the reason given here for leaving them — three of them did have a
  moment at which refusing is free, and were already using it. And if the engine ever gains a
  way to register extra functions on the BPMN path, this check has to be told about them or
  it will refuse a call that works.

## Pros and cons of the options

### Option 1 — Change the engine
- Good: one place, and every caller gets it.
- Bad: it breaks DMN. Totality is what the specification requires of a decision, the
  conformance suite pins it, and Atlas runs decisions through this engine.

### Option 2 — Refuse in `expr.Compile`/`CompileAuto`
- Good: one line, complete coverage of everything Atlas compiles.
- Bad: the same defect as option 1 by a shorter route — `expr` is what the DMN side compiles
  through as well. The refusal belongs to the deploy, not to compilation.

### Option 3 — Refuse on the BPMN compile path
- Good: exact. The deploy refuses; the decision service is untouched; one helper instead of
  eighteen call sites each deciding for itself.
- Bad: the other FEEL surfaces are not covered, and each has to be decided on its own.

### Option 4 — A warning rather than a refusal
- Good: nothing that deploys today stops deploying.
- Bad: it is not a modelling opinion, it is an expression that cannot work. The data-flow
  checks are warnings because a model is routinely drawn before its vocabulary follows;
  nothing comparable is true here.

### Option 5 — Implement the foreign functions
- Good: the commonest case simply works.
- Bad: it answers one name at a time and leaves the misspelling silent — the defect is the
  silence, not the list. Whether to implement them is a separate decision with its own
  record.

## Amendment (2026-09-17): the refusal does not reach a stored definition

This record weighed the cost as "a model that deploys today will stop deploying". It missed
the model that was deployed *yesterday*. `compileFEEL` runs in the build stage, so its
refusal reached `parseNamed` as a plain error rather than as the `ValidationError` carrying a
compiled process that the reload path knows how to take apart
(ADR-0177). A stored deployment with one such call
therefore made the server exit during startup, be restarted, and exit again — with every
other definition and every running instance unreachable behind it. That happened in
production, on a probe model calling `get keys(...)`.

The rule is unchanged and still refuses the deploy with the same message. What changed is
that the compile now carries a gate: a deploy refuses, a reload compiles the expression the
way the build that stored it did — the engine binds the unknown callee to null, as it always
has — and reports the fault as a `feel.null-call` Problem beside the process, which
`loadDeployments` logs as `deployment.reloaded_with_problems`. See
[ADR-draft-a-rule-added-later-is-a-gate-on-deploy](draft-a-rule-added-later-is-a-gate-on-deploy.md),
which states the general form: a rule the compiler gains after a definition was stored is a
gate on deploying it, never a condition for loading it, whatever stage it lives in.

## Links

- amended by [ADR-draft-a-rule-added-later-is-a-gate-on-deploy](draft-a-rule-added-later-is-a-gate-on-deploy.md) — the reload split this record's placement bypassed
- relates to [ADR-0177](0177-reload-skips-the-deploy-gate.md) — the gate/reload split
- relates to [ADR-0008](0008-feel-expression-strategy.md) — compiling expressions once, at
  deploy, which is the moment this refusal uses
- relates to [ADR-0015](0015-reuse-feel-engine.md) — the engine this leaves alone
- relates to [ADR-0026](0026-problems-panel-and-versioned-validation.md) — the panel the refusal surfaces in
- extended by [ADR-0392](0392-the-same-refusal-at-every-door-that-already-refuses.md) —
  the same refusal at the other doors that already refuse
