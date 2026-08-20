# ADR-0153: SCIM 2.0 provisioning connector

- **Status:** Proposed
- **Date:** 2026-08-19
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas is being evaluated as the process- and approval layer in front of identity
management, where a joiner/mover/leaver process must provision and read accounts in
directories and SaaS applications. Most of those systems expose **SCIM 2.0** (RFC
7643/7644) — the standard cross-vendor provisioning protocol (Entra ID, Okta,
Keycloak, and many SaaS speak it). Without a first-class connector, a modeler has to
express SCIM by hand with the generic REST connector (ADR-0067): assembling resource
URLs, remembering the `application/scim+json` media type, and reading raw HTTP
statuses instead of the SCIM error detail. That is error-prone and leaks protocol
mechanics into every process.

The question: how should a BPMN process perform a SCIM resource operation
(create/get/replace/patch/delete/search a User or Group) against a service provider,
without weakening the engine's durability and single-writer invariants?

## Decision drivers

- **Reuse the established connector seam.** A connector kind must ride the same
  `TypeConnectorTask` → reserved `*JobTypeIndex` → in-process worker path every other
  connector uses (ADR-0007/0067), so it inherits durability, at-least-once delivery,
  and the non-blocking run loop for free.
- **Credentials never in the model.** Like REST, authentication must be a *reference*
  to a server-side secret resolved at call time (ADR-0041), never a value in a BPMN
  file.
- **SCIM semantics, not raw HTTP.** The connector should encode what makes SCIM SCIM —
  the media type, resource-path URLs, filtered search, and the RFC 7644 error object —
  so a modeler authors an operation, not an HTTP request.
- **Operational simplicity.** No new server-side dependency and no new configuration
  surface beyond the secret already used by REST.

## Considered options

1. **No dedicated connector** — model SCIM with the generic REST connector.
2. **A managed connector** (a server-registered SCIM provider, like clio/mail), with
   the base URL and credentials configured server-side.
3. **A model-authored SCIM connector** — a new connector kind whose base URL,
   resource, operation, id, and filter live in the model, authenticating via a
   secret reference, mirroring the REST connector's posture.

## Decision outcome

Chosen option: **"a model-authored SCIM connector"** (option 3), because it matches
the REST connector's proven design exactly where SCIM is REST (a model-authored
endpoint plus a secret reference) and adds only the SCIM-specific behavior on top. It
avoids the extra configuration surface and server-side registry of a managed
connector (option 2), which buys nothing here because a SCIM base URL is naturally
model data, and it removes the hand-assembly and foot-guns of option 1.

Concretely:

- A new reserved job type `io.atlas.scim` at `ScimJobTypeIndex == 16`, reserved by
  `NewBuilder` after the sixteen existing types, so one in-process worker serves
  every deployed process.
- `<atlas:scimConnector baseUrl resource operation resourceId filter bodyVariable
  resultVariable authType authUsername authApiKeyName authSecret retries>` on a
  service task compiles to a `ConnectorTaskDetail`. `baseUrl`/`resource`/`resourceId`/
  `filter` are literal-or-FEEL values (the ADR-0067 fx toggle) evaluated over the
  instance's variables at call time; `operation` is one of
  create/get/replace/patch/delete/search.
- The worker maps the operation to an HTTP method (create→POST, replace→PUT,
  patch→PATCH, delete→DELETE, get/search→GET), assembles the resource URL
  (`baseUrl/resource[/{id}]`, id path-escaped), and for create/replace/patch sends a
  JSON body: the named `bodyVariable`'s JSON object, or — until input mappings exist —
  the whole variable scope, exactly as the REST connector does. A search sends its
  filter as a query parameter.
- The HTTP client sends and accepts `application/scim+json`, carries the job key as an
  `Idempotency-Key`, and turns a non-2xx SCIM error object (`detail`/`scimType`) into
  the job-failure message so a parked incident is legible.
- Authentication (basic/bearer/apiKey) resolves a secret *reference* through the same
  `resolveConnectorSecret` the REST connector uses (ADR-0041). The literal-or-FEEL and
  auth parsing is shared with REST via `connectorValue`/`connectorAuth` rather than
  duplicated.

### Consequences

- **Positive:** SCIM provisioning is authorable in the Modeler as a first-class
  operation; it inherits durability, recovery, and at-least-once delivery; no new
  server dependency or configuration; the error detail surfaces to operators.
- **Negative / trade-offs accepted:** the create/replace/patch payload is the whole
  variable scope unless a body variable is named (the same limitation REST carries
  until input mappings land); PATCH bodies must be authored as a SCIM `PatchOp` by the
  modeler — the connector does not synthesize one. The outbound call is bounded by the
  shared connector call budget (ADR-0149), like REST; a per-connector configurable
  timeout is still a follow-up.
- **Follow-ups / risks to watch:** a per-connector configurable HTTP timeout; optional
  automatic pagination for `search` (following `startIndex`/`itemsPerPage`); a Modeler
  palette entry and property panel for the new extension.

## Pros and cons of the options

### Option 1 — generic REST connector
- Good: no new code.
- Bad: every process re-implements SCIM URL assembly, media type, and error handling;
  no shared validation; protocol mechanics leak into the model.

### Option 2 — managed SCIM connector
- Good: base URL and credentials centralized server-side.
- Bad: extra configuration surface and a registry for what is naturally model data;
  inconsistent with REST, the closest sibling; more moving parts for no gain here.

### Option 3 — model-authored SCIM connector (chosen)
- Good: mirrors the REST connector; SCIM semantics encoded once; secret-reference auth;
  no new dependency.
- Bad: shares REST's whole-scope-payload limitation until input mappings exist.

## Links

- relates to ADR-0067 (service-task connector catalog / REST connector)
- relates to ADR-0041 (connector management and secret store)
- relates to ADR-0007 (job protocol durability)
- relates to ADR-0149 (bounded connector call budget) — the SCIM HTTP client reuses it
- relates to ADR-0123 (sanctioned user provisioning for system processes) — the
  internal counterpart for the built-in user store
