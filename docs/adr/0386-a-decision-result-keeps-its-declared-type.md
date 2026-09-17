# ADR-0386: A decision result keeps the type the model declares for it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether Atlas should recognise XSD type spellings (`xsd:decimal`, `xsd:integer`) that temis deliberately does not canonicalize — measured that they arrive as no declared type at all, not decided whether Atlas should hold a second type vocabulary
- **Question checked:** 2026-09

## Context and problem statement

A business rule task whose decision returns a number wrote its result variable as
`VarString`. The cost was not cosmetic. A sequence-flow condition comparing that
variable to a number is a FEEL type mismatch, which evaluates to `null`, which is
not `true` — so the token took the **default flow**, with no incident, no
diagnostic and no trace entry. The process simply routed the wrong way.

Found on a live instance, where an instance's variables read:

```json
{"alter": 19, "jahreskilometer": 22000, "praemie": "1250"}
```

The two set from a form are numbers; the one set from the decision is a string.

**Neither side is at fault on its own.** temis hands a FEEL number back as its
exact decimal *string*, and says so:

> FEEL results convert back to Go with numbers rendered as their exact decimal
> string (temis ADR-0007), booleans as bool, strings as string […]

That is deliberate and right: temis holds numbers as arbitrary-precision
decimals, and a `float64` would round an amount on the way out. And Atlas has a
lossless home for exactly that shape — [`model.VarNumber`](../../model/value.go)'s
`Text` *is* "the canonical decimal string". Nothing has to be rounded or reparsed
to store a decision's number as a number.

What was missing between them is only the knowledge of **which strings are
numbers**. `dmn.OutputVariable` receives an untyped `any`, sees a Go `string`, and
correctly concludes "string".

Measured, before the change, through Atlas's own evaluation path:

| decision logic | declared type | out of temis | stored as |
|---|---|---|---|
| decision table | `number` | `string` `"1250"` | `VarString` |
| literal expression | `number` | `string` `"14"` | `VarString` |
| decision table | `boolean` | `bool` `true` | `VarBool` — correct |
| decision table | `string` | `string` `"B"` | `VarString` — correct |
| boxed context | — | `map[string]any{"brutto":"70",…}` | `VarJSON`, `brutto` quoted |

So only numbers lose their type, and they lose it at every nesting level the
result has.

The question this record answers: **what decides that a decimal string is a
number?**

## Decision drivers

- Silent is worse than wrong. A wrongly-typed variable that routes a token down
  the wrong path leaves nothing to read.
- Exactness must survive. An amount may not be rounded to make it typed.
- A guess must never turn a genuine string into a number: the failure mode of a
  guess is an invisible wrong *value*, not a visible wrong type.
- One answer, not several. The worker, the try-a-decision endpoint and the
  retained evaluation record must not come to disagree about what a result is.

## Considered options

1. The model's own declarations decide
2. Convert any output string that parses as a FEEL decimal
3. Bump temis so it returns `json.Number`
4. Leave it, and document the `number(...)` workaround

## Decision outcome

Chosen option: **"The model's own declarations decide"**.

DMN declares a type for a decision's result and for each member of a structured
one. That declaration — and nothing else — decides whether a decimal string
becomes a number. A string that merely *looks* like a decimal is left alone.

The carrier is **`json.Number`**: a Go `string` underneath, so the exact decimal
survives, and a distinct type, so nothing downstream has to guess again.
`expr.FromJSON` already maps it to a FEEL number wherever it appears, recursively
through lists and contexts, so the variable, the condition that reads it and the
retained record all follow from this one step without further change.

**Where:** inside `dmn.evalDecision`, the single point every caller passes
through — the business rule task, the try-a-decision endpoint, and the durable
evaluation record (ADR-0066). Not at `OutputVariable`, which is outside the
package that holds the compiled model and would have to be handed the types.

**Which accessor answers** depends on the shape of the logic, and all three are
needed because each shape declares its types somewhere else. Measured:

| shape | result | type read from |
|---|---|---|
| table, one output column | scalar | the DRG node's data type — temis already falls back there to a lone output column or a literal expression |
| literal expression | scalar | the same |
| table, several output columns | context keyed by column name | each output column's `typeRef` |
| boxed context | context keyed by entry name | each entry's `typeRef` |
| boxed context with a result cell | scalar | the result cell's `typeRef` |
| `COLLECT` with no aggregation | list | the output column's type, per element |

### Consequences

- **Positive:** a gateway condition can compare a decision result to a number
  with no `number(...)` wrapper in the model to paper over the type, and a
  downstream `praemie.brutto * 12` is arithmetic rather than `null`.
- **Positive:** the exact decimal is preserved — `99.50` comes back as `99.5`,
  temis's canonical form, stored as text and never as a float.
- **Positive:** the retained evaluation record shows a number as a number, so an
  operator reading back how a decision was made is not looking at a quoted one.
- **Negative / trade-offs accepted:** an output the model leaves untyped stays a
  string even when it is plainly a number. That is the honest answer — the
  alternative is option 2 below — but an author who omits `typeRef` will be
  surprised, and nothing yet tells them.
- **Negative / trade-offs accepted:** the conversion is one level deep. A number
  nested inside a context inside a context keeps its string.
- **Follow-ups / risks to watch:** the XSD spellings in the open question above;
  the remote temis worker (`connector/temis`) evaluates over HTTP with no
  compiled model in reach and is therefore unaffected, so a central decision
  still returns strings; and the trace still carries decimal strings throughout,
  since its values are display-only.

**Records already written keep their shape.** A retained evaluation is a fact
(invariant I6), so nothing rewrites the ones on disk; only evaluations from here
on record a number unquoted.

## Pros and cons of the options

### Option 1 — The model's own declarations decide
- Good: cannot turn a genuine string into a number, because it never guesses.
- Good: needs no change outside the `dmn` package — `expr.FromJSON` already
  understands the carrier.
- Bad: an untyped output is not converted, and DMN makes `typeRef` optional.
- Bad: three accessors to read, one per logic shape, each of which could grow a
  fourth as temis adds boxed expressions.

### Option 2 — Convert any output string that parses as a FEEL decimal
- Good: catches the untyped outputs option 1 misses; no type lookup at all.
- Bad: **unsound.** A policy number, an article code, a Swiss postcode and
  `"0800"` all parse as decimals. `"0800"` would come back as `800`. It trades a
  visible wrong type for an invisible wrong value, which is strictly worse than
  the defect it fixes.

### Option 3 — Have temis return `json.Number`
- Good: one line upstream, complete at every nesting depth, and Atlas would need
  no change at all (measured: `expr.FromJSON` already maps `json.Number`).
- Good: it is where the knowledge actually lives — temis knows it held a
  `value.Number`.
- Bad: **it does not exist to take.** Measured against the newest temis there is
  (`v0.0.0-20260911201220`, which this repository now pins — see
  ADR-0385): `fromValue` still
  renders a FEEL number as a decimal string, and an evaluated output still arrives
  as a Go `string`. The bump did not fix this defect, so the choice here is not
  between option 1 and a bump — it is between option 1 and leaving it standing.
- This stays the better *destination*, and it is upstream work, not a version to
  pick up. If temis ever carries the type out, this record's machinery becomes
  redundant and should be removed rather than kept alongside it.

### Option 4 — Leave it, document `number(...)`
- Good: no code.
- Bad: every author must remember it in every condition, and the failure when
  they forget is a silently mis-routed token. A workaround for a defect that
  routes work to the wrong place is not a workaround.

## Links

- fixes [#991](https://github.com/pblumer/atlas/issues/991)
- blocked from the better fix by [#992](https://github.com/pblumer/atlas/issues/992)
- relates to [ADR-0066](0066-decision-evaluation-records.md) — the retained record
  this also changes the shape of
