# ADR-0049: Internal service authentication for the in-process MCP adapter

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

The single-binary server mounts the MCP adapter at `/mcp` (ADR-0016). The adapter
owns no engine state: every tool call is translated into an HTTP request against
the server's *own* API over loopback, so it stays a pure adapter and can never
violate an engine invariant.

ADR-0044 then added opt-in authentication (`--auth`): with it on, `/api/v1`
requires a session. That broke the adapter — its loopback calls carry no session
cookie, so under `--auth` every MCP tool returned a 401. ADR-0044 flagged this
explicitly as a known limitation ("the `/mcp` adapter is not auth-aware").

The question: **how does a trusted in-process component authenticate its own API
calls when the API requires login — without weakening the boundary and without
turning `/mcp` into a privileged bypass?**

## Decision drivers

- **Reuse the auth boundary, don't fork it.** ADR-0044 built the request→Principal
  resolution to be swappable; a service credential should slot into it, not go
  around it.
- **Least privilege.** The adapter deploys, runs, and queries; it never
  administers users. Its identity should not be able to either.
- **No new persistent surface.** Avoid seeding a durable "service account" with a
  password and a session lifecycle just to authenticate an in-process caller.
- **Keep the external `/mcp` posture unchanged.** `/mcp` was already documented as
  unauthenticated-front-it-with-a-proxy; this change must not silently make it
  more (or differently) exposed.

## Considered options

1. **Internal bearer token → service principal.** The server mints a random token
   at startup (only under `--auth`), hands it to *its own* MCP client, and the
   auth middleware resolves that bearer to a non-admin service principal.
2. **Loopback-IP bypass.** Skip auth for requests whose source is 127.0.0.1.
3. **Seed a real service user.** Create a durable `mcp` account and log the
   adapter in for a session at startup.

## Decision outcome

Chosen: **Option 1.**

- Under `--auth`, `New` mints a 32-byte random `internalToken`. It is exposed only
  to the constructing process via `Server.InternalToken()` — never served over any
  endpoint, never logged. `cmd/atlas` passes it to its own MCP client
  (`mcp.WithBearer`), which attaches `Authorization: Bearer <token>` to every
  loopback call. With auth off the token is empty and `WithBearer("")` is a no-op.
- The auth middleware's `principalFor` honors a valid bearer (constant-time
  compared) by resolving it to a **non-admin** service principal
  (`system:mcp`, no roles). It passes the session gate but is refused by
  `requireAdmin`, so a leaked token cannot manage users.
- The external `/mcp` transport is **unchanged**: still unauthenticated for
  outside callers, still "front it with a reverse proxy before exposing it."

Verified end to end: with `--auth` on, `/api/v1/processes` returns 401 without a
credential, while an MCP `tools/call` (e.g. `atlas_list_processes`) succeeds
because the adapter now authenticates its loopback call.

Option 2 is rejected: a source-IP bypass is brittle (proxies, container
networking, a server bound beyond loopback) and authenticates by *where* a request
comes from rather than *what* it presents — the wrong axis, and easy to get
subtly wrong. Option 3 is rejected: a durable password-backed account and a
managed session are far heavier than authenticating one in-process caller, and
they add a real account an operator would see and have to reason about.

### Consequences

- **Positive:** `--auth` no longer breaks MCP; the fix rides the existing Principal
  boundary (a header-resolved principal — exactly what ADR-0044 anticipated) with
  no new persistent state; the service identity is least-privilege; the token is
  ephemeral and never leaves the process.
- **Negative / trade-offs accepted:** whoever can reach `/mcp` drives the API as
  the (non-admin) service principal — the *same* exposure `/mcp` has always had,
  so the standing "put a proxy in front of `/mcp`" guidance still applies; the
  token is regenerated each start, so it is deliberately unusable as a pre-shared
  secret for an *external* MCP client (it is for the in-process adapter only).
- **Follow-ups / risks to watch:** an auth-aware transport for the external
  `/mcp` endpoint itself (a bearer/OAuth check on the MCP surface, so a remote
  connector authenticates); and scoping the service principal per-tool if the MCP
  surface ever needs a privileged operation.

## Links

- resolves the limitation flagged in
  [ADR-0044](0044-user-management-and-authentication-boundary.md); builds on the
  MCP adapter of [ADR-0016](0016-mcp-server-over-http-api.md)
