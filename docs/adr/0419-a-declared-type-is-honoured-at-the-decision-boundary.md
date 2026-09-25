# ADR-0419: A declared type is honoured at the decision boundary

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-25
- **Deciders:** Atlas maintainers
- **Open question:** whether a decision **service** should be held to the same
  standard — neither the conversion nor the refusal reaches one today, because temis
  publishes no input schema for a `CompiledService`, and the working set the
  conversion does use is empty whenever a service's output decision reaches its
  inputs through other decisions rather than directly
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

**Chosen: C′, and it has landed** — in temis as
[ADR-0040](https://github.com/pblumer/temis/blob/main/docs/adr/ADR-0040-eingabetyp-schema-validierung-auswertung.md),
carried here by the module bump. `inputToValues` converts by the declared type,
`ValidateInput` accepts a string only where that type can be made from it, and `goKind`
names a FEEL value by its FEEL type. **Nothing in Atlas implements the conversion**,
which is the point: the boundary belongs to the engine that defines it.

Option 3 was not needed. It remains conformant FEEL — `date()` is a built-in — and is
still the answer for a model that must run against an older engine. It was not chosen
because the boilerplate it leaves is permanent and its reason is not legible a year on.

2A′ was ruled out by measurement: a `time.Time` is *rejected* by `WithStrictInput` as
`date and time` where a `date` is expected, so converting on the Atlas side would have
traded a silent wrong answer for a refused evaluation without ever producing the value
the model asked for.

### What it costs here: nothing, and that was measured rather than assumed

The question this record carried was whether a model in this installation reads a typed
date input as a string on purpose. It was answered by reading every model, not by
reasoning about them: across all 12 registered model handles and all 19 decisions on
the running instance, **every declared input type is `string`, `number`, or undeclared
— not one temporal type.** The six `.dmn` files in this repository declare only
`string`, `boolean`, `number`, and one custom `Rating`.

So no deployed decision changes behaviour, and the preflight report this record
proposed as option **ii** is not needed here. It stays the right thing for an
installation that does have such a model, and is left unbuilt rather than built against
nothing.

Measured after the bump, through Atlas's own `Try`, on a `date`-typed column whose rule
reads `< date("2026-01-01")`:

| sent | before | after |
|---|---|---|
| `"2025-06-01"` | `"neu"` — the catch-all | **`"alt"`** |
| `"2027-06-01"` | `"neu"` | `"neu"` |
| `"nonsense"` | `"neu"` | `"neu"` — see the consequences |

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

Under **option C′**, as landed:

- **Positive.** A declared type is load-bearing rather than decorative, which is what
  makes the typed test fields — and any future typed input mapping — worth having.
- **Positive.** temis's schema, its validator and its evaluator agree again. They did
  not, and the disagreement was invisible from any one side alone.
- **Neutral here.** No deployed model relies on a date arriving as a string, because no
  deployed model declares a temporal input at all.
- **Positive, and decided here.** A business rule task now **refuses** a wrongly-typed
  input rather than carrying on with the catch-all answer. See "The refusal" below for
  what is refused and what deliberately is not.
- **Negative in principle, nil in practice, and that was measured.** A wrong mapping
  that produced a wrong answer silently now produces an incident. That is a behaviour
  change on deployed processes: an instance whose io-mapping has always delivered a
  string where the model declares a number stops at the task instead of passing it.
  On this installation it costs nothing — see "What the refusal would have cost"
  below — but the shape of the risk is real for any installation that has such a
  mapping, and it surfaces all at once, at upgrade.

## What the refusal would have cost, on the history rather than in principle

The question the refusal raises is not whether it is right but what it breaks on the way
in: an instance whose mapping has always delivered the wrong type fails at the task the
moment this ships. That is answerable from the retained evaluation history rather than by
argument, so it was answered that way before shipping.

Every retained evaluation of every deployed decision was read and run through the same
predicate the code uses — a value is refused only when its kind contradicts the input's
declared type:

| Decision | Evaluations | Declared inputs | Would be refused |
|---|---|---|---|
| RowValid | 424 | email, group, license — all `string` | 0 |
| Freigabe | 20 | betrag `number`, risiko `string` | 0 |
| ReisePflichten | 15 | alter `number`, reiseart `string` | 0 |
| Kreditfreigabe (service) | 9 | betrag, laufzeitMonate, einkommen `number`; bonitaet `string` | 0 |
| Begrüssung | 8 | Name `string` | 0 |
| KontotypMapping | 5 | kontotyp `string` | 0 |
| Tagesgruss_holen | 5 | Stunde `number` | 0 |
| Alter prüfen | 4 | Alter `number` | 0 |
| Notenschluessel | 2 | punkte `number` | 0 |
| praemie | 2 | alter, jahreskilometer `number`; fahrzeugklasse `string` | 0 |
| bw-vorpruefung | 1 | abschluss `string`, erfahrungJahre `number` | 0 |
| **Total** | **495** | | **0** |

Not one value in the whole history contradicts its declared type. The counts agree with
the per-decision totals the registry reports, so the history is complete rather than
sampled.

Two things the reading settled that the argument could not.

**The `UNKNOWN_INPUT` exclusion is not hypothetical.** `praemie` is supplied
`schadenfreiJahre` and `selbstbehalt`, which its schema does not declare. Had the refusal
covered that code, both of its evaluations would have failed — a working decision broken
by a mapping that costs nothing. The narrow scope is what the data asks for.

**A `null` is not covered, and it is the shape that actually occurs here.** Twenty
evaluations carried a null where a type is declared: RowValid 15, Begrüssung 4,
KontotypMapping 1 — the last of them the most recent run of that decision. temis does not
call a null a type mismatch, so none of these is refused, and the account-ordering
evaluation of 2026-09-22 stands as it was: `kontotyp: null`, no rule matched, output null,
no incident. That is the same silence this record set out to remove, reached by a
different route. It is named here rather than quietly fixed, because "the variable is
absent" and "the variable has the wrong type" are different questions with different right
answers.

Two limits on the measurement, stated so it is not read for more than it says. The schema
compared against is Atlas's own per-decision view, which lists a decision's *reachable*
inputs, while the refusal checks temis's *direct* ones — a subset, with the same declared
type per name, so a clean result on the larger set is clean on the smaller one too. And
`Kreditfreigabe`'s nine evaluations are counted although the refusal does not reach a
decision service at all; excluding them changes nothing, since they are clean either way.

## The refusal

The conversion above makes a declared type mean something. This section says what
happens when the value cannot be made to mean it.

Atlas evaluates leniently: it never passes `WithStrictInput`, and a wrongly-typed value
is not an error in FEEL. `betrag = "500"` against a column typed `number` does not
raise; the comparison is null, no rule matches, the catch-all row answers, and the token
carries on. The answer is plausible and wrong, and nothing downstream — not the trace,
not the retained record, not an incident — distinguishes it from a right one. That
silence is the whole defect this record is about.

`evalDecision` therefore asks `CompiledDecision.ValidateInput` before evaluating and
returns an error on a mismatch. The handler returns it, the job fails, its retries run
out, and an incident carries the message (ADR-0061). Retry behaviour is untouched: there
is no non-retryable job in Atlas today, and inventing one here would be a second
decision smuggled into this one.

Only `TYPE_MISMATCH` is refused, of the four codes temis reports:

| Code | Refused | Why |
|---|---|---|
| `TYPE_MISMATCH` | yes | the silent-wrong-answer case this record exists for |
| `MISSING_INPUT` | no | temis already refuses it from `Evaluate` as `MISSING_REQUIRED_INPUT`, with a better message |
| `UNKNOWN_INPUT` | no | an io-mapping may carry a row the decision does not read; temis ignores it, and failing the job would break processes that work today |
| `VALUE_NOT_ALLOWED` | no | a value question, not a type question — a model can constrain an input more narrowly than any deployed task knows, so it gets its own record |

Every mismatch is named, not only the first, so an operator reads the whole problem out
of one incident instead of fixing one input and meeting the next.

## Ein Decision Service ist hier nicht abgedeckt

Die Prüfung dieses Records sitzt in `evalDecision`. Ein Decision Service läuft
über `evalService`, und dort greift sie nicht. Das ist keine Nachlässigkeit,
sondern fehlendes Material: `tdmn.CompiledService` veröffentlicht weder
`InputSchema()` noch `ValidateInput` — es gibt in temis nichts, wogegen hier
geprüft werden könnte.

Die Koerzierung aus temis ADR-0040 erreicht einen Service ebenfalls nur
teilweise. `(*CompiledService).declaredInputs()` liest `inputs` der
Output-Decisions, und `buildInputSchema` füllt die aus `RequiredInputs` — den
**direkten** `<requiredInput>`-Referenzen, nicht dem transitiven Kegel. Im
einzigen hier deployten Service (`kreditfreigabe`) hat die `outputDecision`
`Kreditentscheid` zwei Information Requirements, beide `requiredDecision`. Die
Menge ist also leer und die Koerzierung ein No-op, obwohl das `<decisionService>`
seine typisierten Grenzwerte selbst auflistet (`in_betrag: number`,
`in_laufzeit: number`, `in_einkommen: number`, `dec_bonitaet → bonitaet: string`).

Gemessen ist der Schaden heute null: dieser eine Service führt ausschliesslich
`number` und `string`, und für beide ist die Go-Abbildung schon vor ADR-0040
richtig. Die Lücke schlägt erst bei einem Service zu, der ein `date`, `time`,
`date and time` oder eine Dauer an seiner Grenze führt — und dann still, weil
ein nicht koerziertes Datum keinen Fehler wirft, sondern eine nicht matchende
Zeile.

Der Weg dorthin ist ein Eingabeschema für den Service in temis, gespeist aus
`<inputData>` und `<inputDecision>` des `decisionService`-Elements (DMN §10.4
sieht genau diese Quelle vor). Das ist additiv, macht `WithStrictInput` an der
Service-Grenze erstmals wirksam, und ist derselbe Beschluss wie ADR-0040, nur
eine Ebene höher. Die Alternative — die Typprüfung in Atlas aus
`dmn/services.go` nachzubauen — wurde verworfen: sie erzeugt ein zweites
Typsystem neben dem von temis und löst ohnehin nur die Hälfte, weil die
Koerzierung in `inputToValues` sitzt und von aussen nicht nachzuziehen ist.

## What this record does not decide

Whether a business rule task's **input mapping** should also be typed. The mapping is
FEEL over the instance's variables and produces whatever that FEEL produces; it is a
separate boundary with a separate answer.

Whether a **null** where a type is declared should be refused. temis does not call it a
type mismatch, and this record does not make it one: "absent" and "wrongly typed" are
different claims about a variable, and a decision may legitimately be written to answer
for a missing input. The history shows it is the case that actually occurs here, so it
wants a record of its own rather than a line in this one.
