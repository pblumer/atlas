# ADR-0024: Projects as containers for heterogeneous artifacts

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The Modeler today presents a **flat** workspace. There are two independent,
process-id-keyed lists:

- **Drafts** — raw, uncompiled BPMN XML, one JSON file per process id
  (`api/draftstore.go`, [ADR-0021](0021-diagram-drafts.md)).
- **Deployments** — compiled, versioned, runnable definitions, keyed by
  key/version ([ADR-0019](0019-durable-deployments.md)).

Both are BPMN-only and ungrouped: everything a user creates lands in one global
heap keyed by `processId`. There is no way to say "these diagrams belong
together", to name a body of work, or to deploy a related bundle as a unit.

Mainstream modeling tools (e.g. Camunda's Web Modeler) organize work into a
**project**: a named container that holds **artifacts of several types** — BPMN
diagrams, DMN decisions, forms, RPA scripts, element templates, READMEs, nested
folders — and can deploy the bundle in one action. We want to move Atlas toward
that shape, but incrementally: not every artifact type at once, and without
reversing standing architectural decisions by accident.

The question: **how do we introduce a project/artifact organizing concept over
the existing Modeler and storage, starting small (BPMN, then DMN), without
touching the engine invariants or silently reversing the "no DMN authoring
surface" non-goal?**

## Decision drivers

- **Grouping without engine impact.** The concept is a design-time organizational
  layer. It must live entirely **below the HTTP API**, in the server and web UI,
  and must not touch the processor, the WAL, `applyToState`, or any of the six
  invariants (`docs/architecture/invariants.md`).
- **Reuse the durable-sidecar mechanism.** Drafts and deployments already persist
  as atomic-write JSON sidecars reloaded on startup (ADR-0019/0021). A third
  concept should reuse that style, not invent a fourth persistence mechanism.
- **Backward compatibility.** Existing drafts and deployments predate projects
  and carry no project id. They must keep working — un-grouped, in a default
  bucket — with no migration step required.
- **Respect the standing DMN non-goal.** The ROADMAP names "a standalone DMN
  authoring/product surface" as an explicit non-goal; Atlas *executes* DMN via
  temis business rule tasks ([ADR-0014](0014-dmn-business-rule-tasks-via-temis.md)),
  it does not ship a DMN modeler. A "DMN artifact" in a project must not
  smuggle a DMN editor in through the back door.
- **Incremental, vertical slices.** Each phase should ship something that runs:
  the container first (BPMN only), richer artifact types later, one at a time.

## Considered options

1. **Project as a first-class engine/event concept.** Model projects and
   artifacts as durable records in the log, deployed via events.
2. **Project as a design-time grouping layer** — a `projectStore` sidecar
   alongside the existing draft/deployment stores; artifacts stay the existing
   drafts/deployments plus an optional `projectId` tag; the project owns only
   identity, name, and membership. UI groups by project; a projectless artifact
   falls into a default bucket.
3. **Client-only projects.** Group in the browser (localStorage); no server
   state.

## Decision outcome

Chosen option: **"Project as a design-time grouping layer" (option 2).**

A **project** is a named container identified by a stable `projectId`, persisted
as one JSON sidecar per project under `<data-dir>/projects/`, using the same
atomic-write + reload-on-startup approach as ADR-0019/0021. It owns only
organizational metadata:

```
project { id, name, createdAt, updatedAt }
```

An **artifact** is *not* a new storage entity. It is an existing draft or
deployment that gains an **optional `projectId`** field. Membership is a tag on
the artifact, so:

- artifacts without a `projectId` (all pre-existing ones) render in an implicit
  **"Ungrouped"** bucket — no migration needed;
- deleting a project does **not** delete its artifacts by default (they fall back
  to Ungrouped) — deletion of runnable definitions stays the deliberate,
  separate action it is today.

**Artifact types are introduced one at a time:**

- **Phase 1 — BPMN only.** Wrap the existing BPMN draft/deploy flow in a project
  container: create/list/rename projects, list a project's BPMN artifacts, and
  keep the existing per-diagram deploy. New endpoints under `/api/v1/projects`;
  the draft/deployment records gain the optional `projectId`. No new artifact
  editor.
- **Phase 2 — DMN as a *reference*, not an editor.** A DMN artifact in an Atlas
  project is a **reference to a temis model** (the id/handle temis already
  exposes, ADR-0014), which Atlas lists and can include in a bundle deploy.
  Authoring stays in temis's own modeler. This honors the ROADMAP non-goal:
  Atlas organizes and executes DMN; it does not author it. Embedding a DMN editor
  (`dmn-js`) would reverse that non-goal and needs its **own** ADR, not this one.
- **Later phases — forms, element templates, README/docs, nested folders** —
  each its own vertical slice and, where it introduces a genuinely new authoring
  surface, its own ADR.

The project layer is purely additive design-time state. It does not appear in the
event log, does not affect recovery, and does not cross the HTTP API into the
engine. The invariants are untouched because no hot path, no `applyToState`, and
no durable-before-visible ordering is involved.

### Consequences

- **Positive:** Work can be named and grouped; related diagrams live together and
  (later) deploy as a bundle; pre-existing artifacts keep working un-grouped with
  no migration; one persistence style across drafts, deployments, and projects;
  the DMN non-goal is preserved by treating DMN as a reference.
- **Negative / trade-offs accepted:** A third sidecar store to keep consistent on
  startup; `projectId` is a soft tag, so a project and its artifacts can drift
  (an artifact tagged with a deleted project id must degrade gracefully to
  Ungrouped); "bundle deploy" is deferred past Phase 1, so Phase 1's value is
  organizational only.
- **Follow-ups / risks to watch:** atomic multi-artifact ("bundle") deploy;
  whether folders warrant real nesting or stay a flat label; how a temis DMN
  reference is validated/resolved at deploy time; and a firm decision on whether
  Atlas ever embeds a DMN editor (separate ADR if so).

## Pros and cons of the options

### Option 1 — first-class engine/event concept
- Good: projects would be durable, replayable facts; bundle deploy could be a
  single event.
- Bad: pulls a pure design-time organizing concern onto the hot path and into the
  log for no runtime benefit; risks the invariants; far heavier than the problem
  warrants. Projects have no execution semantics — they don't belong in the
  event-sourced core.

### Option 2 — design-time grouping layer (chosen)
- Good: reuses the proven sidecar mechanism; zero engine/invariant impact;
  backward-compatible via optional tag + Ungrouped bucket; naturally incremental
  per artifact type.
- Bad: soft membership can drift; another store to reload on startup.

### Option 3 — client-only projects
- Good: no server work.
- Bad: grouping evaporates on another browser/machine and does not survive — the
  same "it's gone" failure ADR-0019/0021 were created to avoid.

## Links

- relates to [ADR-0019](0019-durable-deployments.md) (durable deployments — same
  sidecar mechanism)
- relates to [ADR-0021](0021-diagram-drafts.md) (diagram drafts — same sidecar
  mechanism, the other thing a project groups)
- relates to [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) (DMN via temis
  — why a DMN artifact is a reference, not an editor)
- relates to [ADR-0011](0011-single-binary-distribution-and-web-ui.md) and
  [ADR-0012](0012-web-ui-app-shell.md) (the Modeler UI the project view lives in)
