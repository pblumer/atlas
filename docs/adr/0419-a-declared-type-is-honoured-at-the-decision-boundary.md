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
and the comparison is then `true`. `date()` is a FEEL built-in, so that column is
ordinary, conformant FEEL — nothing non-standard enters the model.

### A third measurement: temis's own contract contradicts itself

temis does not leave the declared type behind. `Definitions.ReachableInputSchema`
returns it, and `CompiledDecision.ValidateInput` / `Evaluate(…, WithStrictInput())`
exist precisely so that a wrong input is a named problem rather than a silent null.
Measured against the same `date`-typed input:

| Sent | `ValidateInput` says |
|---|---|
| `"2025-06-01"` | **nothing — it conforms** |
| `"nonsense"` | **nothing — it conforms** |
| `""` | **nothing — it conforms** |
| `42` | `TYPE_MISMATCH: expects date, got number` |
| `true` | `TYPE_MISMATCH: expects date, got boolean` |
| `time.Time` | `TYPE_MISMATCH: expects date, got date and time` |

So the schema says the input is a `date`; the validator says any string conforms to it;
and the evaluator then treats that string as a string, so every date comparison fails.
Those three cannot all be right. Either the validator should refuse the string — which
would make today's wrong answer visible — or the evaluator should convert it, which
would make it correct. Doing neither is what produces a wrong answer with nothing to
read.

That is the finding that settles this record's shape: **this is a hole in a contract
temis already has**, not a new opinion Atlas would be imposing on it.

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

**Option C′ — temis closes the hole at the input boundary.** The only way a `date`-typed
input becomes a FEEL date.

An earlier draft of this record called this "a departure from FEEL". That was wrong,
and the third measurement is why. FEEL has no implicit string-to-date *inside an
expression*, which is true and not the question: the Go-value-to-FEEL-value mapping is
outside FEEL entirely, DMN does not prescribe it, and it is the host's boundary to
define. temis already defines it, already publishes the expected FEEL type per input,
and already validates against it — it simply accepts a string for a `date` and then
does not convert it. Closing that is a defect fix inside temis's own contract, not a
new reading imposed on it.

What it costs: a model relying on a date arriving as a string changes behaviour, and
the change has to be recorded in temis, not only here.

**Option 2A′ — Atlas converts, for `date and time` only.** Honest and small: it fixes
the type it can fix and leaves `date` to option 3 or C′. Its cost is a rule with a hole
in it, which is a thing to explain to every author.

**Recommended: C′.** The third measurement moves it from "a change to the engine" to
"the engine's own contract, honoured". The alternative is to leave every date model
carrying `date(…)` around a value the schema already calls a date — compensation for a
host defect, written into documents that are supposed to be the business rule and
nothing else.

Option 3 remains available as a stopgap for a model that must work before C′ lands,
and it is conformant FEEL, so nothing has to be undone afterwards. It is not
recommended as the answer, because the boilerplate it leaves is permanent and the
reason for it will not be legible a year from now.

2A′ is not recommended at all: measured, a `time.Time` is now *rejected* by
`WithStrictInput` as `date and time` where a `date` is expected, so converting on the
Atlas side would trade a silent wrong answer for a refused evaluation without ever
producing the value the model asked for.

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
- **Positive.** temis's schema, its validator and its evaluator agree again. Today they
  do not, and the disagreement is invisible from either side alone.
- **Negative.** A model that relies on a date arriving as a string changes behaviour.
  The preflight list is how that is found before it runs, not after.
- **Negative.** It needs its own record in temis, and a decision there about which
  spellings a string may take before it is accepted as a date (ISO 8601 only, or more).

## What this record does not decide

Whether a business rule task's **input mapping** should also be typed. The mapping is
FEEL over the instance's variables and produces whatever that FEEL produces; it is a
separate boundary with a separate answer.
