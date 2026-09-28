# ADR-0419: A declared type is honoured at the decision boundary

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-25
- **Deciders:** Atlas maintainers
- **Open question:** whether any deployed model reads a typed date input as a string on
  purpose — that decides whether the conversion is a fix or a break, and it cannot be
  answered from the code, only by looking at the deployed models
- **Question checked:** 2026-09

## Context and problem statement

A DMN element declares the type of the value it carries:

```xml
<inputData id="d1" name="Stichtag"><variable name="Stichtag" typeRef="date"/></inputData>
```

Atlas hands a decision its inputs as `map[string]any` — decoded JSON, the shape a
process variable arrives in — and temis converts each Go value into a FEEL value in
`toValue` (temis `dmn/convert.go`). That conversion is driven by the **Go type of the
value**, not by the `typeRef` the model declares:

| Go value | FEEL value |
|---|---|
| `bool` | boolean |
| `float64`, `int…` | number |
| `string` | **string** |
| `time.Time` | date and time |
| `[]any`, `map[string]any` | list, context |

JSON has no date, so a date always arrives as a `string`, and a `string` is what the
decision sees — whatever its `typeRef` says. **Measured**, not inferred, against the
pinned temis and a model whose input declares `typeRef="date"`:

| Sent for `Stichtag` | `year(Stichtag)` | Rule `< date("2026-01-01")` |
|---|---|---|
| `"2025-06-01"` | `null` | **does not match** |
| `date("2025-06-01")` (as text) | `null` | **does not match** |

Both spellings fall through to the catch-all rule. Nothing errors, nothing is logged,
and the decision returns the answer of a rule the author did not intend. The same
holds for `time`, `date and time` and `duration`.

This is not a property of the editor's test panel. Both paths — `Try`
(ADR-0326, the panel) and `Registry.EvaluateTraced` (what a business rule task runs)
— end in the same `evalDecision`, with the same `map[string]any`. **So a decision with
a date input behaves this way in production**, and the failure mode is the worst one
this codebase has: a wrong answer rather than a failure, indistinguishable downstream
from a right one.

Two questions follow. **Should a declared type decide how an input is converted?** And
if so, **where**, given that changing it changes what already-deployed models compute?

## Decision drivers

- **A declared type is a claim the model makes.** A table whose column is typed `date`
  and whose cells compare dates is a model that says what it wants. Evaluating it
  against a string is not a strict reading of DMN; it is a conversion that ignored the
  only statement of intent the document contains.
- **Silence is the cost.** An unmatched date comparison is not reported anywhere: not
  by the compiler (the model is valid), not by the deploy gate (it deploys), not by
  the trace (the rule simply did not match, which is a legal outcome).
- **The test panel must not diverge from the runtime.** ADR-0326's panel exists to
  answer "does this do what I meant" before anything is deployed. A panel that coerced
  while the runtime did not would answer a question the engine answers differently —
  the test would pass and production would fail, which is worse than today's honest
  agreement between the two.
- **A change here changes deployed behaviour.** A model that today reads a date input
  as a string — comparing it with `string length`, slicing it, matching `starts with` —
  would start seeing a date. That is a silent behaviour change on somebody's running
  process unless it is made visible.
- **No engine surface.** Conversion is a pure function of a value and a declared type.
  It must not touch `applyToState`, the event log or the hot path (I1–I6).
- **temis stays the authority on FEEL.** Atlas may say *which* type a value should
  become; it must not reimplement what that type means.

## Considered options

For **whether**:

1. **Leave it.** Document that a date input is a string and let authors compare
   strings.
2. **Honour the declared type at the boundary.** Convert before evaluating.

For **where**, if 2:

- **A.** In `evalDecision` (`dmn/registry.go`), which both `Try` and every runtime
  evaluation already funnel through. One place, both callers, no divergence.
- **B.** In each caller, so the runtime and the panel can be changed separately.
- **C.** In temis, by teaching its evaluator to consult `typeRef`.

For **what to do about models already deployed**:

- **i.** Convert unconditionally, from the next release.
- **ii.** Convert, and report at the deploy gate which decisions have a typed input
  whose value the conversion would change the meaning of.
- **iii.** Convert only for models deployed after the change, keyed on the deployment.

## Decision outcome

*(Proposed — this record exists to be argued with before anything is built.)*

**Honour the declared type, in `evalDecision` — option 2A.**

`evalDecision` already has both the compiled definitions and the decision id, so it can
ask the model what each input declares and convert accordingly:

- `date`, `time`, `date and time` → parsed from the ISO 8601 text the rest of Atlas
  already uses, handed to temis as a `time.Time`, which is the input shape temis
  already converts into a FEEL date.
- `duration` → left as text until temis exposes a duration input value; a string that
  cannot be converted is passed through **unchanged** rather than nulled.
- `number`, `boolean`, `string` → unchanged. JSON already carries them.
- An input whose declared type is absent, `Any`, or a structure → unchanged.

Two rules make it safe to reason about:

- **Conversion never loses a value.** A value the declared type cannot be made from is
  passed through as it arrived, so the decision sees exactly what it sees today and
  the model's own diagnostics stay the thing that reports the mismatch.
- **Conversion is total and pure.** Same input, same type, same result, on the run loop
  or off it, live or on recovery.

Option B is rejected because the divergence it allows is the failure this record is
about. Option C is rejected because `typeRef` handling is a DMN question and temis is
deliberately the FEEL engine; a change there would also bind every temis user to
Atlas's reading.

For deployed models, **option ii**: convert, and make the deploy preflight name the
decisions whose typed inputs would now be converted, so the change arrives as a list
somebody read rather than as a difference somebody noticed.

## Consequences

- **Positive.** A decision table that compares dates works. The test panel and the
  runtime stay one behaviour. What a model declares becomes load-bearing rather than
  decorative, which is what makes the typed test fields (and any future typed input
  mapping) worth having.
- **Negative.** A model that relies on a date arriving as a string changes behaviour.
  The preflight list is how that is found before it runs, not after.
- **Negative.** Atlas now holds an opinion about what `typeRef="date"` means for an
  incoming value. That opinion has to stay in step with temis's own type system, and
  a test pinning the pairing belongs with it.

## What this record does not decide

Whether a business rule task's **input mapping** should also be typed. The mapping is
FEEL over the instance's variables and produces whatever that FEEL produces; it is a
separate boundary with a separate answer.
