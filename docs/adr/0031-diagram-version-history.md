# ADR-0031: Diagram version history in the Modeler

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The reference modeler has a **Versions** control on every diagram: a history of
saved revisions you can browse, name, compare, and restore. It answers "what did
this model look like last week, who changed it, and can I go back?".

Atlas has two adjacent but different notions today:

- **Deployment versions** (ADR-0019): each *deploy* mints an immutable, numbered,
  runnable definition. These are execution artifacts, not an editing history.
- **Drafts** (ADR-0021): a draft is keyed by process id and **overwritten on every
  save** — so there is deliberately *no* history of edits. Reopen a draft and the
  previous content is gone.

So an author has no way to see or restore an earlier edit; the moment ADR-0021
chose "overwrite, no version spam", it also chose "no edit history". The question:
**how does the Modeler offer a browsable, restorable history of a diagram's
edits**, distinct from both deployment versions and the always-overwritten draft?

## Decision drivers

- **Don't reintroduce version spam.** ADR-0021 overwrites drafts precisely so
  every keystroke-save doesn't mint a version. History must be *checkpoints*, not
  every autosave.
- **Distinct from deployment versions.** Editing history and runnable versions are
  different axes; conflating them (as ADR-0021 already noted for drafts vs.
  deployments) confuses both.
- **Reuse the sidecar persistence style** (ADR-0019/0021) rather than invent a
  third storage mechanism.
- **Cheap to store; a diagram is small XML.**

## Considered options

1. **Named checkpoints** — the current draft stays overwrite-in-place (ADR-0021),
   but an explicit "Save version" action appends an immutable, timestamped
   snapshot (raw XML + optional label) to a per-process-id history list. The
   Versions control browses and restores from it.
2. **Every save is a version** — drop ADR-0021's overwrite and keep all autosaves.
3. **Rely on git / external VCS** — no in-app history.

## Decision outcome

Chosen option: **Option 1 — explicit named checkpoints alongside the overwritten
draft.** The draft store (ADR-0021) is unchanged: the working copy is still one
overwritten record per process id. Layered beside it is an append-only history of
**explicit** snapshots — each an immutable `(process id, sequence, timestamp,
optional label, raw XML)` record in a per-process-id list, stored with the same
atomic-write sidecar approach as ADR-0019/0021. The Modeler's Versions control
lists them, previews one, restores it into the working draft (which is itself just
another overwrite of the draft), and lets the author label a snapshot. Deploying
is still its own axis (ADR-0019); a checkpoint may optionally record the
deployment version it corresponds to, but the two lists stay separate.

Option 2 is rejected because it reopens exactly the version-spam problem ADR-0021
solved. Option 3 is rejected because the audience is diagram authors in the web
UI, many of whom will never touch git, and the draft is server state anyway.

### Consequences

- **Positive:** Browsable, restorable edit history without autosave spam; reuses
  the established sidecar persistence; keeps editing history, drafts, and
  deployment versions as three clear axes; snapshots are immutable and cheap.
- **Negative / trade-offs accepted:** A third list (history) beside drafts and
  deployments — the UI must present them without confusing the user (the same
  reconciliation caveat ADR-0021 accepted). History grows unbounded without a
  retention/prune policy. No per-keystroke history — only explicit checkpoints.
- **Follow-ups / risks to watch:** Retention/pruning of old snapshots; a diff view
  between snapshots; author identity on a snapshot once the UI has accounts; fold
  into the Milestone 4 public API review alongside ADR-0019/0021.

## Links

- builds on ADR-0021 (drafts) and ADR-0019 (durable sidecar persistence)
- distinct from deployment versioning (ADR-0019); relates to ADR-0013 (Modeler)
