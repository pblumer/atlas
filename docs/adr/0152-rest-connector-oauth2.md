# ADR-0152: OAuth2 client-credentials for the REST connector

- **Status:** Proposed
- **Date:** 2026-08-19
- **Deciders:** Atlas maintainers

## Context and problem statement

The REST connector (ADR-0067) authenticates with HTTP Basic, a static Bearer token,
or an api-key header — each resolved from a server-side secret reference (ADR-0041).
But most machine-to-machine REST and SCIM APIs used for identity provisioning
(Entra ID / Microsoft Graph, Okta, Keycloak, ServiceNow, Salesforce, and countless
in-house services behind an OAuth2 gateway) are guarded by **OAuth2 client
credentials**: the caller exchanges a client id and secret at a token endpoint for a
short-lived bearer token, then calls the API with it.

Without this, a modeler cannot call those APIs from a REST connector task at all: a
static Bearer token expires, and there is no place in the model to author the token
exchange. The question: how should the REST connector obtain and use an OAuth2
client-credentials token, without weakening the engine's durability or its
credentials-never-in-the-model rule?

## Decision drivers

- **Cover the common real-world auth.** OAuth2 client-credentials is the default for
  the provisioning APIs this connector initiative targets (epic #431).
- **Credentials never in the model.** The client secret must stay a *reference*
  (ADR-0041); only the token endpoint, client id, and scope — not secret — are model
  data.
- **Stay on the connector seam.** No blocking the run loop unboundedly, no new
  server dependency, reuse the bounded HTTP budget (ADR-0149).
- **Don't hammer the token endpoint.** A burst of jobs against one API should share a
  token rather than fetch one per job.

## Considered options

1. **Do nothing** — modelers pre-mint a token and paste it as a static Bearer secret.
2. **A separate "get token" connector task** the modeler wires before each call,
   writing the token into a variable used by a following Bearer call.
3. **A new `oauth2` auth type on the REST connector** that fetches (and caches) the
   token transparently as part of the call.

## Decision outcome

Chosen option: **"a new `oauth2` auth type"** (option 3). It matches how every other
auth scheme is authored (one `authType` on the task) and keeps the token exchange an
invisible mechanism of the connector rather than modeling homework.

Concretely:

- `authType="oauth2"` on `<atlas:restConnector>` with `authTokenUrl`, `authClientId`,
  `authScope` (model data) and `authSecret` (the **client secret reference**). The
  compiler validates that token url, client id, and secret reference are all present.
- A `TokenProvider` performs the client-credentials grant: an
  `application/x-www-form-urlencoded` POST to the token endpoint with
  `grant_type=client_credentials` (and `scope`), the client authenticated via HTTP
  Basic (RFC 6749 §2.3.1). It parses `access_token`/`expires_in` and returns the
  token, which the worker attaches as `Authorization: Bearer …`.
- Tokens are **cached** per (token url, client id, scope) until `tokenSkew` (30 s)
  before expiry, so a run of jobs reuses one token. REST jobs are driven serially on
  the run loop, so the cache needs no locking for correctness (a mutex guards it
  anyway). The token HTTP client uses the shared bounded budget (ADR-0149).
- A missing client secret, an absent token provider, or a token-endpoint failure
  fails the job (retry → incident, ADR-0061) rather than calling the API
  unauthenticated.

The token exchange and caching live in the `rest` package and are reusable by the
SCIM connector, which shares the same `RestAuth` model.

### Consequences

- **Positive:** the REST (and SCIM) connector can call the OAuth2-guarded APIs that
  dominate identity provisioning; tokens refresh automatically and are shared across
  jobs; the client secret never leaves the secret store.
- **Negative / trade-offs accepted:** only the client-credentials grant is supported
  (no authorization-code / refresh-token / on-behalf-of flows — those need a user
  context a service task does not have); the cache is in-memory and per-process, so a
  restart re-fetches (harmless); no `client_secret_post` variant (Basic only) yet.
- **Follow-ups / risks to watch:** optional `client_secret_post` and mTLS client
  auth; audience/resource parameters some providers require; surfacing token-endpoint
  error bodies into the incident message.

## Pros and cons of the options

### Option 1 — static Bearer only
- Good: no new code.
- Bad: tokens expire; a human must mint and rotate them; unusable for real M2M APIs.

### Option 2 — a separate get-token task
- Good: no new auth type.
- Bad: every call site grows a second task and a token variable; caching and refresh
  become the modeler's problem; the client secret flows through process variables.

### Option 3 — an `oauth2` auth type (chosen)
- Good: authored like every other scheme; transparent fetch + cache; secret stays a
  reference; reusable by SCIM.
- Bad: covers only the client-credentials grant (the right one for service tasks).

## Links

- relates to ADR-0067 (service-task connector catalog / REST connector)
- relates to ADR-0041 (connector management and secret store)
- relates to ADR-0149 (bounded connector call budget) — the token HTTP client reuses it
- relates to ADR-0007 (job protocol durability)
