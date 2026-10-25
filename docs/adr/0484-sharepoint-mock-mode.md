# ADR-0484: A mockup provider for the SharePoint Worker Type

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** whether Graph's own filter evaluation agrees with the mockup's subset on the edge cases this record names (text comparison without regard to case, a column with no value compared with `ne`, a comparison across types), which no test here can settle without a tenant
- **Question checked:** 2026-10

## Context and problem statement

The SharePoint Worker Type ([ADR-0141](0141-sharepoint-connector.md), amended 2026-10-22)
performs eleven operations on a site's lists and libraries. Every one of them needs a
Microsoft 365 tenant, an app registration with admin consent, and a site somebody has
furnished with lists, libraries and columns. The shipped example
(`examples/sharepoint-fallakte/`) cannot be run by a reader who does not have all three,
which is most readers, and a process author cannot try a model without a change request
to whoever owns the tenant.

Atlas has answered this question three times already, each for one Worker Type: the
preview mail provider ([ADR-0150](0150-preview-mail-provider-and-visible-incidents.md)),
the AD mockup ([ADR-0181](0181-ad-connector-mock-mode.md)) and the SQL mockup
([ADR-0221](0221-sql-mock-mode.md)). [ADR-0216](0216-mockups-are-one-view.md) took the
lessons of those three and decided how the next one should look — a provider on the
Worker record, a mock that refuses what the real system refuses, a filter subset that
refuses the rest, a journal, and one Console view for every mockup — and named Jira as
the next. This record applies it to SharePoint.

The question is therefore narrower than "should there be a mockup": where does the
simulated site live, how faithful does it have to be, and how does a process author see
what a run did?

## Decision drivers

- **Going live is a change of Worker, not of model.** A task names a Worker and nothing
  else about the tenant (ADR-0036/0041). A mockup switched on in the model would be a
  model that behaves differently in test and in production.
- **Faithful where a model depends on it.** A mockup that accepts what SharePoint refuses
  teaches a model to be wrong (ADR-0181). The answers must have Graph's shapes —
  `fields`, `webUrl`, `items`/`hasMore`/`nextPageToken`, `listItem.fields`,
  `deleted`/`exists`/`created` — because a model reads them by those names.
- **The site must survive the worker.** SharePoint is offloaded to a supervised worker by
  default ([ADR-0233](0233-in-process-connectors-refused.md)), and the server restarts that
  worker whenever a Worker record changes. A site held in the worker would be empty on
  the second run of a process that depends on the first.
- **No new trust.** A mockup must never be a way to reach a tenant, and must not be a
  reason to hand a credential to anything.
- **One place to look** (ADR-0216).

## Considered options

1. **The site in the server's memory; a worker forwards each operation to it.**
2. **The site in the worker's memory, reported to the server** — the AD mockup's shape.
3. **A separate mock Graph server** — `atlas mock-remedy`'s shape: the real Graph client,
   pointed at an endpoint that answers like Graph.
4. **An org-wide switch** instead of a provider — the AD mockup's other choice.

## Decision outcome

Chosen option: **"the site in the server's memory; a worker forwards each operation to
it" (option 1)**, as a provider on the Worker record (`provider: "mock"`), shown in
ADR-0216's one view.

**Where it lives.** `sharepoint.MockSites` holds one `MockSite` per mockup Worker in the
Atlas server, created with the client registry and never swapped with it, as the preview
outbox is. The engine's handler binds the Worker to its site directly. A supervised or
external worker is handed `ATLAS_SHAREPOINT_<NAME>_MOCK=1` and
`ATLAS_SHAREPOINT_MOCK_URL` and registers `sharepoint.RemoteMock`, which posts each
resolved operation to `POST /api/v1/sharepoint/mock-sites/{worker}` with its worker token
(worker scope); the server performs it and answers what the site answered, or `422` with
the site's own refusal, so the job fails with the message it would have failed with in
the engine. A Worker that is not a mockup has no site and is answered `404`: the route
never reaches a tenant. It is runtime state, not engine state — memory, no event, nothing
replayed (I4/I6) — and it holds its own lock, off the run loop.

**What it refuses.** Both clients start from one `prepare`, so the mockup refuses exactly
the requests the Graph client would never send. Beyond that it refuses an item or a file
that is not there, a non-numeric item id, a list item created in or deleted from a
document library, a file where a folder is asked for and the reverse, a `pageToken` no
page answered with, an upload past a 64 MiB content budget per Worker, and a filter
outside its subset. It maps a missing thing to the answers the Graph client gives for
SharePoint's 404: `deleted=false`, `exists=false`, a failed task everywhere else.

**Where it is more forgiving, and says so.** A list or library exists from the first
operation that writes to it — `create-item` makes a list, `upload-file` and
`create-folder` a library — because the mockup has no person to make them; reading one
nobody has written to is refused as SharePoint refuses a list it does not have. The
site's default library exists from the start. A list's columns are the ones written to
it. The filter language is a stated subset of OData — `fields/<column>` compared with
`eq`, `ne`, `gt`, `ge`, `lt`, `le` against a quoted text, a number, `true`, `false` or
`null`; `startswith(fields/<column>,'text')`; `and`, `or`, `not` and parentheses — and a
filter on a column the list has never had a value written to is refused, which is the
only way a misspelt column name shows itself before the process reads it.

**What a person sees.** `GET /api/v1/mocks` serves one ADR-0216 report per mockup — kind,
holder, target, summary and the site as data — collected at read time, since an
in-process mock posts nothing. Operations › Diagnostics › **Mockups** draws a SharePoint
card per Worker: one table per list or library and the journal of the newest 200
operations, refusals in red. `DELETE /api/v1/sharepoint/mock-sites/{worker}` empties a
site in place (the engine's registry holds that very object) for the next trial run.
Both are admin-gated, for the reason the mock directory is: invented data, shaped like
the real thing.

**Governance.** A mockup may be governed with the service identity
([ADR-0475](0475-identity-aware-worker-execution.md)); it is never delegated, because a
person's token released to a Worker that reaches no tenant would be a credential spent on
nothing. Both the identity route and the record update refuse the combination.

**The example.** `examples/sharepoint-fallakte/sharepoint-testdaten.bpmn` puts into an
empty site what a person would have — a request and the letter template — so both
example processes run on a mockup Worker named `intranet` with no other preparation.
`connector/sharepoint/mock_test.go` runs all three end to end against a `MockSite`.

Option 2 was rejected because the site would die with every worker restart, which is
exactly when an operator changes a Worker. Option 3 would reuse the Graph client
unchanged, but it is a second process to run and keep alive, it answers on HTTP rather
than in the shapes the client already decodes, and it would need the site-id lookup and
paging links faked as well — more surface to be wrong in, for a reader who wanted one
click. Option 4 fits AD only because an AD task names its own URL; a SharePoint task names
a Worker record, so the mockup belongs to that record (ADR-0216's reasoning for Jira).

### Consequences

- **Positive:** The SharePoint example and any SharePoint model run with no tenant.
  Switching to production is switching the Worker's provider and naming a credential.
  The mockup's refusals are the Graph client's preconditions plus SharePoint's own, so
  a model that passes on it fails on a real site for reasons of the real site — a missing
  list, a permission — rather than for reasons of the model.
- **Negative / trade-offs accepted:** The site is gone when Atlas restarts. The filter
  subset is smaller than OData, and its comparisons are this record's reading of
  SharePoint's, not SharePoint's (the open question above). A document library's files
  are not reachable as list items (`get-item` on a library is refused), which real
  SharePoint allows; the refusal says so rather than answering differently from Graph.
  Lists are created by writing to them, so a model that only reads a list needs a setup
  step on the mockup that it would not need on a furnished site — the example ships one.
- **Follow-ups / risks to watch:** ADR-0216 is now partial — the AD and SQL mockups still
  have their own pages and routes. Do not let the forgiving parts grow: a mockup that
  creates lists on read, or matches a filter it cannot parse, would pass models that a
  real site refuses.

## Pros and cons of the options

### Option 1 — server-held site, forwarded operations
- Good: survives worker restarts; one site for the engine and every worker.
- Good: no report to go stale, no transport for the view.
- Bad: one HTTP round trip per operation from a worker; a worker that cannot reach the
  server fails its jobs (which then retry, as a Graph outage would).

### Option 2 — worker-held site, reported
- Good: no request per operation.
- Bad: the site is lost on every restart, and two workers would hold two sites.

### Option 3 — a mock Graph server
- Good: the production client runs unchanged.
- Bad: a separate process; site-id lookup, `nextLink` and error envelopes to fake; no
  Console view without a report.

### Option 4 — an org-wide switch
- Good: one setting.
- Bad: it would mock every SharePoint Worker at once, including one an operator meant to
  keep live.

## Links

- applies [ADR-0216](0216-mockups-are-one-view.md) (a mockup as a provider, one view for all of them)
- extends [ADR-0141](0141-sharepoint-connector.md) (the SharePoint Worker Type)
- follows [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) (a provider that needs no credential, state in the server)
- builds on [ADR-0181](0181-ad-connector-mock-mode.md) (a mock is faithful where being faithful is the point)
- relates to [ADR-0233](0233-in-process-connectors-refused.md) (why the worker usually serves the jobs)
- relates to [ADR-0475](0475-identity-aware-worker-execution.md) (governed Workers)
