# ADR-DRAFT: Favourite and recent diagrams are one record per account, resolved at read time

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether forms, capabilities and value streams should be markable
  too. They are not drawn on a canvas, so the request — favourites "auf Ebene
  Diagramme" — did not cover them, and nobody has asked for them yet. Adding a kind is a
  resolver case and a star in one list; the record's shape does not change.
- **Question checked:** 2026-10

## Context and problem statement

The request: favourites, per person, usable across every area of the Console from the
Modeler to Data, on the level of diagrams, *including* the diagrams opened last.

Two kinds of favourite already existed and neither fits. The shop's (ADR-0348) are
product ids under a principal, on the server; the FEEL assistant's are expressions in
the browser's local storage. The first is the right shape for the wrong object; the
second does not follow a person to another machine, which for a shortcut list is half
of what it is for.

"Diagram" spans four stores with three different access rules: BPMN drafts, decision
references and decision drafts are governed by their project and owner
(`canViewArtifact`, ADR-0071); deployed processes by their project; information models
and Panorama models by their application's scope alone (ADR-0189, ADR-0230).

## Decision drivers

- A mark must not become a way around a scope: losing access to an application must
  take its diagrams out of the list, by name as well as by route.
- A deploy, a rename of a draft's application or a withdrawn share must not leave
  somebody a list they have to repair.
- No invariant is touched: this is design-time state, outside the engine and the log.

## Considered options

1. ADR-0348's shape, generalised: a kind and an id under a principal, resolved on read.
2. A stored copy of each diagram's name and route, refreshed when the diagram changes.
3. The browser's local storage, as the FEEL assistant does.

## Decision outcome

Chosen option: **1**, because it is the only one in which access is decided in exactly
one place — the diagram's own scope, asked at read time.

- **One record per account** (`diagram-shortcuts` store, keyed by user id) holds both
  lists: favourites in the order they were marked, and the fifteen most recent visits,
  newest first, one entry per diagram. Both are written on the run loop, so one file
  never interleaves two writes.
- **Four kinds:** `process` (by process id: the draft when the caller may see one, the
  newest deployed version otherwise), `decision` (reference id, else decision-draft id),
  `infomodel`, `panorama`. A process is marked by its id, never by a deployment key, so
  deploying v2 does not leave the star on v1.
- **A new mark and a visit are checked; a clear never is.** Marking or recording a
  diagram the caller cannot open answers 404, as the diagram's own route would, so a
  hidden diagram cannot enter the list by its id. Clearing is unconditional, so a mark
  that stopped resolving can always be removed.
- **On read, an unresolvable favourite comes back as its kind and id with
  `available=false` and no name.** The id is the caller's own; the name is the diagram's,
  and the list must not keep showing it after the share is gone. An unresolvable visit is
  left out: history nobody can open is noise.
- **Always the caller's own**, with no `?principal=`, for ADR-0348's reason.
- **Bounded:** `DiagramFavourites` (default 100) caps marks; visits are trimmed on write.
- **The Console** shows a star beside a diagram's name in the lists that hold diagrams,
  a top-bar menu with both lists, and records a visit when the router opens a canvas.
  With nobody signed in — including with authentication off — neither is drawn.

### Consequences

- **Positive:** access is never cached; a withdrawn share takes effect on the next read.
  The record restores and travels with a whole-instance snapshot as one thing.
- **Negative / trade-offs accepted:** every read resolves every entry — at most a
  hundred and fifteen store reads on the run loop, for a menu somebody opens by hand.
  Opening a diagram costs a write.
- **Follow-ups / risks to watch:** the open question above; and, as in ADR-0348, marks
  that never resolve again are never cleared automatically.

## Pros and cons of the options

### Option 2 — store the name and route
- Good: a read is one file.
- Bad: the stored name outlives the share, which is exactly the leak the first driver
  rules out; and every rename, move and deploy would have to update every account's copy.

### Option 3 — browser storage
- Good: no server change.
- Bad: not per person but per browser, and a shared machine shares the list.

## Links

- extends ADR-0348 (the shop's favourites) to the Console's diagrams
- relies on ADR-0071 (artifact scope), ADR-0189 and ADR-0230 (application scope)
- ADR-0282 (the store registry classifies `diagram-shortcuts` as instance state)
