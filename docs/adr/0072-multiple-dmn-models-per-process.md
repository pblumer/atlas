# ADR-0072: Multiple DMN models per process deployment

- **Status:** Accepted
- **Date:** 2026-07-28
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) bundles a business rule task's
DMN model **at deploy time**: the deploy resolves the referenced model, snapshots it
into the deployment record, and registers it in the `dmn.Registry` keyed by the
owning *process-definition key*. The bundling gate (`dmnForDeployBody` →
`matchModel`) required a **single** DMN model to provide **every** decision the
process's business rule tasks reference, and the registry stored **one** model per
process key. The code even said so: *"A draft's decisions must all live in a single
model — spanning models is not supported yet."*

That constraint breaks a completely ordinary process: two business rule tasks, each
calling a decision that lives in its **own** DMN model (say `Begrüssung` in one model
and `Alter prüfen` in another). No single model provides both, so the deploy was
refused —

> this diagram's business rule task(s) reference decision(s) [Begrüssung Alter
> prüfen] that no DMN model provides

— even though each decision individually exists. The refusal is misleading (each
decision *is* provided, just not by one shared model) and the workaround (merge every
decision a process uses into one DMN model) is arbitrary. It became more visible once
the Modeler's decision picker began offering decisions across models
([ADR-0034](0034-projects-and-artifacts.md) scoping was relaxed for the picker), so
an author could easily select two decisions the deploy would then reject.

## Decision

**A process deployment may bundle more than one DMN model.** The bundling gate
selects the *set* of models that together provide every referenced decision, and the
registry holds a **list** of models per process-definition key.

- **Bundling**: `coverModels(models, needed)` replaces `matchModel`. It assigns each
  needed decision to a model that provides it and returns the distinct chosen models
  (one when they share a model, several when they span models). It still refuses —
  409, unchanged message — when a needed decision is provided by **no** model, so a
  business rule task that could never evaluate is never deployed.
- **Registry**: `dmn.Registry` keys `definitions` to `[]*Definitions`; `Deploy`
  **appends** (call it once per bundled model) and a deployment-bound evaluation finds
  the bundled model that declares its decision (`modelProviding`). The per-decision
  `latest` pointer ([ADR-0063](0063-dmn-decision-binding.md)) is unchanged — each
  `Deploy` still updates it, so latest-binding spans models for free.
- **Durability**: the deployment record carries `DMNXMLs []string` (the bundled
  snapshots). A pre-existing record's single `DMNXML` is read as a one-element list,
  so old deployments recover unchanged; recovery re-registers each model under the
  process key, so state after replay equals state built live.

## Consequences

- The natural case — one process, decisions from several models — deploys and runs.
  No more "merge everything into one model" workaround.
- Deployment binding is unaffected in meaning: a process still evaluates against the
  exact models snapshotted with it; there are just possibly several of them, searched
  by decision id.
- Invariants hold: compile stays at deploy (I5) — `coverModels` selects among models
  compiled at deploy, and the registry compiles on `Deploy`; no hot-path work (I1) —
  selection is in the job worker; events-are-facts (I6) — the bundled snapshots live
  in the deployment record and the `latest` lineage is still derived by replaying
  deployments in order, nothing new is stored on the log.
- A decision that is only *deployed* (surfaced in the picker) but has **no reference**
  still cannot be bundled by this gate — bundling resolves references. Closing that
  gap (bundle from the registry's already-deployed models, or accept a latest-bound
  decision that is already deployed) is follow-up work; this ADR covers decisions
  that span *referenced* models, which is the reported case.
