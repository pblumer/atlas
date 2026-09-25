# ADR-0419: A declared type is honoured at the decision boundary

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-25
- **Deciders:** Atlas maintainers
- **Open question:** whether any deployed model reads a typed date input as a string on
  purpose — that decides whether a conversion is a fix or a break, and it cannot be
  answered from the code, only by looking at the deployed models; and, prior to that,
  which of options 3, C′ and 2A′ this installation wants
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
decision sees — whatever its `typeRef` says.

**Measured**, not inferred, against the pinned temis and a model whose input declares
`typeRef="date"`, with a decision table whose column reads `Stichtag` and whose rule
cell is `< date("2026-01-01")`:

| Sent for `Stichtag` | rule matches? |
|---|---|
| `"2025-06-01"` | **no** — the catch-all rule wins |
| `date("2025-06-01")` (as text) | **no** — the catch-all rule wins |

Nothing errors, nothing is logged, and the decision returns the answer of a rule the
author did not intend. The same holds for `time`, `date and time` and `duration`.

A second measurement decides what a fix can look like, and it rules out the obvious
one. `toValue` maps a Go `time.Time` to a FEEL **date and time** — the only temporal
input shape it accepts — and a date and time is not a date:

| Sent for `Stichtag` | `Stichtag.year` | `< date(…)` | `< date and time(…)` | `date(Stichtag) < date(…)` |
|---|---|---|---|---|
| `"2025-06-01"` (string) | `null` | `null` | `null` | **`true`** |
| `time.Time`, midnight UTC | `"2025"` | `null` | **`true`** | **`true`** |

So handing temis a `time.Time` does produce a real temporal value — `.year` works,
and it compares with `date and time(…)` — but it still does not compare with
`date(…)`, which is how every date table anybody writes is spelled. That comparison
returning null is correct FEEL, not a defect: comparing a date and time with a date is
a type mismatch.

Atlas cannot construct a FEEL *date* at all with the pinned temis. `toValue` has no
case that produces one, and `value.Value` lives in temis's `internal/` package, so
there is nothing for Atlas to hand in. temis's own `coerceToType` would not help
either: it implements DMN §10.3.2.9.4 *coercion*, which keeps a conforming value or
makes it null — it is not a conversion, so a string for a `date` would become `null`
rather than a date, and it is applied at output boundaries only.

The last column is the way out that exists today: `date(Stichtag)` converts the string,
and the comparison is then `true`. That is a change to the **model**, not to Atlas.

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

*(Proposed. An earlier draft of this record chose 2A outright; the second measurement
above withdrew it, because 2A cannot produce a FEEL date and a date is the case the
record exists for. What follows is the revised reading, and the choice it leaves is
the one this record now needs answered.)*

**2A alone does not reach the goal.** `evalDecision` can convert an ISO string to a
`time.Time`, and that is a real improvement for an input declared `date and time` — but
for one declared `date` it produces a date and time, which still does not compare with
the `date(…)` in the model's own cells. Shipping it would change which comparisons work
without fixing the reported one, which is the worst of both.

Three options remain, and they are not exclusive:

**Option 3 — the model says it, and the editor offers to.** A column whose expression
is the bare name of a `date`-typed element becomes `date(Stichtag)`. Measured to work
today, with the string Atlas already sends. No engine change, no behaviour change for
anything deployed, and it fits the shape of every other repair in this editor: the
findings strip names the column, and a button writes the wrapper. It costs the author
one click per column, and it leaves the model saying out loud what it is doing.

**Option C′ — temis converts at the input boundary, by the declared type.** The only
way a `date`-typed input becomes a FEEL date. It is an engine change, and a departure
from FEEL, which has no implicit string-to-date: FEEL's own rule at a boundary is
coercion (conform or null), not conversion. Atlas owns temis, so it is available; what
it costs is that every temis user inherits Atlas's reading of `typeRef`, and that a
model relying on a date arriving as a string changes behaviour.

**Option 2A′ — Atlas converts, for `date and time` only.** Honest and small: it fixes
the type it can fix and leaves `date` to option 3 or C′. Its cost is a rule with a hole
in it, which is a thing to explain to every author.

**Recommended: 3 now, C′ as the target.** They compose. Option 3 makes the models that
exist correct this week, visibly, with nothing deployed changing behaviour. C′ then
makes the wrapper unnecessary for models written afterwards, and can be measured
against the models option 3 already made explicit. 2A′ is not recommended: a partial
conversion is harder to hold in the head than either end of it.

For deployed models, whichever is chosen, **option ii**: name the affected decisions at
the deploy preflight, so the change arrives as a list somebody read rather than as a
difference somebody noticed.

Option B stays rejected: the divergence it allows between the test panel and the
runtime is the failure this record is about.

## Consequences

Under **option 3**:

- **Positive.** A decision table that compares dates works, this week, on the models
  that already exist. Nothing deployed changes behaviour, because nothing about
  evaluation changes — only the model does, and only where an author clicked.
- **Positive.** The model says what it does. `date(Stichtag)` in the column is readable
  by the person reviewing the table, which an invisible conversion is not.
- **Negative.** Every date column carries a wrapper, forever, including in models
  written after C′ lands. That is the cost of a repair that leaves a trace.

Under **option C′**:

- **Positive.** A declared type becomes load-bearing rather than decorative, which is
  what makes the typed test fields (and any future typed input mapping) worth having.
- **Negative.** A model that relies on a date arriving as a string changes behaviour.
  The preflight list is how that is found before it runs, not after.
- **Negative.** temis departs from FEEL at the input boundary, and every temis user
  inherits that. It needs its own record in temis, not only this one here.

## What this record does not decide

Whether a business rule task's **input mapping** should also be typed. The mapping is
FEEL over the instance's variables and produces whatever that FEEL produces; it is a
separate boundary with a separate answer.
