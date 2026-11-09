# ADR-DRAFT: An application's deployed processes are listed in the application, not on the Modeler home

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-09
- **Deciders:** pblumer (maintainer; asked on 2026-11-06 why the Modeler home's application table did not fold and why the same deployed processes were listed in three places, and on 2026-11-09 for the concept that followed to be built)

## Context and problem statement

The deployed processes of an application were listed in three places, each with its
own columns and actions:

1. **Modeler › Home › Deployed** — every deployed process, grouped under its
   application (ADR-0034), with Deactivate, Open and Delete.
2. **Application › Deployments › Definitions** — one row per deployed *version*, with
   its state, running and finished instances, and no actions (ADR-0128).
3. **Operations › Instances** — one row per process, with running instances,
   incidents and finished instances.

Deactivate (ADR-0119) and Delete were offered only in the first, so they could not
move without being lost. The home page also showed "Not assigned" twice, once for
artifacts and once for deployments, counting different things under one name. And its
application table, the first thing on the page, did not fold: fold.js (ADR-0505) folds
a card headed by an `<h2>`, and the table sat under the page's `<h1>`.

ADR-0128 had already said where the list belongs: "the global per-project deployed list
from ADR-0034 moves *into* the application". The move was never finished, so the
Modeler home kept the copy.

## Decision drivers

- One question, one place: *what has this application deployed, and what do I do with
  it?* is answered in the application.
- No action may be lost on the way: pausing and deleting a deployed process have to
  be reachable before the list that offered them goes.
- The Modeler (design time) and Operations (run time) answer different questions;
  each links to the other instead of repeating it.
- Every list folds the same way (ADR-0505).

## Considered options

1. Keep the Deployed list on the Modeler home, and fold the application table.
2. Remove the Deployed list; the application's Deployments tab lists the processes and
   carries their actions; Operations keeps its own run-time view and gains the pause
   switch where an operator watches a process.
3. Remove the Deployments tab's list and keep the home's.

## Decision outcome

Chosen option: **2**, because it finishes what ADR-0128 decided, and it is the only
option in which every deployed process is listed once in the Modeler with every action
that can be taken on it.

- **Modeler home** lists applications only, in a table that folds under its heading
  (`<h1>` Modeler, `<h2>` Applications). A column says how many processes each
  application has deployed and how many of them are paused; it links to the
  application's Deployments tab (`#/modeler/p/{id}/deployments`).
- **Application › Deployments** lists one row per *process*: its newest version, its
  state, the instances all its versions hold, and when it was deployed. The older
  versions fold under the version (`details.fold-sub`), each linking to its
  instances. A row's menu opens the process in the Modeler, opens its instances
  (operators), pauses or resumes its automatic starts (operators, ADR-0119) and deletes
  it with all its versions (who may write to the application). The mark for a
  definition frozen on a decision version (ADR-0423) moved with it, which is why
  `GET /api/v1/applications/{id}/deployments` now carries `frozenDecisions`.
- **Not assigned** is one pseudo-application with an Artifacts and a Deployments tab,
  so the home page names it once. Its deployments are built in the browser from the
  process listing; their instance counts come from the operations summary, which only
  an operator may read, and read "—" for anybody else.
- **Operations › Live view** shows a paused definition as paused and gives the
  operator the pause switch on the newest version.

### Consequences

- **Positive:** a deployed process is listed once in the Modeler, with all its
  actions; the home page is one list; every list folds.
- **Negative / trade-offs accepted:** pausing or deleting a process from the home
  page now takes one more click, into the application. That is a destructive or
  operational action, and the extra step shows its state and instances first. The
  home column counts processes; it does not show the published release version,
  because that is read from the release store, one file per release, on every load
  of the page.
- **Follow-ups / risks to watch:** the grouping of Operations › Instances by
  application (pull request pblumer/atlas#1609) can link each group to
  `#/modeler/p/{id}/deployments`.

## Pros and cons of the options

### Option 1 — keep the home's list
- Good: no change to where actions are.
- Bad: the duplication stays, and the home page stays two lists long.

### Option 2 — the application holds its deployments
- Good: one place per question; ADR-0128's intent built.
- Bad: one more click for pause and delete from the home page.

### Option 3 — the home holds them
- Good: one global view.
- Bad: contradicts ADR-0128; an application page that does not say what it has
  deployed.

## Links

- completes ADR-0128 (process applications: the deployed list moves into the application)
- relates to ADR-0034, ADR-0119, ADR-0423, ADR-0505
