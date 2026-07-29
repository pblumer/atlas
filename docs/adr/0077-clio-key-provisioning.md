# ADR-0077: One-click clio credential provisioning

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

## Context and problem statement

A managed clio connector (ADR-0036/0041) references its token by name; the value is
sealed in the vault (ADR-0069). Provisioning that value was manual: an operator
creates a key in clio, copies the once-shown secret, and pastes it into Atlas's
secret store. That copy-paste is the main friction of setting up (or rotating) a
connector, and it is error-prone — a stale or mis-scoped token fails **silently**,
because the inbound bridge treats a read error as transient (ADR-0075). The clio
key that surfaced this (`atlas-agent-01`) had been revoked and re-minted, but the
vault still held the revoked value, so the bridge read nothing and started nothing.

clio exposes an admin key API (`POST /api/v1/keys`) that mints a scoped key and
returns its full secret once. The question: can Atlas provision a connector's
credential directly through that API, so no human handles the token?

## Decision drivers

- **Remove the copy-paste.** The token should never transit an operator's clipboard.
- **Least privilege.** The minted key is read-only, scoped to the watched subject.
- **No standing admin secret.** Atlas should not have to store a clio admin
  credential to make this work.
- **Invariants.** The mint is a network call — off the run loop (I3); the token is a
  secret and must never reach the WAL, an event, or a log (I6).

## Considered options

1. **Keep manual copy-paste.** No new surface, but the friction and the
   silent-stale-token failure remain.
2. **QR / mobile hand-off.** A phone captures the token from clio and forwards it.
   Nicer for human-carried secrets, but a phone in a server-to-server flow adds
   steps, and a token in a QR is still a plaintext secret in an image.
3. **Server-side mint with an operator-supplied admin token (chosen).** Atlas calls
   clio's key API with an admin token the operator supplies **once** in the request,
   receives the scoped key, and seals it as the connector's credential — then
   rebuilds the connector clients so it goes live immediately.

## Decision outcome

Chosen: **option 3.** `POST /api/v1/connectors/{id}/provision-clio-key` (admin-gated)
takes `{adminToken, subject, recursive, keyName?, expiresAt?}`. It resolves the
connector on the run loop, mints a `read:<subject>` (or `read:<subject>/*` when
recursive) key **off** the run loop via `clio.MintKey` (a standalone call, not a
registry `Client`, because it uses an admin token distinct from the connector's read
token), then on the run loop seals the returned key in the vault under the
connector's `credentialsRef` (deriving one from the connector name if unset) and
calls `rebuildConnectorRegistries`. The response carries only the credential
reference, scope, and key name — never the token.

The **admin token is used solely for the one mint and is never persisted** — not in
the connector record, the vault, an event, or a log (I6). Atlas therefore holds no
standing clio admin credential. This pairs with the companion change that rebuilds
the connector clients whenever a secret is set or deleted, so a rotated token — by
provisioning or by hand — reaches the live bridge without re-saving the connector.

### Consequences

- **Positive:** setup and rotation are one action in the Console; the token never
  touches a clipboard; the minted key is least-privilege (read-only, subject-scoped);
  Atlas keeps no standing admin secret; a rotated credential goes live at once.
- **Negative / trade-offs accepted:** the operator must paste an admin token once per
  provisioning (it is masked and unstored); Atlas now depends on clio's admin key API
  shape (isolated in `clio.MintKey`).
- **Follow-ups / risks to watch:** a device-authorization ("approve in clio") flow to
  avoid even the one-time admin paste; scheduled auto-rotation before `expiresAt`;
  surfacing a clio read/authorization failure as a connector health signal so a
  mis-scoped or revoked token is not silent.

## Pros and cons of the options

### Option 1 — manual copy-paste
- Good: zero new surface.
- Bad: the friction and the silent-stale-token failure that motivated this remain.

### Option 2 — QR / mobile
- Good: pleasant for human-carried secrets (2FA-style enrolment).
- Bad: a phone adds steps to a server-to-server flow; a token in a QR is still a
  plaintext secret exposed to screenshots/shoulder-surfing — no safer than paste.

### Option 3 — server-side mint (chosen)
- Good: no human handles the token; least privilege; no standing admin secret.
- Bad: one admin paste per provisioning; couples to clio's key API (isolated).

## Links

- builds on ADR-0036 (clio connector), ADR-0041 (managed connectors + references),
  ADR-0069 (encrypted vault), ADR-0075 (inbound bridge — the silent-failure path)
- honors I3 (no network on the run loop) and I6 (secrets never reach the log/WAL)
