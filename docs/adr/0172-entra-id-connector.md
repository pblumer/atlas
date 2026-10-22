# ADR-0172: A Microsoft Entra ID connector

- **Status:** Accepted (amended 2026-08-24: a listing operation, `list-users`, which follows Graph's paging itself; and advanced query support, so a listing can use `endsWith`, `ne`, `not` and `$search`. Amended 2026-08-25: group lifecycle (`create-group`, `get-group`, `list-groups`, `update-group`, `delete-group`) and ownership (`add-group-owner`, `remove-group-owner`) beside the existing membership operations; a dedicated `reset-password` that wraps a literal-or-FEEL secret in a `passwordProfile`; Teams (`create-team` on a group, `add-team-member`, `add-team-owner`, `create-channel`, `archive-team`), a Team being addressed by its group's id; and `assign-license` / `assign-role`. Amended 2026-08-25: a supervised Entra worker can take its client secret from the engine vault — `ATLAS_ENTRA_<NAME>_CLIENT_SECRET_REF` names a vault key the server resolves and hands the child under `_CLIENT_SECRET`, the AD bind-secret story with a connector name in place of a reference, so an operator can type the secret into the Console instead of onto the worker host; the worker stays worker-only and the engine builds no client. Amended 2026-08-25: the attributes body can be authored inline as JSON in the modeler instead of naming a variable — a string value beginning with `=` is a FEEL expression evaluated per field at runtime, and the compiler turns the whole template into one FEEL context compiled once at deploy (I5); the two ways of supplying the body are mutually exclusive. Amended 2026-08-25: an Entra tenant is now an operator-managed connector in the Console — a worker-only managed kind whose `credentialsRef` names a vault OAuth bundle `{tenantId, clientId, clientSecret}` (as SharePoint's does), read only by `superviseEnv` so the engine still builds no client and holds no tenant credential; and the Entra worker is supervised by default, starting parked with nothing to serve and brought up by the same refresh that already restarts mail/AD workers the moment a tenant is added — so adding a tenant is a Console entry, not a deployment change. Amended 2026-08-25: the `connector` name may itself be a literal-or-FEEL value — a leading `=` resolves the tenant from the instance's variables at call time, so one process can serve several tenants; this is entra-only because the kind is worker-only and no engine-side credential lookup keys off a fixed name. Amended 2026-08-25: the body-carrying operations get a per-operation capture mask in the Modeler (named fields for the important attributes plus a Weitere-Attribute JSON escape hatch) assembled into the same `attributes` JSON — UI-only, no moddle/compiler change; and the result variable gets an operation-aware hint describing what it receives. Amended 2026-10-17: the app may prove itself with a certificate instead of a client secret — a client assertion signed with the certificate's private key, still the client-credentials grant — configured as `_CLIENT_CERTIFICATE` (or `_CLIENT_CERTIFICATE_REF`) or as a bundle's `certificate` field; and `get-user` / `get-group` take a `select`, because Graph's default property set leaves out `assignedLicenses` and `employeeId`. Amended 2026-10-19: on Windows the certificate may stay in the certificate store, named by its thumbprint — `_CLIENT_CERTIFICATE_THUMBPRINT`, or a bundle's `certificateThumbprint` — and its key signs there through CNG without ever being read, so a non-exportable key serves as it serves PowerShell. Amended 2026-10-22: `get-user-manager` reads a person's manager — through the user, as `$expand=manager`, so a person without one reads as null while an id that is nobody's still fails the job, or ends it with the BPMN error `ENTRA_USER_NOT_FOUND` when the process catches that code.)
- **Implementation:** Landed
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

Only the client-credentials grant is offered (with a client secret or, since the
2026-10-17 amendment, a certificate). Mail and SharePoint support a
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

### Amendment (2026-08-25): the connector name may itself be a FEEL expression

Every addressing field on the task — `userId`, `groupId`, `newPassword`, the inline
attributes — is already a literal-or-FEEL value evaluated per instance. The one field
that was still deploy-fixed was the `connector` name itself, and a real process wanted
it dynamic: a single Joiner/Mover/Leaver model that serves more than one tenant,
choosing the tenant from a start variable rather than being copied once per tenant.

So `connector` now takes the same fx treatment. A plain name (`connector="contoso"`)
is unchanged; a leading `=` (`connector="=tenant"`) is a FEEL expression the engine
evaluates against the instance's variables at call time, the resolved string naming
the tenant the worker holds. An expression that evaluates to an empty name is refused
at resolve, so the incident points at the task rather than at a worker later failing
to find a connector called `""`.

This is deliberately **entra-only**, and the reason is the kind's worker-only shape.
For a managed kind whose endpoint and secret live in the Console store (mail, REST,
SharePoint…), the *engine* resolves the named connector's client at deploy/activation;
a name known only at runtime would have nothing to resolve against and the deploy-time
preflight warning ("this connector is not configured") could not fire. Entra holds no
client in the engine at all — the name only ever travels to the worker, which looks it
up in its own `ATLAS_ENTRA_<NAME>_*` environment (itself provisioned from *every*
store tenant, not from what any one model names). So a runtime name costs nothing that
was being relied on: routing is by job type, not by name; no credential lookup keys off
it; and the supervised worker already serves every configured tenant. The compiler
keeps the authored `=…` text on the task's interned `Connector` for introspection (an
incident or a placement badge still has something to show), and the deploy-time
connector-preflight skips any reference whose name begins with `=`, because there is no
fixed name to check and the runtime unresolved-connector incident stands in for it.

### Amendment (2026-08-25): a per-operation capture mask, and a result-shape hint

The body-carrying operations were authored as one free-text JSON field. That is
maximally flexible and minimally guided: nothing says *which* attributes a create-user
takes, and the raw JSON invites two silent mistakes Graph will not forgive — a
mis-cased key (`DisplayName` for `displayName`, dropped without error) and a stray
trailing comma. So the Modeler now shows a **per-operation capture mask** for those
operations: named fields for the important attributes (create-user: displayName,
mailNickname, userPrincipalName, the passwordProfile, accountEnabled, usageLocation;
create-group: displayName, mailNickname, mailEnabled, securityEnabled, a Unified
toggle; and so on), plus a **"Weitere Attribute (JSON)"** escape hatch for anything the
mask does not name.

Deliberately UI-only (the assemble-in-the-editor option, not first-class attributes):
the mask reads and writes the *same single* `attributes` JSON string the compiler
already consumes — no new moddle property, no compiler change. Every field is text, so
a FEEL value (`=profil.accountEnabled`) works in any field exactly as inline JSON
allowed; the mask's build/parse pair does the coercion, so a boolean field emits a real
JSON boolean rather than the string `"true"`. Anything the mask cannot parse (a
hand-authored non-object body) leaves the fields empty and rides along verbatim in the
escape hatch, so switching an existing raw-JSON task to the mask never loses it.
Operations whose body is an array (assign-license's `addLicenses`/`removeLicenses`) keep
the raw JSON editor. The mask lives in its own module (`entra-attrmask.js`) with pure,
unit-tested assemble/parse functions and a Playwright harness driving the real widget.

The same amendment closes a smaller gap: the Modeler never told an author what the
**result variable** receives, and its shape differs per operation. The field's hint is
now operation-aware — create-user returns the user object you then address as
`=konto.id`, list-users returns an array, a body-less operation returns nothing — so a
downstream `=konto.<field>` can be authored with confidence. (The hint renderer learned
to take a function of the current values for this; a static string still works.)

### Amendment (2026-08-26): delta queries — reading only what changed

This closes the third capability the [MIM comparison](../comparisons/mim.md) named for
this system, and the one the original ADR left for later: **change detection instead of
a full compare.** Two operations join the table — `delta-users` (`GET /users/delta`) and
`delta-groups` (`GET /groups/delta`) — and are the listing's change-tracking twin. A
delta is a full enumeration *the first time* and only what changed on every run after,
which is what makes an hourly identity sync affordable where re-listing the directory
each time is not.

The design question a delta poses that a listing does not is **where the cursor lives.**
Graph hands back an `@odata.deltaLink` on the last page; a next run passes it back to
read only the changes since. This connector holds no state between jobs — that is the
worker model — so the cursor round-trips through the *process*, exactly as
[`galsync.bpmn`](../../examples/galsync.bpmn) already round-trips a DirSync cookie in a
BPMN variable:

- the operation takes an optional `deltaLink` (literal-or-FEEL, almost always a variable),
  empty on the first run and the previous run's cursor thereafter;
- its result is an **object**, `{ value, deltaLink }`, not the bare array a listing
  returns — because the cursor is half of what the operation is for. A process persists
  `deltaLink` (a data object) and hands it back next run.

Two consequences follow from being faithful to Graph rather than tidying its answer:

- **Deletions are in `value`, marked `@removed`.** Graph reports a removed object as an
  item carrying an `@removed` annotation, interleaved with the changes. The connector
  passes the page through as sent rather than filtering them out — dropping them would
  hide exactly the change a leaver flow is watching for. A process tells a change from a
  removal by the annotation, as Graph intends.
- **A truncated change set is refused, not returned.** The `maxUsers` cap fails the job
  rather than truncating, for the listing's reason and one more: a truncated delta would
  persist a `deltaLink` having skipped changes, so the next run would never see them. The
  cursor is only sound if the whole change set reached the process.

Delta's query surface is narrower than a listing's, and the compiler enforces it: `$select`,
`$top` and the `maxUsers` cap apply (both operations return a collection this connector
pages), but `$filter`, `$search` and advanced query do not — Graph's delta endpoint runs
none of them, and refusing them at deploy is better than the runtime `400`. `$select` is
threaded forward by Graph into the `deltaLink` itself, so a resume sends nothing but the
cursor. As with every other operation the rules live in both tables (the compiler's and
the connector's) and the drift test holds them together.

A field carried on the resolved job for this — `deltaLink` — has to survive the
engine→worker hop, and while wiring it the same silent-drop that the advanced-query
amendment found was found again for `newPassword`: neither was listed in the payload the
engine hands the worker, so a reset-password resolved an empty secret and a delta could
not resume. Both are now carried and both are covered by a worker round-trip test, the
kind that catches a dropped field through what it does rather than through a struct that
decoded "successfully."

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

### Amendment (2026-10-17): a certificate instead of a client secret, and a select on a single read

**A certificate credential.** An app registration can prove itself to Entra with a
certificate instead of a client secret, and many organizations allow only that: a secret
is a shared value that travels to the token endpoint on every exchange, while with a
certificate only a short-lived assertion signed by its private key does. Until now the
worker read only `_CLIENT_SECRET`, so such an organization had to fall back to a
PowerShell script task holding the certificate in the worker host's store — where every
script on that host could use it.

- The worker reads exactly one credential per tenant: `_CLIENT_SECRET`, or
  `_CLIENT_CERTIFICATE` — the certificate and its unencrypted private key as one PEM text,
  the shape the LDAP Worker Type already takes for a client certificate. Both set is a
  startup error naming both variables, because the choice would otherwise be an accident
  of precedence. A PEM written on one line with literal `\n` is read as the PEM it spells,
  since a systemd unit or a one-line Kubernetes value cannot hold a line break.
- It is still the client-credentials grant, through the certificate credential
  [ADR-0462](0462-exchange-worker.md) added for the Exchange Worker:
  `oauth2.ClientCertificate` posts `client_assertion_type=…jwt-bearer` with an RS256 JWT
  whose header names the certificate by `x5t#S256` and `x5t` and whose claims are RFC
  7523's: `iss` = `sub` = the client id, `aud` the token endpoint, a fresh `jti`, ten
  minutes of validity. Entra and Exchange read the PEM with the same
  `oauth2.ParseCertificateBundle`, so the two Worker Types refuse the same bundles.
- The key must be RSA, because the assertion is RS256. An EC key, a key that does not
  belong to the certificate, or an encrypted key is refused at startup, naming the tenant
  and never quoting the PEM.
- A supervised worker gets it the way it gets a secret: a Console bundle carries
  `certificate` in place of `clientSecret` (`{tenantId, clientId, certificate}`; a bundle
  with both is left out like any bundle that does not resolve), and a host-set tenant may
  name a vault key in `_CLIENT_CERTIFICATE_REF`. The engine still builds no client and
  holds no tenant credential.
- PFX is not read. Go's standard library cannot decode it, and the one PKCS#12 decoder at
  hand (`x/crypto/pkcs12`) refuses the AES-encrypted PFX current Windows exports by
  default — a format that works only sometimes is worse than one conversion an operator
  does once (`openssl pkcs12 -nodes`, or PowerShell 7's `ExportCertificatePem` and
  `ExportPkcs8PrivateKeyPem`).

**A select on `get-user` and `get-group`.** Graph answers a single-object read with a
default property set that leaves out properties a process routinely needs to decide on —
`assignedLicenses` (has the account been licensed yet?) and `employeeId` among them. A
missing property cannot be told from an unset one, so the read has to name what it wants.
The `select` attribute, the compiled field and the payload already existed for listings;
the compiler now accepts it on `get-user` and `get-group` (an operation-table flag,
`Selects`, held in step with the compiler's by the drift test), and the worker appends
`?$select=` to the single-object path. Every other operation still refuses it.

### Amendment (2026-10-19): a certificate in the Windows certificate store, by thumbprint

**Why.** The 2026-10-17 amendment took the certificate as PEM, with its private key in the
clear. On the Windows servers where these processes run, the app's certificate usually
already sits in the store of the host, its key marked **non-exportable** — the property that
keeps it from being copied, and the one PowerShell relies on when it signs in with
`-CertificateThumbprint`. A key that cannot be exported cannot be turned into PEM, so the
Entra Worker could not use the very certificate the Teams Phone Worker
([ADR-0463](0463-teams-phone-worker.md)) uses on the same host by thumbprint. An operator
had to create and register a second certificate, with an exportable key, for the same app.

- The worker takes a third credential, still exactly one per tenant:
  `_CLIENT_CERTIFICATE_THUMBPRINT`, the SHA-1 thumbprint as Windows shows it (spaces,
  colons and the invisible marks a copy out of the certificate dialog brings are
  ignored). Two of the three set is a startup error naming each, as before.
- The store is searched as the Teams Phone Worker and `Connect-MicrosoftTeams` search it:
  `CurrentUser\My` of the account the worker runs as, then `LocalMachine\My`. So one
  thumbprint names one certificate for every Worker on the host.
- **The key is never read.** `internal/certstore` implements `crypto.Signer`: each digest
  goes to CNG (`NCryptSignHash`, PKCS #1 v1.5) and the signature comes back. The client
  assertion is the same RS256 JWT through the same `oauth2.ClientCertificate`, which now
  takes a `crypto.Signer` instead of an `*rsa.PrivateKey` — an RSA key read from PEM is
  one, so nothing else changes.
- **Silent and CNG only.** A service has nobody to show a PIN or consent prompt to, so a
  key that would ask fails instead of hanging. Every key a current Windows creates or
  imports is a CNG key, and CNG also opens the legacy CryptoAPI software keys; a key that
  only a smart-card CSP holds is refused with that reason.
- **Tried at startup.** Opening the certificate signs one digest and verifies it against
  the certificate, so a thumbprint the store does not hold, a key the service account has
  no read permission on (*Manage Private Keys*), or a key that is not the certificate's
  is a startup error — naming the store and the account searched — rather than a failed
  token request in the middle of a run.
- **Nothing is held between signatures.** The store is opened, the key acquired and both
  released per signature; the worker signs at most once an hour (the token is cached), so
  a renewed or removed certificate cannot leave a stale handle behind.
- **A Console bundle** may carry `certificateThumbprint` in place of `clientSecret` or
  `certificate` (`{tenantId, clientId, certificateThumbprint}`). On a host without a
  certificate store — anything but Windows — such a bundle is left out like any bundle
  that does not resolve, because handing it over would make the worker refuse to start
  for every tenant it serves, not only this one. An external worker set up by hand on
  such a host refuses the variable at startup and says why.
- The thumbprint is not a secret, but it stays in the bundle with the rest of the tenant,
  so a tenant is still one vault entry.

The same mechanism is open to the Exchange Worker and anything else that signs a client
assertion; it is built for Entra first because that is where it was needed.

### Amendment (2026-10-22): the manager, read through the user

**Why.** Routing an approval to a person's line manager is among the most common
questions an identity process asks the directory, and the worker could not answer it.
`manager` is a relationship, not a property, so no `select` on `get-user` reaches it, and a
model had to fall back to the REST Worker Type with a hand-built Graph URL and a Graph
credential of its own — the encoding and the second credential this worker exists to
avoid.

**What.** `get-user-manager` takes a `userId` and a required `resultVariable`, which
receives the manager's directory object, not the user's: a model addresses `=chef.mail`.
An optional `select` names which of the *manager's* properties come back. Every other
query field is refused at deploy, as on `get-user`.

**Through the user, not from `/users/{id}/manager`.** Graph has a path for the manager,
and it answers 404 both for a person who has no manager and for an id that is nobody's.
[ADR-0367](0367-directory-photo.md) accepted that ambiguity for the photo and reads 404 as
"no photo", trading a mistyped id that reads as "no photo" for an incident queue that
stays readable. For the manager the same trade would be a wrong answer a process acts on:
it would route the approval to its fallback approver for a person who does not exist. So
the worker reads `GET /users/{id}?$select=id&$expand=manager` instead. An id that is
nobody's is still a 404 and fails the job into an incident; a person without a manager is
a user object without the expanded property, and the result variable is null, which a
gateway asks with `= chef = null`. It costs no second call.

- The user itself is cut down to `$select=id`, since none of it reaches the process; an
  authored `select` goes into the expansion, `$expand=manager($select=…)`.
- An absent and a null `manager` both read as null. An answer that is not a user object,
  or a `manager` that is not an object, fails the job rather than reading as "no
  manager".
- Only the immediate manager. Graph walks the chain with `$levels`, but only as an
  advanced query (`ConsistencyLevel: eventual`) and as a nested structure; nothing has
  asked for it yet, and a plain expansion needs neither.
- A manager can be an organizational contact rather than a user. Its `@odata.type` says
  which, and the worker passes the object through unchanged.
- The permission is `User.Read.All`, which a tenant that already runs `get-user` grants
  ([`docs/entra-permissions.md`](../entra-permissions.md)).
- **An unknown user can be a path rather than an incident.** When the task's process
  catches `ENTRA_USER_NOT_FOUND` — an error boundary on the task, or an error event
  subprocess around it — Graph's 404 with `Request_ResourceNotFound` ends the job with
  that BPMN error, and the instance takes the path at once without spending retries on an
  answer that will not change. Uncaught, it stays a failure with its retries and then an
  incident, the rule [ADR-0461](0461-script-bpmn-errors.md) set for scripts and the Teams
  Phone Worker follows. The codes a process catches travel with the job (`errorCodes`),
  because the worker has no compiled process to ask. A 404 without Graph's code — a proxy,
  a wrong base URL — is not Graph saying the person does not exist, and stays a failure.
  The error's message names no user: it is kept with the error, and the id is often a
  value the process declares personal. The approval model in `examples/m365-dienste`
  uses it: a mistyped address in an order reaches the fallback group with that reason, as
  it did when a PowerShell script read the manager there.
- Written from Graph's documentation and tested against a recorded Graph; it has not yet
  run against a live tenant.

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
- the least-privilege Graph permissions each operation needs are tabulated in
  [`docs/entra-permissions.md`](../entra-permissions.md)
