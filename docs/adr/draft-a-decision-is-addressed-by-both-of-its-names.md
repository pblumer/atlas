# ADR-DRAFT: A decision is addressed by both of its names

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether a *newly* authored artifact should carry the FEEL identifier rather than the label — it would stop depending on a resolution temis documents as backward compatibility, at the price of a `decisionId` that no longer reads like the decision on the diagram; deferred while that alias stands
- **Question checked:** 2026-09

## Context and problem statement

A DMN decision carries two names, and they need not be the same string:

- its `name` attribute is the free-form **label** the diagram shows;
- its `<variable name>` is the **FEEL identifier** its result is bound to and
  other expressions reference (DMN §7.3.1). A model that declares none falls back
  to the label, which is why the two coincide in most models.

The temis this repository pinned (`v0.0.0-20260722083752`) published a required
decision's result under its *label*: `envNames` built the visible-name table from
`d.Name`. So a model valid per DMN — `Decision A` with `<variable name="alpha"/>`,
`Decision B` reading `alpha * 10` — did not compile:

```
diags=[{error FEEL_COMPILE_ERROR decision "Decision B": unknown variable "alpha" d_b 1 1}]
eval: dmn: decision "Decision B" has no executable logic
```

Atlas refused, at deploy time, a model authored in any other tool, with a message
naming the symptom and not the cause (#992). The workaround was an authoring
constraint, not a preference: every decision in a chain had to be *named* like a
FEEL identifier. One of this installation's own models records it in a comment:

> `id` und `name` der Entscheidung sind absichtlich identisch: … Atlas löst eine
> Entscheidung über ihren NAMEN auf. Weichen die beiden voneinander ab, findet der
> Deploy die Entscheidung nicht und weist die Applikation zurück.

The newer temis binds by the identifier. That fixes the refusal — and moves the
name Atlas reads out of its own index, which is where the real question is,
because **every business rule task deployed so far carries the label**.

### What the newer temis changes, measured from its source

| | pinned `20260722` | `20260911` |
|---|---|---|
| a required decision is bound as | `Decision.Name` | `Decision.RefName()` — the identifier, else the label |
| an input is bound and required as | `InputData.Name` | `InputData.RefName()` |
| `Index().Decisions` lists | labels | identifiers |
| `Definitions.Decision(x)` resolves | id, label | id, identifier, **and** the label "for backward compatibility … so callers may still address a decision by the label it shows in the diagram" |

### What that does to a model, by class

Measured through `dmn.Registry` on both versions:

| class | the model | pinned | newer |
|---|---|---|---|
| A | no `<variable>` anywhere | works | unchanged |
| B | `<variable>` equal to the label | works | unchanged |
| C | `<variable>` differs, expressions use the identifier | **refused** (#992) | works |
| R | `<variable>` differs, expressions use the *label* | works | **refused** — `unknown variable "upstream"` |

Class R is the one genuine regression the bump brings, and it is not repairable
from Atlas: the label is no longer a name the FEEL environment holds. It is also
the class no DMN tool produces, because the label is not what the specification
binds.

### What it does to Atlas

`Index().Decisions` is Atlas's addressing vocabulary. It fills the registry's
"newest model providing this decision" pointers and the pinning selector, it is
what `modelProviding` matches a task's `decisionId` against, it is the membership
check behind try-a-decision, and it is what a decision deployment records and
counts versions under. Left alone, a class-C model already deployed would, after
a restart, be registered under its identifier only — and every task naming its
label would fail with `decision … in no model deployed`, a fresh incident per
instance, over a model nobody touched (invariant I6: what is on disk is a fact).

The same holds one level down for inputs: a task's input keys were recorded at
deploy time under the labels Atlas offered, which is what the pinned temis
required and the newer one does not.

## Decision drivers

- A deployed artifact keeps running. Nothing on disk may change meaning because a
  dependency moved.
- The gate and the runtime agree. A deploy must refuse exactly what the runtime
  would fail to resolve — no more, no less.
- One name per decision in the record. A version counter and a listing are
  identity, and a decision may not appear twice because it answers to two names.
- Atlas resolves nothing itself. Which decision a name means stays temis's answer,
  so the two cannot drift.

## Considered options

1. Take the bump; publish the label, accept both names
2. Stay on the pin and document the authoring constraint
3. Take the bump and make the identifier Atlas's identity everywhere
4. Take the bump and index exactly what temis indexes, with no compatibility layer

## Decision outcome

Chosen option: **"Take the bump; publish the label, accept both names"**.

The bump is what fixes #992, and no Atlas code had to change for the suite to pass
— the compatibility work below is about artifacts, not about compiling.

- **Published** (`ValidationResult.Decisions`): the label, exactly one per
  decision. That is what a deployment records, what the version counter is kept
  under, what a listing shows and what the picker writes into a business rule
  task — all as before the bump.
- **Accepted** (the registry's pointers, `modelProviding`, the try membership, the
  deploy-coverage gate): the label *and* the identifier. `decisionNames` reads the
  evaluable set from temis's index and the two spellings from the DRG graph, so a
  decision whose logic is present but did not compile is offered nowhere.
- **Inputs**: `aliasedInputs` fills a required input's identifier from the label
  where the caller supplied only the label. A supplied identifier is never
  overwritten, and a model whose inputs declare no separate identifier allocates
  nothing.
- **What the picker offers** as a decision's inputs is now the identifier the
  evaluation binds, not the label. Offering the label would have it prefill a key
  temis does not read.

Resolution itself is not reimplemented: Atlas answers "this model provides that
decision" and hands the name straight to `Definitions.Decision`. A name that
appears twice in one model therefore cannot make Atlas evaluate a different
decision than temis would.

### Measured against the installation, before the bump

Every DMN model stored on the live instance was read (11 distinct handles behind
12 references, 2026-09-17):

- **No decision** declares a `<variable>` whose name differs from its label. Seven
  models declare none at all; `praemie-tarif` declares one per decision, each equal
  to the decision's name.
- **No input data element** does either.
- So **class C and class R are both empty here**: nothing deployed on this
  installation changes how it is addressed, and the compatibility layer above is
  carried for other installations and for the models the bump now makes possible.

That is a measurement of one installation and not a general claim. What the layer
guarantees regardless is that a label recorded before the bump keeps resolving.

## Consequences

**Good**

- The DRD from #992 deploys and evaluates. `Kunden-Risiko` is a legal decision
  name again, chain or not.
- A model authored in the temis Modeler, Camunda or by hand is accepted on the
  same terms as one authored in Atlas.
- Both spellings resolve at every seam a `decisionId` reaches, including the
  pointers rebuilt after an undeploy and the deploy gate's coverage report.
- temis's own fixes since July come along; the suite is green on them with no
  further change.

**Bad, or merely true**

- Atlas now depends, for a label, on a resolution temis documents as *backward
  compatibility*. If it is dropped, Atlas's index still finds the model and the
  failure is a clear "no decision", not a silent misroute — but it would then need
  its own label→identifier translation. That is the open question above.
- A class-R model (label-referencing chain) that some other installation deployed
  stops compiling. It is diagnosed on reload (ADR-0177) rather than refused at
  startup, and its decision is then addressable nowhere, so a task calling it fails
  per evaluation instead of failing the boot.
- `Aliases` is a second list to keep in step with `Decisions` wherever a model is
  validated. They are produced by one function to keep that from drifting.
- Addressing now reads the DRG graph as well as temis's index, so a registration
  and a deployment-bound lookup each cost one graph build more than before. Both
  run off the processor goroutine, and the evaluation itself builds that graph
  once and hands it to both steps around it.
- The bump raises the module's own Go requirement to 1.25; this repository builds
  on 1.26.

## References

- #992 — the refusal, its measurement and the compatibility question
- ADR-0007 — how the DMN integration sits beside the worker path
- ADR-0063, ADR-0319 — the latest pointer and deploy-time pinning that read this index
- ADR-0177 — why a reload does not re-apply the deploy-time gate
- ADR-0336 — rebuilding the pointers after an undeploy
- `dmn/names.go`, `dmn/names_test.go` — the two-name vocabulary and its tests
