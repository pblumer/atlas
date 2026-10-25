# ADR-0141: A SharePoint connector (create list item, via Microsoft Graph)

- **Status:** Accepted (amended 2026-10-22: ten operations beside create-item — `get-item`, `update-item`, `delete-item` and `list-items` on a list, and `upload-file`, `download-file`, `get-file`, `list-folder`, `create-folder` and `delete-file` in a document library — carried by a second reserved job type, `io.atlas.sharepoint`, so a worker built before them is never handed one; a site addressed by its URL or by host and path as well as by its Graph id; column values that keep the JSON shape of their FEEL value; a certificate in place of the client secret; and every answer read under the process variable's budget. Amended 2026-10-23: a certificate may also be named by its thumbprint in the Windows certificate store, `certificateThumbprint`, the Entra worker's third credential read the same way; the store is asked on the first token request rather than while the engine builds the client on its run loop, and tried at startup by the worker.)
- **Implementation:** Landed
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team
- **Open question:** whether Graph writes a JSON number or boolean into a *text* column as
  its text. Since the 2026-10-22 amendment a FEEL value that evaluates to a number is
  sent as a number, where before it was sent as its text; a model that computed a number
  into a text column relies on Graph converting it. Graph's documentation shows typed
  values only for typed columns and says nothing either way.
- **Question checked:** 2026-10

## Context and problem statement

Processes routinely need to record structured data in a system of record a business
already runs: "log this incident", "file this request", "append this row". For many
Microsoft 365 organizations that system is a **SharePoint list**. Atlas already runs
several service-task connector kinds through the job path — clio (ADR-0036), temis
(ADR-0050), HTTP-REST (ADR-0067), and outbound mail (ADR-0079) — behind one seam: a
`TypeConnectorTask` that creates a job carrying a reserved job type, picked up by an
in-process worker off the hot path (ADR-0007). There was no way to create a SharePoint
list item from a process.

Two shapes were already settled by the connectors before it and apply here unchanged:

1. **Where does the provider live?** A SharePoint provider is a Microsoft Graph base
   plus an OAuth credential (an app registration's client secret, or a refresh token).
   Per the secret rule (ADR-0041, I6) a credential must never be authored in a BPMN
   file, which is shared, versioned and rendered. Mail's native Graph provider solves
   exactly this (ADR-0093): the credential is one JSON auth bundle in the vault, named
   by the connector's `credentialsRef`.
2. **Where does the target live?** The site, list and item fields are per-task content
   an author writes in the model, exactly like a REST task's URL (ADR-0067), and they
   benefit from the same FEEL "fx" toggle so a field can be `=customer.name`.

So a SharePoint task is a hybrid of the two connectors that already exist, not a new
mechanism.

## Decision drivers

- **Extensibility is the point (ADR-0067).** Adding the kind should be one catalog
  entry + one moddle type + one compiler branch + one worker, and nothing else.
- **Honor the invariants.** The Graph call runs only in the worker, post-fsync, off
  the single writer, never in `applyToState` (I1/I2/I4, ADR-0005/0007). Model-authored
  fields are deploy-time data compiled into the process (I5); the credential is a
  *reference* resolved at build time (I6, ADR-0041).
- **Reuse the OAuth already built (ADR-0093).** SharePoint is Microsoft Graph, the same
  API the native mail provider authenticates against. The two grants a server workflow
  needs — client-credentials (app-only) and refresh-token — are exactly the mail
  provider's, minus Google's service-account JWT.
- **No heavy dependency (ADR-0010).** The grants are a form POST; the standard library
  covers them.

## Considered options

**For the first operation.** (a) **Create a list item** — the archetypal "record a
row" workflow step. (b) Upload a file to a document library. (c) Read a file / list
items. Create-item is the most common outbound workflow use and mirrors REST's
result-variable output shape cleanly.

**For the auth model.** (a) **App-only + refresh-token, both** (chosen), selected per
connector via the bundle's `method`. (b) App-only only. Both keeps parity with the mail
Graph provider and covers delegated/consumer tenants.

**For sharing the OAuth code with mail.** (A) Extract mail's `oauth.go` into a shared
package both import. (B) **A self-contained `sharepoint` package** with its own minimal
token machinery (the two grants it uses). The codebase deliberately duplicates
per-connector worker helpers rather than sharing them (the mail and REST workers each
carry their own `bindVars`/`toExprKind`, commented "mirrors the REST worker … so the
two enums evolve independently"); (B) follows that established convention and keeps the
change additive and isolated from the shipped mail connector.

## Decision outcome

Chosen: **a SharePoint connector kind whose provider is a managed connector and whose
target (site, list, fields) is model-authored, creating a list item through Microsoft
Graph, in a self-contained `sharepoint` package.**

- The **SharePoint connector task** is the catalog's next kind. A service task bearing
  an `<atlas:sharepointConnector connector="…" site="…" list="…" resultVariable="…">`
  extension (with `<atlas:itemField name="…" value="…"/>` children) compiles to a
  `TypeConnectorTask` carrying the reserved `SharePointJobType`
  (`SharePointJobTypeIndex == 12`). `connector` names a managed provider; `site`,
  `list` and each item field are literal-or-FEEL values (`RestExpr`, the fx toggle)
  evaluated over the instance's variables at call time.
- The **provider** is a managed connector record of kind `sharepoint`: an optional
  Graph base `endpoint` override (defaulting to the Graph v1.0 API) and a
  `credentialsRef` whose OAuth bundle is resolved from the vault (ADR-0041/0093). The
  bundle's `method` selects the grant (`clientCredentials` or `refreshToken`); the
  tenant builds the token URL and the scope defaults to `…/.default`.
- The **worker** (`sharepoint.Handler`, an `OutputHandler`) resolves the task's
  connector and target from the compiled process, evaluates the FEEL fields, POSTs
  `{fields:{…}}` to `/sites/{site}/lists/{list}/items` with a bearer token, and writes
  the created item's JSON into the task's result variable — the same output-mapping
  path the REST connector uses (ADR-0067). Delivery keeps the ADR-0007 job guarantees:
  at-least-once, recovery inherited from the job protocol.

### Consequences

- **Positive:** a SharePoint list item is authored end-to-end in the modeler and
  actually creates; OAuth-only Microsoft 365 tenants work via app-only or refresh-token
  with no model change and no new dependency; the Graph base and secret stay with ops,
  never in a model; adding the next connector kind stays a small, well-shaped change.
- **Negative / trade-offs accepted:** one operation to start (create list item; file
  upload and reads are follow-ups on the same framework); the item fields are the whole
  payload (no input mappings beyond the authored fields); Graph list-item creation is
  not idempotent, so an at-least-once retry after a crash between "Graph created it" and
  "job completed" can produce a duplicate item (unlike mail's deterministic
  Message-ID); the OAuth grant code is duplicated from the mail package rather than
  shared (per the codebase's per-connector-helper convention); `site` is the composite
  Graph site id, not the friendlier `hostname:/sites/path:` colon form.
- **Follow-ups / risks to watch:** unify the mail and SharePoint Graph-OAuth into a
  shared package; file upload to a document library and read operations behind the same
  `Client` seam; duplicate-item de-duplication (e.g. an idempotency column); the
  `hostname:/sites/path:` site-addressing form; surfacing a misconfigured-bundle skip
  as an incident or Console warning rather than a silent park; a per-call HTTP timeout.

### Amendment (2026-10-22): the steps a process takes in a site, not only the first

**Why.** The record shipped one operation and named the rest as follow-ups on the same
framework: reads, file upload, the `hostname:/sites/path` addressing form. Every one of
them is a step a case takes in the system of record the first one writes to — a request
is filed as a list item, and then it is read back, its status changes, it is found again
by that status, and the documents that belong to it go into a library beside it. A
Worker Type that can only create the item leaves every later step to a REST task, which
cannot hold the OAuth bundle (ADR-0041) and so cannot reach the site at all.

**The operation set.**

| Operation | What a process does with it | Answers with |
|---|---|---|
| `create-item` | File a row in a list | the created item |
| `get-item` | Read one back by its id | the item, its columns under `fields` |
| `update-item` | Change some of its columns | the columns after the change |
| `delete-item` | Take it away | `deleted`, false when it was not there |
| `list-items` | Find rows by an OData filter, one page at a time | `items`, `hasMore`, `nextPageToken` |
| `upload-file` | Put a document into a library, optionally with properties | the file, its properties under `listItem.fields` |
| `download-file` | Read a small document into a variable | `content`, `contentType`, `size` |
| `get-file` | Ask whether a document is there, with its properties | `exists`, and the file when it is |
| `list-folder` | See what a folder holds, one page at a time | `items`, `hasMore`, `nextPageToken` |
| `create-folder` | Make a case folder, and every missing one above it | the folder, `created` |
| `delete-file` | Take a document or a folder away | `deleted` |

The table is deliberately not "every Graph call". Permissions, content types, versions
and site administration are administration of the site, not steps in a case. Sharing
links are left out on purpose: creating one changes who may open a document — an access
decision, which anonymous links in particular turn into a tenant-policy question — while
the `webUrl` every file answer carries already opens it for anybody who may.

**A second job type, not a wider first one.** Create-item keeps
`io.atlas.sharepoint.createitem`; every other operation carries `io.atlas.sharepoint`
(`SharePointOpsJobTypeIndex`, 35). The strongest case for one job type is that it is
simpler: one subscription, one row in every table, and the operation in the payload says
everything a worker needs. It fails on one fact. A worker built before this amendment
reads every SharePoint job as "create an item from these fields" — it does not know the
payload has an operation — and workers and servers are deployed separately (ADR-0216); a
supervised worker is restarted by the server, not with it. Handed a `delete-item`, such a
worker would create an empty item and complete the job as if it had deleted one. The job
type is the one contract an older worker can be held to: under a type it does not
subscribe to, the job waits for a worker that knows it. Create-item stays where it was,
so an older worker keeps serving the models it always served.

**Addressing a site the way people have it.** `site` takes the site's URL as the browser
shows it, `host:/sites/name` (or `host/sites/name`), a bare host for the root site, or
Graph's id `host,siteId,webId`. Anything but an id is looked up once
(`GET /sites/{host}:/{path}`) and remembered per Worker, because Graph addresses
everything beneath a site by its id. The form `host,/sites/path` that the parser's
documentation once gave is read as a host and a path: it was never an id. A Graph id's
commas are sent literally, as Graph's examples write them, where the original code
escaped them.

**Typed column values.** A column value keeps the JSON shape of its FEEL value: a number
is sent as a number, a boolean as a boolean, a list as a list, a context as an object,
and a null as null — which clears the column. A literal stays its text. The original
code flattened every value to its text, which made a multi-choice, a lookup
(`<Name>LookupId`) and a Yes/No column unreachable. For a create-item authored before,
this changes what is sent when a FEEL value is not a string; see the open question.

**What travels through a variable is bounded.** Every answer is read under the process
variable's budget (`limits.Variable`) and refused, with what to narrow, when it is
larger — the original read was unbounded. `upload-file` and `download-file` carry
content as text or base64 under the same budget and refuse rather than truncate, for
ADR-0442's reason. Graph's file answers carry a pre-authenticated download URL valid for
about an hour; it is removed before the answer reaches a variable, because it is a bearer
capability nobody asked for and a variable lives in the event log.

**At-least-once, made harmless where it can be.** An upload replaces the file at its path
(a library with versioning keeps the previous one); `create-folder` treats a level that
is already there as made; `delete-item` and `delete-file` answer `deleted=false` for
something that is not there, because a retry after a delete that succeeded finds exactly
that. Create-item remains the one operation a retry can duplicate.

**A certificate in place of the secret.** A `clientCredentials` bundle may carry
`certificate` — the certificate and its unencrypted private key as one PEM text — instead
of `clientSecret`, through the shared certificate grant ADR-0462 put in the oauth2
package. A bundle naming no method is a `clientCredentials` bundle.

**Errors say what to do.** A failed call names the operation, Graph's error code and
message, and where there is something to do, what: a 401/403 names the permission
(`Sites.ReadWrite.All`, or `Sites.Selected` with a grant on the site); a 429 or 503
quotes the wait Graph asked for in `Retry-After`. The retry itself stays the job
protocol's (ADR-0061).

**Consequences.** Positive: a case's whole life in a SharePoint list and its library is
modelled with one Worker Type and one credential. Negative: two job types for one kind,
in every table that lists them; a typed value is a behaviour change for a create-item
that computed a number into a text column (open question above); `list-items` filters
with the `Prefer: HonorNonIndexedQueriesWarningMayFailRandomly` header, so a list past
SharePoint's 5000-item threshold still needs the filtered column indexed. The page token
is the query of Graph's `nextLink` rather than the link itself, so a variable can never
choose the host the bearer token goes to.

### Amendment (2026-10-23): a certificate named by its thumbprint, as for Entra

**Why.** The amendment above lets a `clientCredentials` bundle carry the app's certificate
as PEM, its private key in the clear. On the Windows servers these processes run on, the
certificate usually already sits in the host's store, its key marked **non-exportable**,
and a key that cannot be exported cannot be turned into PEM. The Entra worker has read
such a certificate by its thumbprint since the 2026-10-19 amendment of
[ADR-0172](0172-entra-id-connector.md), which called the mechanism open to anything that
signs a client assertion. The SharePoint worker speaks to the same token endpoint and
could not, so an operator had to register a second certificate with an exportable key for
the same app.

- A `clientCredentials` bundle — or one naming no method — may carry
  `certificateThumbprint` in place of `clientSecret` or `certificate`: the SHA-1
  thumbprint as Windows shows it, searched in `CurrentUser\My` of the account the process
  runs as and then in `LocalMachine\My`. That is `internal/certstore`, the Entra worker's,
  so one thumbprint names one certificate for both; the key signs through CNG and is
  never read. A bundle carries one of the three, two or more are refused naming each, and
  a thumbprint in a `refreshToken` bundle is refused as a certificate is.
- **The store is asked on the first token request, not when the client is built.** This
  is where SharePoint differs from Entra. Entra is worker-only, so its worker opens the
  store while it starts and nothing else touches it. SharePoint has an in-engine half,
  and the engine builds every SharePoint client on its run loop — at startup and after
  every Worker or secret change — even while the kind is offloaded, because the Console
  reads each Worker's state from those clients. Opening the store is a call into a key
  storage provider, including one signature to prove the key is usable, and Atlas does
  not control how long that takes; on the single writer it would hold up every other
  request. So `sharepoint.NewProviderClient` checks only the thumbprint's form, and the
  token source opens the store when it first needs a token, off the loop. A failed open
  is not remembered: a certificate installed, or a key permission granted, afterwards is
  picked up by the next request.
- **The worker still tries the certificate at startup**, as the Entra worker does:
  `sharepointRegistryFromEnv` opens the store for every instance that names a thumbprint,
  so a certificate the store does not hold, or a key the account may not use, is a
  startup error naming the instance and the account. The cost is Entra's as well: a
  supervised worker that refuses to start serves none of its instances until the one is
  fixed.
- **On a host without a certificate store** — anything but Windows — a thumbprint bundle
  is reported, not served: `buildSharePointClients` records `certstore.ErrUnsupported` as
  the Worker's problem, and `sharepointWorkerEnv` leaves the instance out of the
  supervised worker's environment, as `entraBundleParse` does for a tenant, because
  handing it over would make that worker refuse to start for every instance it serves.
- The Console's secret check (`api/web/secret-shapes.js`) states the same rule: a
  SharePoint bundle gives one of the three, while mail's Microsoft bundle, which shares
  the grant but not the certificate, still needs its secret.

**What it does not decide** is whether the SharePoint and Entra Workers should share an
app registration. The certificate proves the app; the registration decides what the app
may do, and a token for `.default` carries every application permission granted to it. A
SharePoint Worker signed in as the Entra Worker's app can therefore do what the Entra
Worker can — create and disable accounts — on every SharePoint task. A registration of
its own, holding `Sites.Selected` or `Sites.ReadWrite.All` and nothing else, keeps the
two apart.

## Pros and cons of the options

### Operation — create list item (chosen)
- Good: the most common "record a row" workflow step; clean result-variable output.
- Bad: does not cover document upload or reads (follow-ups).

### Auth model — app-only + refresh-token (chosen)
- Good: covers app-only tenants and delegated/consumer scenarios; parity with the mail
  Graph provider; the operator picks per connector via the bundle's `method`.
- Bad: two grants to implement and test instead of one.

### Self-contained `sharepoint` package (chosen)
- Good: additive and isolated from the shipped mail connector; follows the codebase's
  per-connector-helper convention; the two grants it needs are small and unit-tested.
- Bad: duplicates the OAuth grant/token-cache code from mail (a unify follow-up).

## Links

- follows ADR-0079 (outbound mail connector) and ADR-0093 (native Graph provider /
  OAuth): provider in the managed store, message/target in the model
- realizes the connector catalog concept of ADR-0067; output mapping mirrors ADR-0067
- honors I1/I2/I4/I5/I6 and ADR-0005/0007/0041; keeps ADR-0010 (grants use only the
  standard library)
- the 2026-10-23 amendment takes the thumbprint credential from
  [ADR-0172](0172-entra-id-connector.md)'s amendment of 2026-10-19
- extended by [ADR-0484](0484-sharepoint-mock-mode.md): a `mock`
  provider whose site lives in the server's memory, so a SharePoint model runs without a
  tenant
