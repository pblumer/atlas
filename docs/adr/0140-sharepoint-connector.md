# ADR-0140: A SharePoint connector (create list item, via Microsoft Graph)

- **Status:** Accepted
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
