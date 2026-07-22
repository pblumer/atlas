# ADR-0020: Diagram drafts, separate from deployments

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas maintainers

## Context and problem statement

The Modeler could only *deploy* a diagram (compile it, register it, and start an
instance) or *export* it as a local file. There was no way to save work in
progress on the server. A model that is not yet executable — a lone start event,
a service task without a job type — cannot be deployed at all, because the
compiler rejects it (ADR-0004). So a user drawing a new diagram had nowhere to
put it: closing the tab or navigating away lost the work, and users repeatedly
asked "where is the save button?".

The question: how does the Modeler persist a diagram that is not (yet) a
deployable definition, so it survives and can be reopened?

## Decision drivers

- **Save must not require a compilable model.** The whole point is to keep
  incomplete work.
- **No version spam.** Saving repeatedly while editing must not create a new
  version each time, the way deploying does.
- **Reuse the durable-sidecar mechanism** already established for deployments
  (ADR-0019) rather than invent a second persistence style.
- **Keep drafts and deployments conceptually distinct** — one is editable work in
  progress, the other is an immutable, versioned, runnable definition.

## Considered options

1. **"Save" = deploy without starting an instance.** Reuse the deployment store.
2. **A separate draft store**, keyed by process id, storing raw uncompiled XML,
   overwriting on re-save.
3. **Client-only persistence** (localStorage / file download). No server state.

## Decision outcome

Chosen option: **"A separate draft store" (option 2)**. A draft is the raw BPMN
XML plus display metadata (process id, name, save time), stored one JSON file per
process id under `<data-dir>/drafts/`, using the same atomic-write, reload-on-
startup sidecar approach as ADR-0019. Drafts are **not compiled**, so incomplete
models save fine; they are **keyed by process id and overwritten** on re-save, so
editing never produces versions. The Modeler lists drafts separately from
deployed processes and can reopen one into the editor. Deploying remains a
distinct action that compiles, versions, and runs.

Option 1 was rejected because it can't hold a non-compilable model and every save
would mint a version. Option 3 was rejected because the work would not survive on
the server, defeating the point (and the same class of "it's gone after restart"
complaint ADR-0019 addressed).

### Consequences

- **Positive:** Work in progress is saved and reopened; incomplete models are
  fine; no version churn; the deploy path is unchanged; one persistence style
  across drafts and deployments.
- **Negative / trade-offs accepted:** A draft and a deployment can share a
  process id and coexist (a saved edit of an already-deployed process); the UI
  shows them in separate sections rather than reconciling them. Drafts are not
  event-sourced (same interim status as ADR-0019).
- **Follow-ups / risks to watch:** When the Milestone 4 public API arrives, fold
  draft storage into the same review as deployment persistence.

## Links

- builds on ADR-0019 (durable deployments via an on-disk sidecar store)
- relates to ADR-0011 (single-binary web UI), ADR-0013 (embedded bpmn-js modeler)
