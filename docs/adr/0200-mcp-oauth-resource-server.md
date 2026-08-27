# ADR-0200: Atlas as an OAuth resource server, so a hosted MCP client can connect

- **Status:** Proposed
- **Date:** 2026-08-27
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0196](0196-authenticated-mcp-transport.md) put `/mcp` behind the boundary and
made a tool call act as its caller: the transport takes the session cookie or a
bearer token the request arrived with, and forwards it. That was right, and it
works — for a client that runs where a person can configure it. `atlas mcp --token`
holds an API token (ADR-0194). A `.mcp.json` on somebody's laptop holds a header.

It does not work for a **hosted** client, and that case was not considered. A
connector on claude.ai — or any agent platform that reaches the server from its own
infrastructure on behalf of a person sitting in a browser — has nowhere to put a
bearer token. Its dialog offers a URL and, under advanced settings, an optional
OAuth client id and secret. Nothing else.

This is not hypothetical. A working connector against a running Atlas stopped the
moment authentication arrived. The client got `401`, read
`WWW-Authenticate: Bearer realm="atlas"`, found no pointer to anything, guessed
`/authorize`, and got a `404` — which is correct, because Atlas serves no such
route. Every "OAuth" in this repository is Atlas as an OAuth *client* toward
Microsoft Graph, SharePoint and Entra. It has never been on the other side.

So the gap is sharper than "MCP over HTTP needs a credential", which ADR-0196
solved. It is this: **an API token is a credential for a machine that a human
configures on that machine. A hosted client is a machine nobody can configure.**
The person can only press "connect". Handing them a token to paste would mean
minting a long-lived bearer that then lives in a third party's configuration store,
readable by anyone who can read that config — the opposite of what ADR-0194 set out
to make possible.

That is the problem OAuth exists for, and the MCP specification says precisely how
it is to be solved.

### What the specification requires

The MCP authorization spec (revisions 2025-06-18 and 2025-11-25) makes the MCP
server an OAuth **resource server** — it consumes access tokens, it does not have
to issue them. The normative points that bind an implementation:

- **RFC 9728 protected resource metadata is MUST.** A server must support either
  the `WWW-Authenticate` header carrying `resource_metadata` on a `401`, or the
  well-known URI — and clients must support both, preferring the header. The header
  form the spec gives is
  `WWW-Authenticate: Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource", scope="files:read"`.
- **Token audience validation is MUST.** A server must verify the token was issued
  for it, per RFC 8707 §2, and clients must send the `resource` parameter on both
  the authorization and the token request. Token passthrough — forwarding a
  received token upstream — is explicitly forbidden.
- **PKCE with `S256` is MUST on the client**, and a client must check the
  authorization server's `code_challenge_methods_supported` before proceeding. If
  that field is absent, it **must refuse to proceed.** An authorization server that
  omits it is not merely non-ideal; it is unusable.
- **Dynamic client registration (RFC 7591) is MAY**, not MUST — it was softened
  from SHOULD in the November revision. The registration priority a client should
  follow is: pre-registered credentials, then a Client ID Metadata Document, then
  DCR, then ask the person.

That last point decides the size of this work. Pre-registered credentials come
*first*, and the connector dialog has fields for exactly them. **Atlas can be
connectable without implementing RFC 7591 or CIMD at all.**

## Decision drivers

- **A hosted client must be connectable without a long-lived shared bearer.** If
  the answer is "paste an API token", the credential outlives the session, sits in
  someone else's config, and is not attributable to the person using it.
- **The caller's identity must survive.** ADR-0196's property — a tool call is
  exactly as privileged as whoever made it, and is attributed to them — is the point
  of the whole line of work. Any token issued here must carry a person, not a role.
- **Do not become an identity provider by accident.** Atlas already authenticates
  people: it has a user store, bcrypt, sessions and a login screen. It is already an
  identity provider for its own UI. Issuing OAuth tokens makes that explicit rather
  than new — but it is a threshold worth crossing deliberately.
- **A half-built authorization server is worse than none.** Today a client fails
  fast with a `404`. A server that advertises metadata and then mishandles the flow
  fails slowly, confusingly, and possibly insecurely.
- **Federation (O-01) must get easier, not harder.** When Atlas later delegates to
  eIAM or another OIDC provider, that must be a swap of one half, not a rewrite.

## Considered options

1. **Do nothing.** Hosted clients cannot connect; `atlas mcp --token` and a
   configured header remain the only ways.
2. **A header-injecting reverse proxy** in front of `/mcp`.
3. **Resource server + a minimal authorization server, pre-registered clients
   only.**
4. **The full specification**: option 3 plus dynamic client registration and Client
   ID Metadata Documents.
5. **Delegate to an external identity provider now** — do federation (O-01) first
   and be only a resource server.

## Decision outcome

Chosen: **option 3**, with option 4 as a follow-up and option 5 as where this ends
up.

Atlas becomes an OAuth resource server, and gains the smallest authorization server
that a compliant client will actually talk to. Concretely:

**The resource-server half** (small, and owed regardless of the rest):

- `GET /.well-known/oauth-protected-resource`, and the path-suffixed form for the
  transport's own URI, both `accessPublic` by declaration (ADR-0199).
- Every `401` from the boundary gains `resource_metadata="…"` beside the
  `Bearer realm="atlas"` it already carries. This is the piece whose absence turned
  a correct `401` into a guessed `/authorize`, and it is worth landing on its own
  even if nothing else here is built.
- Audience validation on every access token, refusing a token minted for another
  resource. Atlas forwards no token it receives, which the specification forbids and
  which is worth writing down here because the *pre-ADR-0196* adapter did a version
  of exactly that.

**The authorization-server half:**

- `GET /.well-known/oauth-authorization-server`, advertising
  `code_challenge_methods_supported: ["S256"]` — without it a compliant client must
  refuse to proceed, so this field is not optional in practice.
- `GET /authorize`: requires a signed-in person, reusing the session and the login
  screen that already exist, and shows a **consent screen** naming the client and
  what it will be able to reach. It issues a short-lived code bound to the person,
  the client, the PKCE challenge and the `resource`.
- `POST /token`: exchanges that code, with `code_verifier` and `resource`, for an
  access token and a refresh token.
- An OAuth client is registered by an operator in the Console — a name, a redirect
  URI, an id and a secret shown once — and the operator pastes id and secret into
  the client's dialog.

**The token itself** is a third credential shape beside the session cookie and the
API token, and it is deliberately *not* an API token. It stores as a hash only, the
way ADR-0194's does, but it carries a **person**, an audience, an expiry in hours
rather than days, and a refresh token. Its reach is that person's own reach — which
is how ADR-0196's property survives a hosted client. `principalFor` gains one branch;
that it is the single place where a credential becomes a principal is what keeps
this from spreading.

Option 1 is rejected because it makes "Atlas works with AI agents" true only for
agents running on a developer's own machine. Option 2 is rejected outright: a shim
that unconditionally attaches a credential must be publicly reachable for a hosted
client to use it, and is therefore the pre-ADR-0196 hole with an extra hop — this
record should not leave that idea looking available. Option 4 is right eventually
and is not needed now: pre-registration is first in the spec's own priority order,
and DCR is what lets a client register *without an operator*, which is a
convenience here and an unauthenticated write endpoint to think hard about. Option
5 is the destination, not the next step: federation needs the roles of O-02 to
assign claims to, and until then Atlas would be delegating to an authority whose
answer it cannot yet use.

### Consequences

- **Positive:** a hosted MCP client can connect, and the person who clicked
  "connect" is the identity every tool call runs as — visible in the audit log
  (ADR-0197) like any other sign-in. No long-lived shared bearer is handed to a
  third party.
- **Positive:** the resource-server half alone fixes the confusing failure. A client
  that cannot proceed at least learns *why*, from a pointer rather than a guess.
- **Positive:** doing the RS half first is what makes federation a swap. When an
  external provider takes over, the protected-resource metadata points elsewhere and
  the AS half is deleted; nothing else moves.
- **Negative / trade-offs accepted:** Atlas becomes an authorization server, with
  `/authorize` and `/token` as new public routes and a consent screen to design and
  to get right. The login throttle (ADR-0197) must cover them from the first commit —
  they are password-adjacent surface, and they are the kind of route that gets found.
- **Negative:** more credential shapes. Three ways to become a principal is one more
  than two, and each is a branch in the one function that decides who a request is.
- **Follow-ups / risks to watch:** do not serve the metadata documents before the
  endpoints behind them work — a discoverable, broken flow is worse than a clean
  `404`. Refresh-token rotation and revocation from the Console are part of this, not
  after it; an OAuth grant that cannot be revoked is a worse API token. And the spec
  moves: the November revision softened DCR and added CIMD in five months, so pin the
  revision implemented and re-read it before extending.

## Pros and cons of the options

### 1 — do nothing
- Good: no new public routes, no authorization server, no consent screen.
- Bad: hosted clients cannot connect at all, and the failure is a `404` that explains
  nothing.

### 2 — a header-injecting proxy
- Good: no code, works today.
- Bad: must be publicly reachable to be usable by a hosted client, and hands a valid
  credential to anyone who finds it — the exact arrangement ADR-0196 removed.

### 3 — resource server + minimal authorization server (chosen)
- Good: connectable by hosted clients; the person's identity is what acts; nothing
  built that the spec does not require; the RS half is reusable under federation.
- Bad: Atlas becomes an authorization server, with the surface and the consent UX
  that implies.

### 4 — the full specification, with DCR and CIMD
- Good: a client can register itself; no operator step.
- Bad: an unauthenticated registration endpoint is a decision of its own, and
  pre-registration is first in the spec's priority order — so this buys convenience,
  not connectivity.

### 5 — federate now
- Good: Atlas issues no tokens and holds no consent screen.
- Bad: federation needs roles to map claims onto (O-02), so it cannot come first;
  and the resource-server half is needed either way.

## Links

- extends [ADR-0196](0196-authenticated-mcp-transport.md) to the case it did not
  consider: a client nobody can hand a credential to
- the credential shape it deliberately does not reuse:
  [ADR-0194](0194-api-tokens.md)
- the access classes that keep the new well-known routes public by declaration:
  [ADR-0199](0199-route-access-classes.md)
- the throttle and the audit events the new routes must be covered by:
  [ADR-0197](0197-login-throttle-and-audit-log.md)
- prepares **O-01** (federation) and depends on **O-02** (roles) for its scope story;
  both in
  [`docs/compliance/isds-offene-punkte.md`](../compliance/isds-offene-punkte.md)
- the product-side concept this implements:
  [`docs/compliance/zugriffsschutz-konzept.md`](../compliance/zugriffsschutz-konzept.md),
  measure M10
- specification: MCP authorization, revisions 2025-06-18 and 2025-11-25
  (`modelcontextprotocol.io/specification/2025-11-25/basic/authorization`), building
  on OAuth 2.1, RFC 9728, RFC 8414, RFC 8707 and — optionally — RFC 7591
