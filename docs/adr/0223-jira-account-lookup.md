# ADR-0223: Jira account lookup

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0201](0201-jira-connector.md) gave the Jira connector seven operations and a rule for
what earns an eighth: *a step a business process takes*, not an endpoint Jira happens to
publish. `assign-issue` is one of the seven.

It has an argument a process cannot produce. Jira Cloud hands an issue to an **accountId**
— an opaque string like `5bbb13f8412ef82f0960566c` — and it has done so since Atlassian's
GDPR changes made an account unaddressable by name or address from outside. A process
knows the person as an address it read off a form, or a name a directory gave it. Nothing
in the connector turns one into the other.

So a model that wants to hand a new ticket to the person who asked for it has three ways
to get the id, and all three are bad:

- **Hard-code it.** The id is opaque, per-site, and per-person: a model with an accountId
  in it works for exactly one assignee, on exactly one Jira site.
- **Call `/rest/api/3/user/search` through the REST connector.** It works, and it costs a
  second copy of the credential on the server (`ATLAS_CONNECTOR_*_TOKEN` beside the Jira
  worker's vault bundle), the site URL repeated in the model, and the Cloud/Data Center
  parameter difference — `query` versus `username` — pushed onto the model author.
- **Give up on assigning from the process** and leave it to a person in Jira, which is the
  step the process was automating.

The question this record answers: does looking an account up earn a row in the operation
table, and if so, how does it address the two products?

## Decision drivers

- **The rule from ADR-0201.** An eighth row must be a step a process takes, not an
  endpoint someone might want.
- **A credential lives in one place.** The whole point of a managed Worker Type is that a
  model names an instance and the server holds its URL and secret (ADR-0036/0041). A
  workaround that needs a second copy of the credential is evidence of a missing row.
- **A model must not know which Jira product it is talking to.** The connector already
  decides Cloud versus Data Center from the credential's shape for authentication and for
  how an assignee is addressed; a lookup that made the author choose would be the first
  place that leaks.
- **An account that cannot be assigned is worse than no account.** Jira refuses an assign
  to a user without the project's *Assignable User* permission, and it refuses it one task
  *after* the one that chose them.

## Considered options

1. **No row.** Document the REST-connector recipe and leave it there.
2. **A row: `search-users`.** One operation that answers with the accounts themselves.
3. **A design-time picker.** A field in the Modeler that queries the configured Jira while
   the author is modelling, and stores the chosen accountId in the model.

## Decision outcome

Chosen option: **"a row: `search-users`"**.

- `<atlas:jiraConnector operation="search-users" query="…" [project="…"] maxResults="…"
  resultVariable="…"/>` returns the matching accounts as a JSON array, which a following
  `assign-issue` reads as `=konten[1].accountId` (FEEL lists are 1-based), and a
  `create-issue` can send as an `assignee` field of `={"accountId": konten[1].accountId}`.
- **The term's parameter follows from the credential, not from the model.** Cloud takes
  `query`, matched against a display name and an address; Data Center takes `username`.
  Cloud does not merely prefer `query` — it refuses `username`. This is the same
  `cloud()` split the connector already makes for authentication, for how an assignee is
  addressed, and (since Atlassian removed the offset-paged issue search) for which search
  endpoint a JQL goes to.
- **A `project` restricts the search to assignable accounts**, through Jira's
  `/user/assignable/search`. It is the one place `project` appears without `issueType`,
  and asking the right endpoint beats filtering afterwards: the accounts it removes are
  exactly the ones that would fail a later `assign-issue`.
- **The term is trimmed**, with the identifiers rather than with the content. Jira matches
  it as a *substring*, so a leading space is not a difference in formatting but a term
  nothing can match. A JQL stays untrimmed: its own whitespace is insignificant to Jira's
  parser, so there is nothing to weigh against leaving authored text alone.
- **The transport stays `/rest/api/2`.** ADF is a write-side concern and a lookup only
  reads, so nothing here needs v3 — the Cloud issue search remains the connector's one
  departure from the v2 base.
- **Paging is by `startAt`, and a short page ends the read.** This endpoint answers with a
  bare array: no envelope, so no `total` and no `nextPageToken`. The page's own length is
  the only thing that says another exists, and it is also what stops a server that ignores
  `startAt` from being read forever.

### Consequences

- **Positive:** a process turns what it knows about a person into the id Jira wants, with
  the credential still in one place and the model still free of a URL. `assign-issue`
  becomes usable from a model that was not written for one specific assignee.
- **Positive:** the account search is a *step in the process*, so it re-runs per instance.
  A picker's answer is frozen at modelling time; this one is right when the person changes
  teams, and it is auditable as a task like any other.
- **Negative / trade-offs accepted:** the worker's Atlassian account needs the global
  *Browse users and groups* permission. That is a permission an operator grants once, and
  it is the same account that already reads and writes issues.
- **Negative: an empty result is not proof that nobody matched.** Three different facts
  reach a process as the same empty array. The term genuinely matched no account; or the
  caller is one Jira does not recognise, or has not granted *Browse users and groups*,
  which it answers by seeing nobody rather than by refusing; or the address it searched by
  is hidden by that account's profile visibility, which is what Cloud matches
  `emailAddress` subject to. The middle one is not hypothetical: a Cloud site answered an
  unauthenticated user search with `200 []` while answering the same credential on
  `/myself` with a 401, so a wrong credential arrives as "nobody by that name". The
  connector cannot tell the three apart — Jira does not — so the Modeler's hint says so at
  the field, and names the display name as the term to fall back to.
- **Negative:** a search that matches several people returns several, and the model decides
  which. `[1]` is the honest default and the wrong answer for an ambiguous term; a model
  that cares should search inside a project, or use a term specific enough to be one
  person.
- **Follow-ups / risks to watch:** an `assignee` on `create-issue` as a first-class field
  (today it goes through the extra-fields map, which means the model writes the
  `{accountId: …}` object itself, and the Cloud/Data Center difference is the author's
  again). A design-time picker (option 3) would make that field worth having; the two
  belong in one change rather than separately.

## Pros and cons of the options

### Option 1: no row
- Good: the operation table stays exactly the loop ADR-0201 described.
- Bad: the loop is not closed. `assign-issue` is in the table and its argument is not
  obtainable, which reads as an operation that works and is in practice reachable only by
  hard-coding an opaque id.
- Bad: the documented workaround puts a second copy of a credential on the server, which
  is the thing the managed connector store exists to prevent.

### Option 2: a row (chosen)
- Good: one credential, one instance name, one place the Cloud/Data Center difference is
  decided.
- Good: the failure mode it prevents — assigning an account that cannot be assigned — is
  handled by asking a different endpoint rather than by a rule a model author has to know.
- Bad: it is the first operation in the table that is not about an issue, so "the loop a
  process runs against an issue tracker" now has an exception in it. Accepted because the
  exception is the argument of a row that was already there.

### Option 3: a design-time picker
- Good: the nicest authoring experience — pick a person, the id is stored.
- Bad: it needs the Console to call a live Jira while somebody is modelling, which is a new
  direction of traffic (design time → a production system) and a new endpoint whose
  permissions are the *modeller's* rather than the process's.
- Bad: it freezes an answer into the model. A process that assigns "the person who asked"
  cannot be modelled that way at all — that value is only known per instance.
- Not exclusive with option 2, and better on top of it: the picker would write what this
  operation returns.

## Links

- extends [ADR-0201](0201-jira-connector.md) (the Jira connector and its operation table)
- relates to [ADR-0067](0067-service-task-connector-catalog.md) (the generic REST connector, which is what
  a Jira endpoint without a row is still reached through)
- relates to [ADR-0168](0168-connector-work-on-a-worker.md) (the resolved job the
  operation's values travel in)
