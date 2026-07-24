# ADR-0044: User management and the authentication boundary

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas has grown a real product surface — Console, Modeler, Operations, a Tasks
inbox with claimable user tasks (ADR-0042), and a full HTTP API (ADR-0043) — but
it has **no concept of a user**. The server is fully open: anyone who can reach
it can deploy, run, cancel, and complete work, and a user task's assignee is just
a free-text string. The Organization page said as much ("You are the only user in
this organization").

We want to introduce user management. The explicit brief was to *start as small
as possible without boxing ourselves in*, so that the same foundation can later
reach enterprise territory (SSO, RBAC, deactivation, multi-tenancy). The decision
is therefore less about the MVP's feature list and more about which shapes are
load-bearing for that trajectory — a data model and an auth boundary that a later
enterprise build won't have to break.

## Decision drivers

- **Don't box in the enterprise trajectory.** The parts that are expensive to
  change later — the identity data model and the authentication boundary — must
  be right now, even though most enterprise features are deferred.
- **Smallest honest first slice.** Ship a working, coherent vertical (accounts +
  login + a management UI), not a half-built RBAC engine.
- **Don't break the existing single-binary experience.** Today's open,
  single-user deployments must keep working untouched.
- **Respect the engine invariants.** User data is operator/config data, not
  process history; it must not touch the WAL, the processor, or `applyToState`.
- **No footguns.** No hardcoded default credentials; no way to lock every
  operator out of an instance.

## Considered options

For **where identity lives**:
1. Engine state (events through the WAL/processor).
2. A durable **sidecar store**, like forms/projects/DMN references
   (ADR-0019/0028/0034).

For **enforcement**:
1. Mandatory — every request requires login.
2. **Opt-in** — a `WithAuth()` option / `--auth` flag, off by default, mirroring
   how `--docs` gates the API explorer (ADR-0043).

For **the authorization model**:
1. A single `admin` boolean on the user.
2. A **role list** on the user, with only `admin` enforced for now.

For **authentication mechanism**:
1. Local password now, with the model closed to anything else.
2. Local password now, with the model **open to external identity providers**
   (a `source` + `externalId` on the user) so SSO can be added without a
   migration.

## Decision outcome

**Identity is a sidecar store, enforcement is opt-in, authorization is a role
list (only `admin` enforced), and authentication is local passwords behind a
swappable boundary that already has the hooks for external identity.**

Concretely:

- **`User` model** (`api/userstore.go`): a stable, opaque, never-reused `ID`
  decoupled from `Username`/`Email` (either can change, or be reassigned by an
  external IdP, without rewriting references); `Roles []string` (RBAC-ready, not
  a bool); `Disabled` (deactivate without destroying the record or its audit
  trail); `Source` + `ExternalID` (the external-identity hook); a bcrypt
  `PasswordHash` for local users, empty for external ones. The secret never
  leaves the server — responses are built from a `publicUser` projection that has
  no hash field at all.
- **Sidecar store**, one JSON file per user, owned solely by the run-loop
  goroutine — the same discipline as every other sidecar. User data is config,
  not event history, so it stays entirely off the engine's hot path and out of
  the six invariants.
- **Auth boundary** (`api/auth.go`): handlers depend on a resolved `*Principal`
  in the request context, never on "a cookie" or "a session". Today the Principal
  comes from a local-password login and an in-memory session behind an
  HttpOnly/SameSite cookie; tomorrow it can come from an OIDC/JWT bearer token by
  changing only the middleware. Roles are snapshotted into the session so
  authorization reads the context and never touches the user store off the run
  loop.
- **Opt-in enforcement** via `WithAuth()` / `--auth`. Off by default: an existing
  deployment is unchanged. On: `/api/v1` requires a session (except login,
  product info, and the OpenAPI doc), and managing users requires `admin`.
- **First-run bootstrap**: with auth enabled and no users yet, seed an admin from
  `ATLAS_ADMIN_USERNAME` / `ATLAS_ADMIN_PASSWORD`; if no password is set, generate
  a strong one and log it **once**. No credential is ever hardcoded.
- **Lockout guard**: deleting, disabling, or de-admining the last enabled admin
  is refused (409).

### Consequences

- **Positive:** The expensive-to-change surfaces are settled. Adding OIDC is a new
  middleware plus `source="oidc"` users; adding real RBAC is enforcing more roles
  that already round-trip through the model and UI; adding deactivation policy
  builds on `Disabled`. None require a data migration. Existing deployments are
  untouched until an operator opts in.
- **Negative / trade-offs accepted:**
  - **Sessions are in memory**, so a server restart logs everyone out. Acceptable
    for a single-node build; durable/shared sessions are a later concern.
  - **Roles are snapshotted at login**, so a role change takes effect on the
    user's next login (a disable is applied immediately by dropping live
    sessions).
  - **The `/mcp` adapter is not auth-aware.** It proxies to the loopback HTTP API
    without a session, so under `--auth` its calls are rejected. Until the adapter
    carries a credential, don't enable `--auth` on an instance that also serves
    MCP without fronting MCP separately (the endpoint was already documented as
    "put auth in front of it" — ADR-0016).
- **Follow-ups / risks to watch:** external identity providers (OIDC/SAML/LDAP)
  via `Source`/`ExternalID`; per-endpoint RBAC beyond `admin`; groups; durable and
  shared sessions; multi-tenancy (a tenant/org field the store layout and ID
  scheme already leave room for); audit logging; richer password policy and MFA;
  making user-task assignment (ADR-0042) pick from real users.

## Pros and cons of the options

### Identity in the engine vs. a sidecar
- **Sidecar (chosen)** — Good: keeps config data off the hot path and out of the
  invariants; reuses a proven, durable pattern. Bad: user changes aren't part of
  the event history (they don't need to be).
- **Engine state** — Good: one uniform persistence path. Bad: pollutes the
  event log with non-process data, drags account writes onto the single-writer
  loop, and complicates `applyToState` for no benefit.

### Opt-in vs. mandatory enforcement
- **Opt-in (chosen)** — Good: zero blast radius on existing deployments; a clean,
  already-established gating pattern. Bad: an operator must remember to turn it on.
- **Mandatory** — Good: secure by default. Bad: breaks every existing flow, the
  MCP adapter, the API explorer, and the tests at once — the opposite of "as small
  as possible."

### Role list vs. admin bool
- **Role list (chosen)** — Good: RBAC-ready with no reshaping later. Bad: slightly
  more than the MVP strictly needs today.
- **Admin bool** — Good: minimal. Bad: boxes in exactly the axis we were told to
  keep open.

## Links

- relates to ADR-0019 (durable sidecar stores), ADR-0028 (forms/Tasks app),
  ADR-0042 (user-task assignment), ADR-0043 (opt-in `--docs` precedent),
  ADR-0016 (unauthenticated MCP endpoint)
