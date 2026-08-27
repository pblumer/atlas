# ADR-DRAFT: The MCP transport is authenticated, and acts as its caller

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0016 made the MCP server an adapter over the HTTP API, and `atlas serve`
mounts its Streamable HTTP transport at `/mcp` so a remote client can reach the
same tools. ADR-0049 then gave the in-process adapter a credential — the server's
internal service token — so that turning on `--auth` did not break it.

Both records were explicit that the transport itself performed no authentication,
and both said the same thing about it: front it with a reverse proxy. That was
stated as a deployment caveat. It was in fact a hole, and the combination of the
two decisions made it worse than either did alone:

- **The transport was outside the boundary.** `cmd/atlas` mounted the adapter on
  its own `http.ServeMux`, *beside* `srv.Handler()`. The `withAuth` middleware
  never saw those requests. `--auth` had no effect on them whatsoever.
- **The adapter carried a credential of its own.** Every loopback call it made
  went out with `Authorization: Bearer <internalToken>`, which resolves to the
  `system:mcp` service principal.

So `--auth` did not close `/mcp`; it supplied `/mcp` with a working credential.
An anonymous caller who could reach the port acted as `system:mcp`, passed the
session gate, and reached everything not behind `requireAdmin` — 71 tools,
`atlas_deploy` among them. Deploying means executing code: script tasks run in the
context of the service user and all three languages are on by default (ADR-0047,
recorded as risk R-09 in the ISDS concept). An open `/mcp` was therefore code
execution with no authentication at all.

Two smaller defects came from the same root. The remote stdio adapter
(`atlas mcp --server …`) has no way to present a credential, so it cannot work
against a server with `--auth` — while `atlas worker` has had `--token` for some
time. And the internal token is a single ambient secret shared with the supervised
workers: no expiry, no scope, no revocation, and one identity for every actor
holding it.

The question: **how does the MCP surface become an ordinary part of the
authenticated API, rather than a parallel way in?**

## Decision drivers

- **The transport must not be able to sit outside the boundary**, including by
  how `cmd` chooses to wire it. Where a security-relevant surface is mounted
  should not be a decision the wiring can make differently.
- **Least privilege, and honest attribution.** A tool call should be as
  privileged as the person who made it — no more, and no less.
- **Inherit authorization rather than re-implement it.** Every rule the API grows
  later (roles per endpoint group, project scopes) must apply to MCP without the
  `mcp` package knowing about it.
- **Keep the adapter a pure adapter.** It must stay unable to touch the engine, so
  invariant I3 is enforced in exactly one place (ADR-0016).
- **Do not make the `api` package depend on the `mcp` package.**

## Considered options

For **where the transport is mounted**:

1. Keep it in `cmd/atlas`, and re-implement a credential check there.
2. **Hand it to the `api` server as an `http.Handler`**, which mounts it inside
   its own mux.
3. Have the `api` package import `mcp` and construct the transport itself.

For **which identity a tool call acts as**:

A. Keep the adapter's service token, and authenticate the transport separately.
B. **Forward the caller's own credential** to the loopback API.
C. Mint a per-caller token at the transport, mapped from the caller's session.

## Decision outcome

Chosen: **option 2 with option B.** Both halves are needed; either alone leaves
the surface wrong.

**Mounting.** `api.WithMCP(h http.Handler)` takes the transport and mounts it at
`/mcp` and `/mcp/` with class `accessAuthenticated`
(ADR-draft-route-access-classes), so it passes `withAuth` like every other route.
Under `--auth`, a request with no credential is refused with `401` and a
`WWW-Authenticate: Bearer` header naming the scheme — Bearer and not Basic, since
Basic is what makes a browser open its own credential dialog over the login
screen. `cmd/atlas` no longer builds a second mux, and the dependency runs one
way: `api` takes an `http.Handler` and never imports `mcp`.

**Identity.** The adapter is given no bearer at all on this path. Instead
`ServeHTTP` lifts the `Authorization` and `Cookie` headers off the incoming
request and binds a derived `Client` to them for the duration of that request; the
derived client shallow-copies, so it shares the connection pool and costs one
struct copy. Both headers are forwarded verbatim and never parsed, so the adapter
needs to know neither what Atlas's session cookie is called nor which bearer
schemes the server accepts, and cannot get either wrong when those change.
Whatever authenticated the caller at `/mcp` is exactly what authenticates the
calls their tool makes.

The caller's credential takes precedence over any token the client was built
with. That ordering is the point: an adapter that lent its own token to a request
arriving with a weaker one would re-open, one level down, the privilege the open
transport used to hand out for free.

`Server.handle` becomes `handleWith(client, req)`, taking the client as a
parameter. That is the only structural change dispatch needed, and it is where the
two transports legitimately differ: stdio is a per-agent process with one identity
for its whole life, so it passes the client it was built with, while HTTP binds a
client per request.

That difference is why the stdio adapter gets a credential of its own:
`atlas mcp --token` (or `ATLAS_TOKEN`), the shape `atlas worker` has had for some
time, because it is the same need. It had none and no way to be given one, so it
could not work against a server running `--auth` at all — a gap that only becomes
more visible once the HTTP transport is gated, and one this record would be
incomplete without. The token is trimmed on the way in, because one exported from
a shell profile routinely carries a trailing newline and a bearer sent with one is
refused for a reason nothing in the `401` explains. `runMCP` is split so its
streams can be supplied, which puts the credential's whole path — flag or
environment, into the client, onto the request — under test; it is wiring, and
wiring is exactly what a unit test of the `mcp` package cannot reach.

Four consequences worth stating as behaviour: an MCP call now appears in every
audit trail under the caller's own name; an admin over MCP can reach an
admin-gated tool, which the service principal could not; a non-admin cannot,
which the service principal also could not but for the wrong reason; and a deploy
token presented at `/mcp` is refused there outright with `403`, because the
transport is not one of the two operations `deployAgentAllowed` names. That last
one is the shape of the whole change: ADR-0129's confinement now covers MCP
without anything in the MCP path knowing that deploy tokens exist.

Option 1 is rejected because it puts a security check in the wiring, where the
next person to restructure `cmd` can drop it — and because it would be a second
implementation of a boundary that already exists. Option 3 is rejected as an
unnecessary coupling: an `http.Handler` is the entire contract needed. Option A
is rejected because it fixes only the gate: every call would still arrive as one
service principal, so MCP would remain a privilege ceiling *and* a privilege
floor, and would inherit no future authorization rule. Option C is rejected as
machinery for nothing — the credential the caller already presented is the
credential to use, and minting a second one adds a lifecycle to get wrong.

### Consequences

- **Positive:** `/mcp` is gated by `--auth` like the rest of the surface, and by
  being a declared route rather than by a rule written for it. A tool call is as
  privileged as its caller and attributed to them. Every authorization rule the
  API grows applies to MCP for free. The adapter holds no credential on this path,
  so there is nothing there to leak. The signed-in web UI can drive a tool with
  its own session cookie.
- **Negative / trade-offs accepted:** **this is a breaking change.** An MCP client
  that reached `/mcp` on an `--auth` server without presenting anything now gets
  `401` and must be configured with a credential. That is the defect being fixed,
  but it will be noticed. A static bearer is also only the pragmatic subset of the
  MCP authorization spec, which prescribes OAuth 2.1 with protected-resource
  metadata for remote servers.
- **Follow-ups / risks to watch:** first-class API tokens (named, scoped,
  revocable, expiring) to replace the ambient internal token, which after this
  record is held only by the supervised workers — and which is also what
  `atlas mcp --token` has to be handed today, since there is no better credential
  to give it yet. OAuth 2.1 protected-resource metadata on the transport, once
  a client should fetch its own token rather than be configured with one. The
  `system:mcp` principal name is now historical and deliberately left alone: it is
  a wire value that appears in job attribution and in operators' logs.

## Pros and cons of the options

### Where it is mounted

**1 — keep it in `cmd`, check there**
- Good: no change to the `api` package.
- Bad: a second implementation of the boundary; a check that lives in wiring is a
  check the next restructuring can drop; the mount site can still differ from
  where the boundary is.

**2 — hand it to the `api` server (chosen)**
- Good: one boundary, one mount; the decision is owned by the package that owns
  the boundary; no new dependency in either direction.
- Bad: `api` grows one more option.

**3 — `api` imports `mcp`**
- Good: nothing for `cmd` to wire.
- Bad: couples the API package to the adapter that adapts it, for no gain over an
  `http.Handler`.

### Which identity

**A — keep the service token, authenticate the transport separately**
- Good: smallest change; the transport is at least gated.
- Bad: every call still arrives as one principal, so MCP is both a ceiling and a
  floor on what any caller can do; audit trails name the adapter, not the person;
  no future authorization rule reaches it.

**B — forward the caller's credential (chosen)**
- Good: least privilege by construction; honest attribution; inherits every rule
  the API has and will have; nothing for the adapter to leak.
- Bad: the adapter must thread a per-request client through dispatch.

**C — mint a per-caller token at the transport**
- Good: the transport could narrow what it issues.
- Bad: a second credential lifecycle to mint, expire and revoke, to express
  something the caller already presented.

## Links

- supersedes the "performs no authentication — front it with a reverse proxy"
  posture of [ADR-0016](0016-mcp-server-over-http-api.md); the adapter shape it
  chose is unchanged
- resolves the follow-up named in
  [ADR-0049](0049-internal-service-auth-for-mcp.md) ("an auth-aware transport for
  the external `/mcp` endpoint itself"); the internal token keeps its other holder,
  the supervised workers
- builds on ADR-draft-route-access-classes, which is what makes `/mcp` gateable as
  an ordinary route
- relates to [ADR-0044](0044-user-management-and-authentication-boundary.md) (the
  `*Principal` boundary this rides) and
  [ADR-0129](0129-remote-deployment-targets.md), whose deploy-token allowlist now
  covers `/mcp` for free, refusing one at the transport
- the product-side concept this implements:
  [`docs/compliance/zugriffsschutz-konzept.md`](../compliance/zugriffsschutz-konzept.md), measure M2
