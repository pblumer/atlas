# ADR-0172: A Microsoft Entra ID connector

- **Status:** Proposed (amended 2026-08-24: a listing operation, `list-users`, which follows Graph's paging itself; and advanced query support, so a listing can use `endsWith`, `ne`, `not` and `$search`. Amended 2026-08-25: group lifecycle (`create-group`, `get-group`, `list-groups`, `update-group`, `delete-group`) and ownership (`add-group-owner`, `remove-group-owner`) beside the existing membership operations; a dedicated `reset-password` that wraps a literal-or-FEEL secret in a `passwordProfile`; Teams (`create-team` on a group, `add-team-member`, `add-team-owner`, `create-channel`, `archive-team`), a Team being addressed by its group's id; and `assign-license` / `assign-role`. Amended 2026-08-25: a supervised Entra worker can take its client secret from the engine vault — `ATLAS_ENTRA_<NAME>_CLIENT_SECRET_REF` names a vault key the server resolves and hands the child under `_CLIENT_SECRET`, the AD bind-secret story with a connector name in place of a reference, so an operator can type the secret into the Console instead of onto the worker host; the worker stays worker-only and the engine builds no client. Amended 2026-08-25: the attributes body can be authored inline as JSON in the modeler instead of naming a variable — a string value beginning with `=` is a FEEL expression evaluated per field at runtime, and the compiler turns the whole template into one FEEL context compiled once at deploy (I5); the two ways of supplying the body are mutually exclusive)
- **Date:** 2026-08-21
- **Deciders:** Atlas maintainers

## Context and problem statement

[`docs/comparisons/mim.md`](../comparisons/mim.md) names the Microsoft Graph
connector as the second-largest gap against Microsoft Identity Manager, and the row
carries more weight than one line suggests. MIM's own *Microsoft Azure Active
Directory* management agent is out of support, and Microsoft's answer is the Graph
connector — so for any organization whose directory has moved to the cloud, Graph is
not one MIM row among many, it is *the* directory.

Atlas already speaks Graph twice: the mail connector sends through it (ADR-0093) and
the SharePoint connector creates list items with it (ADR-0141). Neither exposes the
directory. Nothing in Atlas can create, disable, or delete a cloud account.

So: how should a BPMN process provision an identity in Entra ID?

## Decision drivers

- **Cover the joiner/mover/leaver lifecycle** an identity process actually performs.
- **The AD connector's argument applies again.** [ADR-0166](0166-active-directory-connector.md)
  rejected "just use the generic LDAP connector" because AD's primitives are
  encodings a modeler should not hand-author. Entra's are the same shape.
- **[ADR-0164](0164-no-in-process-service-tasks.md) decides where it runs.** New
  connector kinds are built worker-first; [ADR-0173](0173-generic-sql-connector.md)
  took that to its conclusion for the first time.
- **Don't write the OAuth2 token flow a third time.**
- **Testable without a tenant.**

## Considered options

1. **Use the generic REST connector.** Graph is HTTP and JSON, and ADR-0152 gave REST
   an OAuth2 client-credentials grant, so a process could author the URLs itself.
2. **Extend the SharePoint connector**, which already holds a Graph client and a
   tenant credential, with directory operations.
3. **A dedicated Entra connector** with lifecycle-named operations, worker-only.

## Decision outcome

Chosen option: **a dedicated Entra connector** (option 3).

Option 1 is the one worth answering carefully, because it is not obviously wrong:
everything this connector does *is* a REST call, and Atlas can already make an
authenticated one. What a REST task cannot do is say what the call means. In Graph,
disabling an account is `PATCH /users/{id}` with `{"accountEnabled": false}`, and
removing a group member is `DELETE /groups/{id}/members/{userId}/$ref` — a URL whose
`$ref` suffix is easy to get wrong and whose failure mode is a 404 that looks like a
missing user. Adding a member is worse: a `POST` to a `$ref` collection whose *body*
carries an absolute `@odata.id` URL that has to name the right cloud.

Those are encodings, not business decisions, and a modeler should pick "Disable
account". This is exactly the argument ADR-0166 made for AD over generic LDAP, and it
lands the same way here. The generic REST connector remains right for the Graph calls
this connector does not cover.

Option 2 is rejected because "the SharePoint connector" would stop being about
SharePoint. The two share a transport, not a purpose, and the shared part is already
factored out (see below).

### Worker-only, and why it matters more here

Like the SQL connectors (ADR-0173) and unlike everything built before them, this kind
has **no in-process handler**. Reserved job type `io.atlas.entra` at index 23; the
engine resolves the task (`entra.Resolve`) and a worker performs it (`entra.Run`)
with a tenant credential from its own environment — `ATLAS_ENTRA_CONNECTORS` names
them, each contributing `ATLAS_ENTRA_<NAME>_TENANT_ID`, `_CLIENT_ID` and
`_CLIENT_SECRET`, plus an optional `_BASE_URL` for a national cloud.

The general argument is [ADR-0168](0168-connector-work-on-a-worker.md)'s. The specific
one is that an app registration with `User.ReadWrite.All` and `Group.ReadWrite.All`
can create and disable accounts across an entire directory. That is plausibly the
most valuable secret an Atlas installation touches, and there is no reason for the
engine to be able to read it. `entra.Job` has nowhere to put one.

Only the client-credentials grant is offered. Mail and SharePoint support a
refresh-token grant because a person's mailbox is a legitimate thing to act as;
unattended directory provisioning is not, and offering a delegated grant here would
mostly be a way to build something that stops working when someone leaves.

### The operation set

`create-user`, `get-user`, `list-users`, `update-user`, `delete-user`, `enable`,
`disable`, `add-group-member`, `remove-group-member` — a full joiner/mover/leaver
lifecycle, and deliberately more than the AD connector covers today (AD has no read,
update, or delete; that is recorded as a gap in the MIM comparison).

The 2026-08-25 amendment extends the set past the account and its group membership to
the three objects an identity process manages — the account, the group, and the Team a
group backs — plus licence and role assignment. No new authored field is needed but
one (`newPassword`); every other operation reuses the existing `userId`, `groupId`,
attributes variable, or listing query, so the additions are rows in the operation
table, not new model surface:

- `reset-password` sets a new secret. It is not folded into `update-user` because the
  secret is not a directory attribute a model should hand-author inside a
  `passwordProfile`: like the AD/LDAP connectors' modify-password, it authors one
  `newPassword` value (literal-or-FEEL, almost always a variable), and the connector
  wraps it in a `passwordProfile` with `forceChangePasswordNextSignIn`. The encoding
  is the connector's, the way ADR-0172 argues an operation's URL is.
- The **group** gains the same shape the user already had: `create-group`, `get-group`,
  `list-groups`, `update-group`, `delete-group`, and ownership beside membership
  (`add-group-owner`, `remove-group-owner`). `list-groups` reuses the same paging and
  advanced-query machinery as `list-users` — the listing became collection-agnostic
  (its path is a property of the operation) rather than being copied. `create-group`
  and `update-group` author properties through the attributes variable (`displayName`,
  `mailNickname`, `mailEnabled`, `securityEnabled`, `groupTypes`).
- **Teams**: `create-team` teamifies an existing group (`PUT /groups/{id}/team`) and
  the rest address `/teams/{groupId}/...` — `add-team-member`, `add-team-owner` (the
  same member call with `roles: ["owner"]`), `create-channel` (authoring
  `{displayName, description}` through the attributes variable), and `archive-team`. A
  Team's id *is* its Microsoft 365 group's id, so all of them are authored through the
  same `groupId`, with no separate team identifier. Removing a team member is
  `remove-group-member` — a team member is a member of its group — so there is
  deliberately no `remove-team-member` that would force a model to hold a membership
  id. `create-team` sends settings spelled out by the connector rather than left to the
  tenant's defaults, so the same model yields the same team everywhere. `POST /teams`
  from scratch is deliberately not used: it is asynchronous (a `202` with a polling
  location), which this connector's single synchronous `Call` does not model; the
  teamify-a-group path is synchronous and is the documented way to create a
  group-backed Team.
- `assign-license` (`POST /users/{id}/assignLicense`) and `assign-role`
  (`POST /roleManagement/directory/roleAssignments`) both author their body through the
  attributes variable — `{addLicenses, removeLicenses}` and `{roleDefinitionId}`. For a
  role, the connector merges the authored user in as `principalId` and defaults
  `directoryScopeId` to the whole directory (`/`), so a model names the user once and
  can still narrow the scope when it needs to.

The rules live in a table — which operation needs a user, a group, an attributes
object — because they are needed in two places. The compiler validates a model at
deploy, and the worker re-checks a job it was handed, so an under-specified task
fails with a named missing field instead of a Graph 404. The compiler cannot import
the connector (the dependency runs the other way), so the table exists twice and
`TestEntraOpsMatchTheConnector` is a behavioural drift guard between them: for every
operation, a model supplying exactly what the connector requires must compile, and
one missing any required field must not.

A password reaches Graph as a process value, never as model text — at creation
through `create-user`'s attributes variable (Graph's `passwordProfile`), and
afterwards through the dedicated `reset-password` operation added on 2026-08-25, which
authors one `newPassword` value the same shape the AD connector uses.

### Amendment (2026-08-24): `list-users`, and who follows the pages

The operation set above could address a user and change one. It could not *find*
one. A joiner/mover/leaver process routinely starts from a question — who is in this
department, which accounts are still enabled, does this UPN already exist — and the
original set answered none of them: `get-user` needs the answer as its input.

`list-users` is `GET /users`, with the model authoring an OData `$filter`
(literal-or-FEEL, so a process can list the department it is actually about), a
`$select` projection, a `$top` page size, and a `maxUsers` cap.

**The connector follows `@odata.nextLink`, and a model never sees it.** This is the
whole design question, and it is ADR-0166's argument once more: a collection in Graph
is paged, and a process that had to loop over a continuation token would be carrying
Graph's paging protocol in its diagram — the same encoding this record refused to
make a modeler hand-author for a `$ref` URL. So the result variable receives *the
listing*, as one JSON array, never one page of it.

Three consequences follow, and each is a deliberate choice rather than an omission:

- **The cap fails; it does not truncate.** `maxUsers` defaults to 1000 and a listing
  that exceeds it fails the job, for the reason ADR-0154's entry cap does: a short
  result set is a wrong answer rather than a partial one, and a process that decides
  something from it decides it confidently. `0` is the authored way to say unbounded.
- **An unbounded listing still terminates.** A server offering a next page forever —
  broken, misconfigured, or a directory nobody expected to be this large — would
  otherwise hold a worker until the job's lease expired, which surfaces as a task
  that mysteriously retries rather than as a listing that was too big. A ceiling of
  1000 requests ends it with a sentence saying so.
- **A continuation may not leave the tenant's own endpoint.** A paged result is the
  one place where a *response* names the next URL, and this client carries a bearer
  that can read an entire directory. `GraphClient` therefore confines an absolute
  continuation to its own scheme and host and refuses anything else, so a redirected
  page cannot hand that token to whoever wrote the response.

### Amendment (2026-08-24): advanced queries, opted into rather than guessed

The listing above could not express a large part of what a directory is asked. Graph
gates `endsWith`, `ne`, `not`, `$search` and `$count` behind *advanced query support*,
and refuses them otherwise with `Request_UnsupportedQuery`. "Which mailboxes are on
this domain" is an `endsWith`, so this was not an exotic corner.

Advanced support is two things that only work together: the request header
`ConsistencyLevel: eventual` and `$count=true` in the query. Sending one without the
other is a 400, so `advancedQuery="true"` sends both and there is no way to author
half of it. A `search` attribute carries the term as Graph takes it — quotes included,
because `$search` has its own quoting and a compound term (`"a" AND "b"`) has quotes
inside it; inventing them here would make that case unwritable.

Three decisions carry the weight:

- **It is opted into, never inferred from the filter.** Reading the filter text for
  `endsWith` would work for a literal and not at all for a FEEL expression, which has
  no text at deploy. More importantly, eventual consistency changes what the process
  is *told* about the directory — a listing may be slightly stale. That is the
  author's decision, not a substring match's.
- **A `search` implies it.** Graph runs a `$search` only as an advanced query, so
  making an author tick a second box would be a trap whose only outcome is a 400.
  `advancedQuery="false"` next to a search is refused at deploy rather than honoured
  into a request that cannot work.
- **The header rides on every page.** Graph rejects a continuation fetched without it,
  which would turn a working listing into one that dies halfway through. The paging
  loop therefore replaces only the path and keeps the rest of the request.

`Client.Call` now takes a `Request` struct rather than a parameter list. The header
could have been derived inside `GraphClient` from the path carrying `$count=true` —
Graph does pair them, so it would work today. It is explicit instead because a
behavioural header inferred by sniffing a URL is the kind of coupling that breaks
quietly, and a fake client in a test could then only observe the string rather than
the intent.

What is still not covered is `$orderby` and the `$count` *value* as a result in its
own right; a process needing either uses the REST connector.

### The OAuth2 token flow, lifted

This would have been the third copy of the same hundred lines: an expiry-aware cache,
the form-grant exchange, and the client-credentials and refresh-token grants, written
for mail (ADR-0093) and copied for SharePoint (ADR-0141). `connector/oauth2` now holds
the mechanism, and the three connectors hold their own policy — which grants they
accept, what their credential bundle looks like, what their endpoint defaults are.
The mail connector's Google service-account grant stays with mail, implementing the
shared `Fetcher` interface, because it is Google-specific and nobody else wants it.

Both existing suites pass unchanged against the lifted code, which is the evidence the
move did not alter behaviour.

### Consequences

- **Positive:** the Graph/Entra row closes, and with it the practical successor to
  MIM's retired Azure AD agent. A cloud joiner/mover/leaver process is modelable
  without hand-authored URLs. The engine holds no credential that can disable
  accounts. And one token flow exists where there were two and nearly three.
- **Negative / trade-offs accepted:**
  - **A second worker-only kind, and the same cost.** `atlas serve --supervise` keeps
    it to one flag, but a modeler can author a task the default single-process install
    will not execute.
  - Connector configuration for this kind is not in the Console (ADR-0168 accepted
    this cost); the Workers view showing which names are served is what recovers it.
  - **The operation set is a subset of Graph and always will be.** Licences,
    administrative units, directory roles, and application role assignments are not
    covered, and neither is `$orderby`; a process needing one uses the REST
    connector. Growing the table is cheap, growing it without a use case is not.
  - **An advanced query trades consistency for reach.** `ConsistencyLevel: eventual`
    is what the name says: a listing may be slightly stale. That is why it is opt-in
    and why the Modeler names the trade rather than hiding it behind a checkbox
    labelled "advanced".
  - **A listing is materialized whole.** `list-users` holds every page in memory and
    writes one array into a process variable, which is why it is capped by default.
    A directory export belongs in a job that streams, not in a process variable —
    the cap is where that line is drawn, and an operator raising it to a very large
    number is choosing to cross it.
  - **The rules live in two tables.** The dependency direction forbids sharing them,
    so a drift test stands in for a compiler check. It is a real seam, and it is
    guarded rather than pretended away.
  - At-least-once delivery means a replayed `create-user` can attempt a duplicate.
    Graph rejects a duplicate `userPrincipalName`, so the failure is safe and visible
    rather than silent — but it is a failed job, not an idempotent retry.

## Pros and cons of the options

### Option 1 — the generic REST connector
- Good: exists today; covers all of Graph, not a subset.
- Bad: the model carries Graph's URL and body encodings, including a `$ref` body with
  an absolute `@odata.id`; nothing checks them until a token hits a 404.

### Option 2 — extend the SharePoint connector
- Good: reuses a Graph client and a tenant credential that already exist.
- Bad: a SharePoint connector that provisions accounts is no longer about SharePoint;
  the two share a transport, not a purpose.

### Option 3 — a dedicated Entra connector (chosen)
- Good: lifecycle-named operations; validated at deploy; the credential lives where
  it is used.
- Bad: a subset of Graph; a second place that knows the operation rules.

## Relationship to other records

- repeats [ADR-0166](0166-active-directory-connector.md)'s argument — a vendor's
  primitives deserve named operations — for the cloud directory
- follows [ADR-0164](0164-no-in-process-service-tasks.md) and
  [ADR-0173](0173-generic-sql-connector.md): built worker-first, with no in-process half
- uses [ADR-0168](0168-connector-work-on-a-worker.md)'s resolved-detail-on-the-job
  mechanism and its environment-held worker credentials
- factors out the token flow shared with [ADR-0093](0093-native-mail-providers.md)
  and [ADR-0141](0141-sharepoint-connector.md)
- bounds its calls with [ADR-0149](0149-bounded-connector-call-budget.md)'s budget
- honors [ADR-0041](0041-connector-management-and-secret-store.md)'s promise that a
  model never carries a secret — here by keeping the credential out of the engine
- rides the connector seam of [ADR-0007](0007-job-worker-protocol.md)/[ADR-0067](0067-service-task-connector-catalog.md)
- answers the second gap named in [`docs/comparisons/mim.md`](../comparisons/mim.md)
