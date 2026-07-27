# ADR-0063: DMN decision binding (latest vs deployment)

- **Status:** Accepted
- **Date:** 2026-07-25
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) snapshots a business rule
task's DMN model **at deploy time**: `deployModel` resolves the referenced model,
registers it in the `dmn.Registry` keyed by the owning *process-definition key*,
and the worker evaluates against that snapshot (`reg.Evaluate(cp.Key, decisionId,
…)`). One model per process deployment; the decision is frozen with the process
version and there is no notion of a decision version anywhere in the runtime.

That is the right default for reproducibility, but it is not the only thing authors
want. In Camunda a business rule task's `zeebe:calledDecision` carries a
**`bindingType`** — `latest`, `deployment`, or `versionTag` — so a task can either
pin to the decision deployed alongside it or always evaluate the newest deployed
decision. The zeebe moddle Atlas already vendors defines `bindingType` (default
`"latest"`), so the editor can author it, but the compiler **ignores it entirely**.
The result is that every decision is implicitly `deployment`-bound with no way to
say "always use the latest version of this decision".

## Decision

**Honor `bindingType` on `zeebe:calledDecision`, with `latest` and `deployment`
semantics.** (`versionTag` — pinning to a specific numbered version — is deferred;
it needs versioned model storage, which Atlas does not yet have.)

- **`deployment`** — evaluate the decision model **snapshotted with this process's
  deployment** (the ADR-0014 behavior). Fully self-contained and reproducible: it
  survives restart from the deployment record and never changes once deployed.
- **`latest`** (the default, matching Camunda) — evaluate the **newest deployed
  version** of the decision. When a later process deployment bundles a changed
  decision of the same id, tasks bound `latest` — including those in
  already-deployed processes — pick it up; `deployment`-bound tasks keep their
  snapshot.

"Newest deployed" is defined by deploy order: the `dmn.Registry` keeps, in addition
to the per-process-key snapshots, a **latest pointer per decision id** updated on
every `Deploy`. Because deployments reload oldest-first on restart (deploy records
sorted by ascending key), the latest pointer is rebuilt deterministically — the
event log stays the source of truth (I6); nothing new is persisted.

For a decision that has only ever been deployed once, `latest` and `deployment`
resolve to the same model, so defaulting to `latest` (Camunda's default) does not
change the behavior of any existing process.

### Where it hooks in

- **Compiler**: `xmlCalledDecision` reads `bindingType`; the business-rule-task
  parse records it on `BusinessRuleTaskDetail.Binding` (`BindingLatest` /
  `BindingDeployment`, default latest).
- **Registry**: `dmn.Registry` gains `latest map[decisionId]*Definitions`, updated
  by `Deploy`, and `EvaluateLatest(decisionId, …)` alongside the existing
  per-def-key `Evaluate`.
- **Runtime** (the single seam): the local worker's `Bind` closure
  (`dmn/worker.go`) chooses `reg.Evaluate(cp.Key, …)` for `deployment` and
  `reg.EvaluateLatest(…)` for `latest`, driven by the compiled `detail.Binding`.
  Resolution stays off the processor goroutine (ADR-0007) and compile stays at
  deploy (I5) — `latest` selects among already-compiled models, it does not
  recompile.

## Consequences

- Authors get Camunda-style control: pin a decision (`deployment`) or track the
  newest (`latest`) — surfaced as a "Binding" dropdown on the business rule task.
- Central (temis-connector) decisions are unaffected: they carry no local snapshot
  and resolve through the connector at runtime, so binding does not apply to them.
- Invariants hold: no hot-path work (I1) — binding is resolved in the job worker;
  one `applyToState` (I4) untouched; compile-at-deploy (I5) — the registry compiles
  on `Deploy`, and `latest` only *selects* a compiled model; events-are-facts (I6)
  — the latest lineage is derived from replaying deployments in order, not stored.
- Deferred: `versionTag` binding (pin to a numbered version) and independent
  decision versioning / a decision-version catalog. `latest` covers "always the
  newest"; `deployment` covers "pin to what I deployed with". A numbered-version
  pin is the natural Slice 2 once models are stored with version history.
