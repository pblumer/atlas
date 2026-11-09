# ADR-0544: Every process, decision and form belongs to an application

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-09
- **Deciders:** pblumer (maintainer; asked on 2026-11-09 that no process, decision or form be created or run outside an application any more, and that what already is be cleaned up, so that an application can always be moved, deleted and deployed as a whole)

## Context and problem statement

[ADR-0034](0034-projects-and-artifacts.md) made the application tag of an artifact
(`projectId`) optional, so that the drafts and deployments written before applications
existed kept working without a migration, and collected the untagged ones in an implicit
bucket, today's **Not assigned**. [ADR-0128](0128-process-applications.md) turned the
project into the process application — the unit that is published, versioned, exported
and deployed to other servers — and kept the tag optional for the same reason.

The optionality outlived its reason. Nothing stopped a *new* artifact from being created
untagged: the Modeler home's "Create new" offered "Process (BPMN)" and "Form" outside any
application, a raw `POST /api/v1/deployments` filed nowhere, every move route accepted an
empty `projectId` as "take it out", and deleting an application left everything it held
behind under Not assigned. On the maintainer's server Not assigned had grown to 90
artifacts and 152 deployed processes, more than any application holds.

What sits there falls out of everything an application exists for: it is not published
with an application, not exported in its package, not promoted to another server, not
purged with it ([ADR-0468](0468-deleting-an-application-with-its-contents.md)), and its
application constants ([ADR-0453](0453-application-values-per-installation.md)) resolve
against nothing. The question: **must every process, decision and form belong to an
application, and if so, what happens to what already does not?**

## Decision drivers

- An application has to be movable, deletable and deployable as a whole; that holds only
  if nothing it needs lives outside it.
- Nothing already running may stop: instances of an unfiled definition run to their end.
- Which application a process belongs to is a statement about who owns it. The server
  cannot infer it, so it must not guess.
- The rule holds at the server, not only in the Console: the API and the MCP tools are
  doors of their own, and a rule one of them skips is no rule.
- Design-time only. The tag is design-time metadata below the HTTP API; nothing here may
  touch the log, recovery or any of the six invariants.

## Considered options

1. **Keep the tag optional** and only take the blank entries off the Modeler home.
2. **Require an application in the Console only**; the API stays tolerant.
3. **Require an application at the server for everything new**, keep what exists running,
   and give an explicit way to file it.
4. **As 3, and file every unfiled artifact automatically** into a generated application
   ("Legacy") at startup.

## Decision outcome

Chosen option: **3**, because it is the only one in which the rule holds for every door
and nothing is filed on anybody's behalf.

### The rule

A process, decision or form is not **created**, **deployed** or **moved** outside an
application:

- `POST /api/v1/drafts`, `/forms`, `/dmnrefs`, `/dmn-drafts`, `/decision-deployments` and
  `/imports/mim` refuse a new artifact without a `projectId` with 400.
- `POST /api/v1/deployments` files a model under the application `?projectId=` names; without
  it, under its draft's application, else under the application of the process's earlier
  versions — so a redeploy through the API does not have to repeat what the server knows —
  and refuses a model that has none of the three. A catalogue import that carries processes
  goes through the same door and inherits the same way.
- The move routes (`PATCH /drafts/{id}`, `/processes/{key}`, `/dmnrefs/{id}`) refuse an empty
  `projectId`; a form saved without one keeps the application it is in.
- `DELETE /api/v1/applications/{id}` without `?purge=true` is refused with 409 while the
  application still holds deployed versions, decision deployments, drafts, decision drafts,
  decision references or forms. They are moved to another application first, or go with it in
  a purge. ADR-0034's "artifacts fall back to Ungrouped" is withdrawn.

### What already belongs to no application

An artifact belongs to no application when it carries no tag or names an application that
no longer exists. It keeps working: its instances run, its draft may still be saved, its
form may still be filled in. It is **not** filed automatically. It is listed by
`GET /api/v1/unassigned` and filed by `POST /api/v1/applications/{id}/adopt`, which takes,
per kind, the ids to file under that application:

- a deployed process by process id, with every version and the other pools of its
  collaboration — the same scope as `PATCH /processes/{key}` — and its definitions then read
  that application's constants;
- a decision reference together with its unfiled decision drafts, because a decision draft
  belongs where its decision does.

Adopt checks everything before it writes anything: one artifact that does not exist or
already belongs to an application refuses the request with 409, naming each. It is not a
move route — what is filed is moved with the routes above — so it cannot empty somebody
else's application. It needs editor on the application and on each artifact's current scope
([ADR-0071](0071-sharing-scopes.md)), and the protected system application
([ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md)) is not a destination.
The Modeler's Not assigned page offers it as "Assign to an application…"; the MCP tools are
`atlas_list_unassigned` and `atlas_adopt_artifacts`.

In the Console, "Create new" asks which application a new process, form, decision or import
goes into, and any address that opens a new one without naming an application asks first.

### Tests

The `api` package's tests build their servers without the rule (`TestMain`, the way it
already lowers the password cost): the engine behaviour most of them cover is not about
filing, and several hundred deploy a bare model. The rule's own tests turn it on, and
`TestAServerRequiresAnApplicationByDefault` holds the production value, a constant. Every
other package — the MCP adapter, the command line, the Worker, the benchmarks — runs against
the production default and files what it creates.

### Scale-out assessment

The tag is design-time metadata in the sidecar stores, read and written on the run loop of
the one server that holds them, like every move before it. Adopt is a sequence of the same
per-record writes a move makes (durable before visible per record); a failure part-way leaves
what was written correctly filed, and repeating the request files the rest. Nothing enters the
log or partition state, so partition routing, recovery and the migration seam to replicated
cells ([ADR-0175](0175-replicated-partition-cells.md)) are unaffected; the design-time stores'
own placement in a multi-node deployment is the open question it was before.

### Consequences

- **Positive:** an application holds everything it needs, so publishing, exporting,
  promoting and purging it is complete; Not assigned can only shrink; the rule is the same
  for the Console, the API, the MCP tools and the command line.
- **Negative / trade-offs accepted:** a breaking change to the HTTP API and to the MCP tools:
  a script or agent that deploys or saves without naming an application now gets 400 and has
  to name one. A plain delete of a non-empty application takes one step more. The package's
  tests run without the rule, so it is covered by its own tests and by every other package's,
  not by the `api` package's whole suite.
- **Follow-ups / risks to watch:** the Console offers no move for a deployed process between
  applications (the API has one); a stored DMN model file has no application of its own and is
  filed through the reference that points at it; Panorama and information models already had
  strict ownership ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md)) and are unchanged.

## Pros and cons of the options

### Option 1 — keep the tag optional, trim the home menu
- Good: smallest change, nothing breaks.
- Bad: every other door still creates unfiled artifacts; Not assigned keeps growing.

### Option 2 — require it in the Console only
- Good: no API break.
- Bad: the API, the MCP tools and imports still file nothing, which is how most of the
  estate was made; a rule only one door keeps is a convention.

### Option 3 — require it at the server, adopt explicitly (chosen)
- Good: one rule for every door; nothing is filed on anybody's behalf; what runs keeps running.
- Bad: an API break; the clean-up is work a person has to do.

### Option 4 — as 3, and file everything into "Legacy" at startup
- Good: Not assigned is empty at once.
- Bad: it renames the problem — "Legacy" is Not assigned with a different name, now a real
  application that publishes, exports and is deleted as a unit it never was; and it files
  processes under an owner nobody chose.

## Links

- amends [ADR-0034](0034-projects-and-artifacts.md) (the tag is no longer optional for anything
  new; a deleted application no longer leaves its artifacts behind)
- amends [ADR-0128](0128-process-applications.md) (the application is the unit everything is
  filed under, not only what is published)
- relates to [ADR-0071](0071-sharing-scopes.md), [ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md),
  [ADR-0453](0453-application-values-per-installation.md), [ADR-0468](0468-deleting-an-application-with-its-contents.md),
  [ADR-0541](0541-the-application-holds-its-deployments.md)
