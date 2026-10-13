# ADR-DRAFT: The Modeler says which version it shows, and Deploy saves the draft

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-13
- **Deciders:** Atlas maintainers

## Context and problem statement

A process exists in Atlas as two kinds of record that share a process id and nothing else
([ADR-0021](0021-diagram-drafts.md), [ADR-0019](0019-durable-deployments.md)):

- **the draft** — one per process id, overwritten by every Save, uncompiled, the copy an
  application lists and publishes ([ADR-0128](0128-process-applications.md));
- **the deployed versions** — one per Deploy, immutable, what instances run.

Save writes the draft and nothing else. Deploy, from the Modeler, sent the diagram on the
canvas to `POST /api/v1/deployments` and did not touch the draft. Operations and the Modeler
home open a version (`#/modeler/d/{key}`); an application opens the draft
(`#/modeler/draft/{processId}`). Nothing on screen said which of the two was open.

This was reported as a change that "was suddenly gone": it was visible in the deployed process
opened in the Modeler, and missing from the process opened through its application, with no
way to tell whether the Modeler had picked up an old version. The mechanism that produces
exactly that is a deploy that did not go through Save — a change made on a draft and deployed
without pressing Save, made on a version opened from Operations and deployed from there, or
deployed by an agent through `atlas_deploy`. The newest version then holds the change and the
draft does not. Worse than invisible: publishing the application deploys its drafts, so the
next publish mints a version *without* the change, silently.

The reporter also asked to see who created the process and who last changed it. The draft
recorded its creator (`OwnerID`, [ADR-0071](0071-sharing-scopes.md)) but
not who last saved it; a version recorded its deployer (`DeployedBy`,
[ADR-0205](0205-connector-ownership-and-event-delivery.md)) but no route answered it.

## Decision drivers

- **Say which record is open, where the author looks.** The question arises while editing; an
  answer in another view is an answer nobody reads.
- **The comparison is the server's.** "Same model" must not depend on formatting, attribute
  order or the diagram, and the server already has that comparison for layout saves
  ([ADR-0251](0251-adjust-a-deployed-diagram.md), `layout.SameModel`).
- **Name people, not ids.** An opaque principal id is not "who to ask".
- **Remove the cause where it is cheap to remove**, and say plainly where it is not removed.

## Considered options

1. **A chip in the editor bar backed by a server-side comparison**, plus *Deploy saves the
   draft first* for a session that addresses a draft or a new diagram.
2. The chip alone, leaving Deploy as it was.
3. A full autosave of the draft while editing.
4. Versioned drafts — a history of every save.

## Decision outcome

Chosen option: **1**, because it answers the question where it is asked and removes the common
way the two records part, without changing what a draft is.

**What is shown.** `api/web/shown-version.js` puts a chip after the breadcrumbs:

| Open | Chip | Meaning |
|---|---|---|
| draft | `Draft · not deployed` | no version exists |
| draft | `Draft = v7` | the draft is the model v7 runs |
| draft | `Draft · v7 + changes` | the draft holds work saved after v7 |
| draft | `Draft ≠ v7` (warning) | v7 was deployed *after* the draft's last save and differs — the draft lacks a deployed change |
| version | `v7 · newest` / `v7 of 9` (warning) | which of how many |
| new | `Not saved` | nothing stored yet |

Its panel names who created the draft and who last saved it, who deployed the version and who
last adjusted its layout, the first difference, and links to the other record. An equal second
on "deployed after the last save" counts as after: a missed warning costs more than a
needless one.

**What the server answers.**

- A draft records `savedBy`, the principal whose save wrote its content. Save, the MIM import
  and an application source import stamp it; a move does not, because a move is not a change.
- `GET /api/v1/drafts` (and the save and move responses) answer `ownerId`/`ownerName` and
  `savedBy`/`savedByName`. `GET /api/v1/processes` answers `deployedBy`/`deployedByName` and
  `diagramUpdatedByName`. Names are resolved after the run loop's turn, once per distinct
  principal per request (`principalNamer`).
- `GET /api/v1/drafts/{id}/comparison[?key=]` compares a draft with the newest version of its
  process, or the one named, by `layout.Difference` — `SameModel`'s walk with the first
  difference spelled out and each side named. It needs viewer on the draft's scope, like the
  draft's XML, and the scope is checked before anything about the key is said. The two
  documents are parsed off the loop. MCP: `atlas_compare_draft`.

**What Deploy does.** From a session that addresses a draft, or from a new diagram, Deploy
saves first through the same path as Save, and deploys nothing if that save fails. It saves
only when the canvas differs from what this session last saved, so a repeated deploy does not
re-stamp "last saved by". A changed process id makes the save a rename, which reopens the
editor; Deploy then asks for Save first instead of racing that navigation.

A version opened read-only is **not** saved on Deploy: saving it would overwrite a draft
nobody asked to replace, possibly holding someone else's unpublished work. The Deploy panel
says that the draft is left as it is and points at Save; the chip then shows how the draft
stands against the version.

### Why not the others

- **The chip alone** (2) makes the problem visible after the fact and leaves its most common
  cause in place.
- **Autosave** (3) would save half-finished edits into the record a publish deploys, turn every
  pause into a reimport for co-editors ([ADR-0140](0140-live-collaborative-modeling-sessions.md)),
  and still not help when the change was deployed from a version or by an agent. Saving at the
  one moment the author commits to the model — Deploy — gives the guarantee that matters
  without those costs.
- **Versioned drafts** (4) would let a lost change be recovered rather than prevented; it is a
  larger change to the draft store and is not needed for the reported case.

### Consequences

- **Positive:** the open record, its standing against the other, and the people behind both are
  visible in the editor. A deploy from a draft leaves the draft equal to what was deployed, so
  the next publish deploys the same model again rather than an older one.
- **Negative / trade-offs accepted:** the draft is overwritten on Deploy, as Save would, and
  drafts keep no history. A draft saved before `savedBy` existed, a system-installed draft, and
  every save on a server without authentication show "not recorded" for who saved it.
  `principalName` reads the user store once per distinct person per listing. A draft in the
  protected system project cannot be saved by anyone
  ([ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md)), so Deploy from it is
  now refused at the save — which is that record's intent, where before the editor could
  deploy a different diagram over a platform process.
- **Follow-ups / risks to watch:** a deploy by an agent (`atlas_deploy`) still does not save
  the draft — the agent is told to check with `atlas_compare_draft`. The Modeler home and the
  application's artifact list do not show the comparison yet; the chip is the one place it is
  said.

## Links

- relates to [ADR-0021](0021-diagram-drafts.md), [ADR-0128](0128-process-applications.md),
  [ADR-0205](0205-connector-ownership-and-event-delivery.md),
  [ADR-0251](0251-adjust-a-deployed-diagram.md)
