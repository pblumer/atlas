# ADR-0080: Native Gmail and Microsoft Graph mail providers

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0079 shipped the outbound mail connector with SMTP as its only transport, behind a
`mail.Client` seam, and named native provider APIs as the explicit follow-up: "native
Gmail / Microsoft Graph API providers are additive behind the same `Client` seam."

SMTP already reaches Google and Microsoft 365 via their submission endpoints with an
app password. But many organizations disable SMTP AUTH (Microsoft is deprecating basic
auth for SMTP; Google Workspace admins routinely turn it off) and mandate OAuth2. For
those tenants the native provider APIs — Gmail `messages.send` and Microsoft Graph
`sendMail` — are the only supported path, and they authenticate with OAuth2 bearer
tokens rather than a password. The question is how to add both providers without a
model change and without leaking a credential into a model or an event.

## Decision drivers

- **Additive, not a rewrite (ADR-0079).** A new provider should be one dispatch branch
  plus its client, behind the existing `mail.Client` seam. Models are untouched: a mail
  task still names a connector; the provider is a server-side concern.
- **Honor the secret model (ADR-0041, I6).** A client secret, a refresh token, and a
  service-account private key are all secrets. They must live in the vault, never in a
  model, a connector record field, or an event.
- **Server-appropriate auth.** Sending mail from a workflow has no interactive user, so
  app-only grants are the norm — but a single consumer mailbox (a plain `gmail.com`
  account, which cannot use domain-wide delegation) still needs a path.
- **No heavy dependency.** The engine is minimal-dependency and no-CGO (ADR-0010); an
  OAuth/JWT library is avoidable — the grants are a form POST and, for a service
  account, an RS256-signed JWT, both of which the standard library covers.

## Considered options

**Auth model.** (a) App-only only — Graph client-credentials, Gmail service-account
with domain-wide delegation. (b) Refresh-token only — a pre-obtained token per
connector, works for consumer accounts. (c) **Both**, selected per connector.

**Where the credential lives.** (A) Typed fields on the connector record (tenantId,
clientId, …) with only the secret in the vault. (B) **One JSON auth bundle in the
vault** under the connector's `credentialsRef`; the record keeps only provider, sender,
and the reference.

**Token library.** (i) Add `golang.org/x/oauth2` (+ `google`). (ii) **Implement the
grants in the standard library.**

## Decision outcome

Chosen: **both auth models (option c), one vault JSON bundle (option B), grants in the
standard library (option ii).**

- The **`mail.Client` seam gains two implementations.** `GraphClient` POSTs a structured
  message to `/users/{mailbox}/sendMail`; `GmailClient` POSTs the base64url-encoded RFC
  5322 message (reusing the SMTP client's MIME builder) to `/users/me/messages/send`.
  Both attach a bearer token from a `TokenSource` and treat any non-2xx as a send
  failure, so the job stays pending and retries (ADR-0007/0061).
- A **`TokenSource` abstraction** yields a cached, auto-refreshing access token. Three
  grants back it: `clientCredentials` (Graph app-only), `refreshToken` (either
  provider, incl. consumer Gmail), and `serviceAccount` (Gmail domain-wide delegation,
  an RS256 JWT-bearer assertion signed with `crypto/rsa`). A cache wrapper refreshes a
  token a minute before expiry; a response with no `expires_in` is never cached.
- The **credential is one JSON bundle in the vault**, resolved via the existing
  `credentialsRef` → `resolveConnectorSecret` path. Its `method` field selects the
  grant; the remaining fields (secret and non-secret alike) configure it. The connector
  record is unchanged beyond the `provider` it already carries — no schema growth, and
  every secret stays in the vault.
- **Provider dispatch lives in `mail.NewProviderClient`**, the single place that turns a
  managed connector's provider + sender + endpoint + resolved secret into a client. The
  server's `buildMailClients` calls it and skips a connector whose bundle is malformed
  (its tasks park until fixed), exactly as an unconfigured connector already parks.

### Consequences

- **Positive:** OAuth-only Google Workspace and Microsoft 365 tenants can send natively;
  a consumer Gmail account works via a refresh token; no model change and no new
  dependency; adding a third provider stays a one-branch change; all credentials remain
  in the vault.
- **Negative / trade-offs accepted:** hand-rolled OAuth means we own the grant and
  JWT-signing code (kept small and unit-tested against fake token/API servers); the
  auth bundle is an operator-authored JSON blob (documented, validated at build time,
  parked-on-error rather than fatal); token acquisition adds a network round trip per
  connector, mitigated by caching; Graph `sendMail` sends plain-text bodies only, in
  step with the SMTP client (HTML/attachments remain the ADR-0079 follow-up).
- **Follow-ups / risks to watch:** HTML bodies and attachments across all providers; a
  per-send/token HTTP timeout; surfacing a misconfigured-bundle skip as an incident or
  Console warning rather than a silent park; optionally validating the bundle shape at
  connector-create time.

## Pros and cons of the options

### Auth model — both (chosen)
- Good: covers app-only tenants and single consumer mailboxes; the operator picks per
  connector via the bundle's `method`.
- Bad: three grants to implement and test instead of one.

### Credential in a vault JSON bundle (chosen)
- Good: no record-schema growth; secrets never leave the vault; one reference to manage.
- Bad: the bundle is free-form JSON an operator must get right; shape errors surface at
  build time, not create time (a follow-up).

### Standard-library grants (chosen)
- Good: no new dependency (ADR-0010); full control and testability.
- Bad: we maintain JWT signing and token flows ourselves.

## Links

- follows up ADR-0079 (outbound mail connector) — realizes its native-provider follow-up
- honors ADR-0041 (secret store / vault) and I6; delivery inherits ADR-0007/0061
- keeps ADR-0010 (no heavy deps): grants use only the standard library
