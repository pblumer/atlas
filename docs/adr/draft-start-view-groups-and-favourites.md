# ADR-DRAFT: The Start view groups by application and keeps favourites per person

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-20
- **Deciders:** Atlas maintainers
- **Open question:** whether the application is the grouping the people who start processes
  think in. Applications divide work by who builds it; a starter may look for HR, IT or travel,
  which a category on the process or its start form would say better. Nothing yet shows which.
- **Question checked:** 2026-10

## Context and problem statement

The Tasks app's Start view ([ADR-0028](0028-forms-and-the-tasks-app.md)) listed every executable
process with a start form as one alphabetical column. An installation with a few dozen of them
reported that the list had become hard to find one's way down, and asked for groups, a filter
and favourites.

Two things are not settled by the existing records. Which name does a process sit under, and
who may read that name — applications can be private ([ADR-0071](0071-sharing-scopes.md)), and
the people who start a process are often not members of the application it was deployed under.
And where does a person's star live — the browser, or the server, and if the server, in which
of the stores that already keep somebody's marks.

## Decision drivers

- A starter must find a process quickly, whatever device they are on.
- A private application stays as hidden as it is today: its name is not revealed through a
  side door.
- The process listing is read by every view; it must not fail over a label.
- No new mechanism where an existing one fits, and no existing one stretched over a case it
  refuses.

## Considered options

For the grouping:

1. Group by application, resolving names in the browser from `GET /api/v1/applications`.
2. Group by application, with the process listing naming each row's application where the caller
   may see it.
3. A new category on the process or its start form.

For the favourites:

1. Keep them in the browser.
2. Reuse the diagram shortcuts' `process` kind ([ADR-0460](0460-diagram-shortcuts.md)).
3. A per-person store of their own, keyed on the principal like the display preferences
   ([ADR-0459](0459-display-preferences-per-user.md)).

## Decision outcome

Chosen: **grouping option 2 and favourites option 3.**

`GET /api/v1/processes` carries `applicationName` beside `projectId`, filled only where the
caller's role on that application is at least viewer. The Start view puts each process under its
application's name, sorted by name; a process under no application, or under one the caller may
not see, goes under *Other* at the end, so nobody is shown a name they were not shown before. An
application store that cannot be read costs the names, never the listing. A filter matches a
process's name, id and application name.

Option 1 was rejected on cost: the applications listing reads every draft to count artifacts, on
the run loop, and the Start view is opened far more often than the Modeler home that pays for it
today. Option 3 is not ruled out — it is the open question above — but it needs data no model
carries yet, and the application is data every deployment already has.

The favourites are a service of their own, `api/startfavourites`, on `/api/v1/me/start-favourites`:
one record per user id holding process ids in the order they were marked, bounded by the
`StartFavourites` budget. A mark names a process id, not a deployment key, so a new version keeps
its star; only a deployed process can be marked, and any mark can be cleared. The routes are open
to every signed-in principal (`roleAny`), because roles do not nest and the people who start
processes hold `operator`, which need not come with `user`. With authentication off there is no
person to keep them for: the answer is 404, and the console keeps them in the browser.

The diagram shortcuts were rejected because a mark there is checked against the diagram's
design-time scope, which refuses exactly the starter who is not a member of the application, and
because a modeler's favourite diagrams and somebody's favourite forms to fill in are different
lists. Browser storage alone was rejected because a star that does not follow a person to their
second device is one they set again, or do not.

### Consequences

- **Positive:** the list stays usable as it grows; a starter's favourites follow them; no
  application is revealed to somebody who could not see it.
- **Negative / trade-offs accepted:** a starter who is a member of no application sees most of the
  list under *Other*, which helps them only through the filter and the favourites. One more
  per-person store to back up (`start-favourites`, instance class).
- **Follow-ups / risks to watch:** whether starters ask for a grouping the application does not
  give them — that is the open question, and its answer is a category, not more application
  membership.

## Links

- relates to [ADR-0028](0028-forms-and-the-tasks-app.md), [ADR-0071](0071-sharing-scopes.md),
  [ADR-0128](0128-process-applications.md), [ADR-0348](0348-favourites.md),
  [ADR-0459](0459-display-preferences-per-user.md), [ADR-0460](0460-diagram-shortcuts.md)
