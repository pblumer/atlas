# ADR-0127: Process applications — the project, elevated into a deployable, versioned, portable unit

- **Status:** Proposed
- **Date:** 2026-08-17
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0034](0034-projects-and-artifacts.md) introduced the **project**: a
design-time container that groups heterogeneous artifacts (BPMN drafts, DMN
references, forms) under one name, persisted as a JSON sidecar and reloaded on
startup. It is deliberately a **design-time grouping layer** — it never touches
the engine, the WAL, or the invariants. It already carries the two things this
decision builds on:

- a **bundle deploy** — `POST /api/v1/projects/{id}/deploy` validates every
  member artifact and deploys them together, all-or-nothing
  (`api/projectdeploy.go`);
- a per-project view of what is deployed — the Modeler home groups deployed
  process definitions by their `projectId` (`api/web/app.js`).

We now want to reframe this container as the thing users actually reason about:
a **process application** — a named unit of work that is *published as a whole*,
can be *read in from git*, and can be *deployed to other Atlas servers*, with the
deployed state visible **inside** the application rather than in a separate global
list. Three concrete gaps stand between today's "project" and that "application":

1. **Naming and mental model.** Users think in "applications we ship", not
   "projects that happen to group diagrams". The bundle deploy already exists but
   is framed as a secondary action; per-diagram deploy is still the headline path.
2. **No application-level version.** Deployments are versioned per `processId`
   (ADR-0019). There is no single "the application went from v4 to v5" number that
   names *a set of artifact versions published together*.
3. **No portability.** Deploy is always local to the one running engine. There is
   no git import and no deploy-to-another-server. The only cross-instance movement
   is whole-directory backup/restore (ADR-0107) and a full-instance snapshot
   (ADR-0109) — both operator-level, neither an application-scoped release.

The question: **how do we elevate the project into a "process application" — the
unit of bundling, versioning, and portability (git + remote deploy) — without
reversing ADR-0034's "design-time layer, below the HTTP API, invariants
untouched" stance, and without a forced data migration?**

## Decision drivers

- **Stay below the HTTP API, invariants untouched.** Like ADR-0034, this is a
  design-time organizing concern. It must not appear in the event log, must not
  affect recovery, and must not cross into the processor, the WAL, `applyToState`,
  or any of the six invariants (`docs/architecture/invariants.md`). Git and remote
  deploy are network side effects — they belong in the server's post-API layer,
  never on the hot path.
- **Backward compatible, no migration.** Every existing project, its `projectId`
  tags on artifacts, and its sidecar files must keep working untouched. An
  "application" is the same record seen through a new frame, not a new store that
  needs a migration step (ADR-0034's compatibility driver still holds).
- **Reuse the on-disk serialization.** ADR-0107 established that the sidecar JSON
  files *already are* the portable serialization of design-time work. Git import
  and remote deploy should move those same bytes, not invent a parallel schema
  (ADR-0107 option C is still the wrong path).
- **Reuse the existing deploy API for remote targets.** A remote Atlas already
  exposes `POST /api/v1/deployments` and the project bundle deploy. Deploying "to
  another server" should be the local server calling the remote's existing API,
  not a new engine-to-engine protocol.
- **Incremental, vertical slices.** Ship the rename first (pure reframe, zero
  behaviour change), then application releases, then the two genuinely new
  transports (remote targets, git) as separate slices — each shippable alone, each
  reversible if it stalls.
- **Don't smuggle a big new surface through a rename.** Remote deploy and git sync
  each introduce outbound network, credentials, and failure modes. They may each
  warrant their **own** ADR for the transport/auth details — this ADR fixes the
  *shape* (they attach to the application), not every wire detail.

## Considered options

1. **Rename in the UI only.** Relabel "Project" → "Application" in `app.js`; leave
   the API, MCP tools, and storage as `projects`.
2. **Elevate the project into an application end-to-end (chosen).** Rename the
   domain concept, HTTP API, and MCP surface to "application"; keep the on-disk
   `projects/` dir and the `projectId` artifact tag unchanged for zero-migration
   back-compat; add an application-level release version, git source binding, and
   remote deployment targets as additive, phased slices.
3. **A new nested "application" folder *inside* projects.** Keep projects and add a
   second grouping layer beneath them.
4. **Application as a first-class engine/event concept.** Model applications and
   releases as durable records in the log, deployed via events.

## Decision outcome

Chosen option: **"Elevate the project into a process application end-to-end"
(option 2).**

A **process application** is what ADR-0034's project becomes: the design-time unit
of grouping *and* of bundling, versioning, and portability. Nothing about its
place in the architecture changes — it stays a sidecar-backed, design-time layer
below the HTTP API, absent from the event log and recovery. What changes is its
scope of responsibility and three additive capabilities.

### Naming and storage — reframe, don't migrate

- The **domain concept, HTTP API, and MCP tools** are renamed to *application*:
  new routes under `/api/v1/applications`, new MCP tools `atlas_*_application`.
  The existing `/api/v1/projects` routes and `atlas_*_project` tools remain as
  **thin deprecated aliases** for one release, so external callers and saved
  scripts keep working while they migrate.
- The **on-disk representation is unchanged**: the sidecar directory stays
  `projects/`, and artifacts keep their optional `projectId` tag. Renaming bytes
  on disk is a migration with only cosmetic upside and real risk; ADR-0034's
  "no migration" driver wins. The name skew (disk says `project`, API/UI say
  `application`) is documented and deliberate, and a later, separate slice may do
  a one-time startup rename if the skew proves costly. The protected `system`
  project (ADR-0122) is likewise reframed as the protected *system application*
  with no change to its reserved id or guard.

### Capability 1 — the application is the publish unit, with its own version

- The **bundle deploy becomes the headline path.** "Publish application" validates
  every member artifact and deploys them together (the existing
  `handleDeployProject` all-or-nothing flow), and it is what the primary Publish
  button invokes. Per-diagram deploy remains for quick iteration but is demoted to
  a secondary action.
- Publishing mints an **application release**: a new sidecar record capturing *this
  bundle's* version number (a per-application counter, `v1, v2, …`), the set of
  member artifact keys and their per-process versions (ADR-0019, unchanged
  underneath), a timestamp, the actor, and an optional changelog note. Releases are
  design-time metadata — a manifest of "what we shipped together", not an engine
  fact. The per-`processId` version stays exactly as ADR-0019 defines it; the
  application version is a bundle-level index layered above it.

### Capability 2 — remote deployment targets

- A **deployment target** is a named remote Atlas endpoint (URL + a vault-stored
  credential, reusing the ADR-0069/0070 secret vault), persisted as its own
  design-time sidecar. Targets model the environments a user already thinks in:
  *this server*, *Test*, *Production*.
- **Deploying to a target** is the local server calling the *remote's existing*
  deploy API with the application's bundle — no new engine-to-engine protocol, no
  invariant surface. It runs as a post-API side effect (like any outbound
  connector call), never on the hot path.
- The application's **Deployments view** shows, per target, the live application
  version and running-instance count, by querying each target's existing
  `GET /api/v1/processes` and instances-summary endpoints. This is the concrete
  meaning of "deployed apps are visible inside the application" — the global
  per-project deployed list from ADR-0034 moves *into* the application, keyed by
  target. It relates to ADR-0105's per-server call-activity overrides (both are
  "this definition, on that server" operator state) but is distinct: targets are
  *where an application deploys*, not how one call activity resolves.

### Capability 3 — git source binding

- An application may be **bound to a git repository** (URL + branch + a vault-stored
  credential). Binding is optional; an unbound application is exactly today's
  local-only project.
- **Import/sync** reads the application's artifacts from a checked-out working copy
  and **commit/push** writes local changes back, moving the *same sidecar JSON the
  backup archive already serializes* (ADR-0107) rather than a parallel format. Git
  I/O is a design-time server side effect, off the hot path.
- The application is the natural git unit: one repo ⇄ one application, so its
  release history and its commit history line up.

### Phasing (each slice ships alone)

- **Phase 1 — Reframe (no behaviour change).** `/api/v1/applications` +
  `atlas_*_application` tools aliasing the project layer; UI relabel (Applications
  list, application detail, "Nicht zugewiesen" for the ungrouped bucket); make
  bundle deploy the headline Publish action. Pure rename slice — covered by the
  ADR-0018 mechanical-edit exception only where truly behaviourless; the aliasing
  and Publish-path change get tests.
- **Phase 2 — Application releases.** The release sidecar + per-application version
  counter, the Publish dialog (version bump + changelog), and the release-history
  view. Deployments tab shows *local* live version/instances from existing
  `/processes` data.
- **Phase 3 — Remote deployment targets.** Target sidecar + vault credential,
  remote bundle deploy, per-target live-status view. **Candidate for its own ADR**
  for the remote auth/transport and partial-failure semantics.
- **Phase 4 — Git source binding.** Bind/import/sync/commit over the ADR-0107
  serialization. **Candidate for its own ADR** for the git transport, conflict
  handling, and credential model.

### Consequences

- **Positive:** Users get the mental model they actually hold — an application that
  ships as a unit, carries a version, and can travel to other servers or from git.
  Bundling and the deployed-state view already exist and are merely reframed and
  relocated; nothing in the engine, the log, or recovery is touched; existing
  projects keep working with no migration; git and remote deploy reuse the ADR-0107
  serialization and the remote's existing deploy API rather than new protocols.
- **Negative / trade-offs accepted:** A deliberate name skew between disk
  (`projects/`, `projectId`) and API/UI (`application`), carried for
  compatibility; two more sidecar stores (releases, targets) to reload on startup;
  deprecated `/projects` aliases to maintain for a release; git and remote deploy
  introduce outbound network, credentials, and new failure modes that the local-only
  model never had.
- **Follow-ups / risks to watch:** partial-failure semantics of a multi-target
  publish (target 2 fails after target 1 succeeded); how a remote target's identity
  and credential are trusted and rotated; git conflict/merge handling when both
  sides changed an artifact; whether the disk rename is eventually worth a one-time
  startup migration; and keeping the deprecated `/projects` alias from ossifying.

## Pros and cons of the options

### Option 1 — rename in the UI only
- Good: cheapest possible change; nothing server-side to touch.
- Bad: the API and MCP tools still say `projects`, so the concept is inconsistent
  the moment anyone leaves the UI; buys none of versioning, git, or remote deploy —
  it is a label, not the reframe the feature needs.

### Option 2 — elevate project → application end-to-end (chosen)
- Good: one coherent concept across UI, API, and MCP; reuses the proven sidecar
  mechanism, the existing bundle deploy, and the ADR-0107 serialization; zero
  engine/invariant impact; backward-compatible via unchanged storage + deprecated
  aliases; naturally incremental (rename → releases → targets → git).
- Bad: a name skew between disk and API/UI; more design-time stores; new network
  side effects (git, remote deploy) to get right.

### Option 3 — a new nested "application" folder inside projects
- Good: leaves the project concept literally untouched.
- Bad: creates two grouping layers (project → application → artifact) and the
  question "which one is the publish unit?"; ADR-0034 deliberately deferred real
  nesting; more concepts for users to learn, for no gain the reframe doesn't give
  more cleanly.

### Option 4 — application as a first-class engine/event concept
- Good: releases would be durable, replayable facts; a bundle deploy could be one
  event.
- Bad: pulls a pure design-time concern onto the hot path and into the log for no
  runtime benefit; risks the invariants; far heavier than the problem warrants.
  This is exactly ADR-0034 option 1, rejected for the same reason — applications
  have no execution semantics and don't belong in the event-sourced core.

## Links

- extends [ADR-0034](0034-projects-and-artifacts.md) (projects and artifacts — the
  container this decision elevates; storage and the design-time-layer stance are
  inherited unchanged)
- relates to [ADR-0019](0019-durable-deployments.md) (per-`processId` deployment
  versioning — the application release version layers above it, does not replace it)
- relates to [ADR-0107](0107-backup-and-restore.md) and
  [ADR-0109](0109-full-instance-snapshot.md) (the on-disk serialization git import
  and remote deploy reuse; portability today vs. application-scoped portability)
- relates to [ADR-0105](0105-per-server-call-activity-target-overrides.md)
  (per-server operator state — deployment targets are adjacent but distinct)
- relates to [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) /
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md) (where remote-target
  and git credentials live)
- relates to [ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md)
  (the system project, reframed as the system application, guard unchanged)
- relates to [ADR-0016](0016-mcp-server-over-http-api.md) (the MCP surface that
  gains `atlas_*_application` tools)
