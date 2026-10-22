# ADR-0141: A SharePoint connector (create list item, via Microsoft Graph)

- **Status:** Accepted (amended 2026-10-22: a `clientCredentials` app may prove itself with a certificate instead of a client secret — as PEM in the bundle's `certificate`, or named by its thumbprint in the Windows certificate store in `certificateThumbprint` — the Entra worker's three credentials read the same way; the store is asked on the first token request rather than while the engine builds the client on its run loop, and tried at startup by the worker)
- **Implementation:** Landed
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team

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

### Amendment (2026-10-22): a certificate instead of a client secret, as for Entra

**Why.** The SharePoint worker reaches Graph through the same token endpoint as the Entra
worker, but its bundle took only a client secret or a refresh token. An organization that
allows its app registrations nothing but certificates — or that already keeps the app's
certificate in the store of the Windows host, its key non-exportable, for the Entra worker
or for PowerShell — could not sign the SharePoint worker in with it. The mechanism existed
(`oauth2.ClientCertificate`, `internal/certstore`); the 2026-10-19 amendment of
[ADR-0172](0172-entra-id-connector.md) called it open to anything that signs a client
assertion and built for Entra first.

- A `clientCredentials` bundle carries exactly one of `clientSecret`, `certificate` (the
  certificate and its unencrypted private key as one PEM text) or `certificateThumbprint`
  (a certificate in `CurrentUser\My` of the account the process runs as, then
  `LocalMachine\My`). The rules are the Entra worker's: two set is refused, naming both;
  the key must be RSA; a PEM is read by `oauth2.ParseCertificateBundle` and a thumbprint
  by `certstore`, so the two Worker Types refuse the same input.
- `refreshToken` keeps its client secret. A certificate in such a bundle is refused rather
  than ignored: a grant that quietly kept signing in with the secret would hide what the
  operator meant to change.
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
  is reported, not built: `buildSharePointClients` records `certstore.ErrUnsupported` as
  the Worker's problem, and `sharepointWorkerEnv` leaves the instance out of the
  supervised worker's environment, as `entraBundleParse` does for a tenant, because
  handing it over would make that worker refuse to start for every instance it serves.
- The Console's secret check (`api/web/secret-shapes.js`) states the same rule: a
  SharePoint `clientCredentials` bundle needs one of the three, while mail's Microsoft
  bundle, which shares the grant but not the certificate, still needs its secret.

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
- the 2026-10-22 amendment takes the certificate credential, PEM and thumbprint, from
  [ADR-0172](0172-entra-id-connector.md)'s amendments of 2026-10-17 and 2026-10-19
