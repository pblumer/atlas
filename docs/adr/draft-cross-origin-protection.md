# ADR-DRAFT: A browser's request that changes something comes from Atlas's own origin

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-24
- **Deciders:** Atlas maintainers (security review of 2026-11-13, request of 2026-11-24
  to carry out its hardening items)
- **Open question:** whether an installation needs a setting that names further origins
  as its own. One origin is trusted today beside the request's own host: the one in
  `--external-url`. An installation reached under several names (ADR-0562) through a
  proxy that rewrites `Host`, by a browser that sends no `Sec-Fetch-Site`, has no way
  to name the second.
- **Question checked:** 2026-11

## Context and problem statement

A browser attaches a person's session to a request whichever page caused it. What kept
another page from acting as the signed-in person was one attribute of the session
cookie, `SameSite=Lax` (ADR-0044), which keeps the cookie off a state-changing request
from another *site*.

A site is wider than an origin. Two origins are the same site when they share a
registrable domain, whatever the host name below it and whatever the port:

- A service under a sibling host name — `wiki.example.com` beside `atlas.example.com` —
  is the same site. A page it serves, or a script somebody managed to place on it, sends
  requests to Atlas that carry the session.
- Another port of the same host is the same site, and cookies are not scoped to a port
  at all. ADR-0204 names this for hosted apps and leaves it as "a residual CSRF question
  worth documenting rather than papering over".
- A server run open (`--auth=false`, development and demos) has no cookie to withhold.
  Any page a person on that machine opens can post to `localhost`.

The compliance concept recorded the state as it was: "CSRF-Schutz über `SameSite=Lax`".
The security review of 2026-11-13 listed it as a hardening gap, not as a vulnerability
with a known path: no request was shown to succeed against a default installation.

Browsers say more than a cookie attribute can use. `Sec-Fetch-Site` states whether a
request is `same-origin`, `same-site`, `cross-site` or caused by the person directly
(`none`), and `Origin` names where it comes from. A page's script can set neither.
Since Go 1.25 the standard library reads both (`http.CrossOriginProtection`).

Should Atlas refuse a browser's state-changing request that does not come from its own
origin, and what does that cost the callers that are not the Console?

## Decision drivers

- **An origin, not a site,** is the boundary the rest of the browser's model draws, and
  the one an operator can reason about: one scheme, one host, one port.
- **No client that is not a browser changes.** Workers, the command line, the MCP
  adapter, scripts with an API token and every integration send neither header.
- **What is called from elsewhere on purpose keeps working** (ADR-0186, ADR-0200).
- **No new state and no token to carry.** A synchroniser token would touch every form
  and every `fetch` in the Console and add a store.
- **A refusal has to explain itself,** because one of its two causes is the operator's
  own proxy.
- **No new dependency** (ADR-0010).

## Considered options

1. **`http.CrossOriginProtection` in front of the access layer,** with the public form
   family and the OAuth client endpoints exempt and `--external-url` trusted.
2. **A synchroniser token** (a per-session value every state-changing request repeats).
3. **`SameSite=Strict`** on the session cookie.
4. **The same check, behind a setting that is off by default.**
5. **Nothing,** as it was.

## Decision outcome

Chosen option: **1.**

### The rule

`withCrossOriginProtection` (`api/crossorigin.go`) wraps the access layer:

- `GET`, `HEAD` and `OPTIONS` pass. They change nothing, and a page of another origin
  cannot read the answer.
- Any other method passes when `Sec-Fetch-Site` is `same-origin` or `none`.
- When that header is absent — an old browser (the last of the large ones added it in
  2023), or any browser on an origin that is plain HTTP and not `localhost`, where
  browsers do not send it — the request
  passes when it has no `Origin` either, or when the `Origin` names the host the
  request arrived for.
- Everything else is answered `403` with a sentence that names both readings of the
  refusal, and is logged as `auth.cross_origin_refused` with the path, the origin the
  browser named and the host the request arrived for.

A request with neither header is not a browser's and is not judged. That is what keeps
every API client as it was; it is also why this is no protection against a client that
is not a browser, which never needed a person's cookie to begin with.

### What stays reachable from other origins

- **`/public/forms/…` and `/public/wizard/…`** — a start form and the wizard behind it
  are submitted from the page that embeds them, under the CORS rule of ADR-0186, with
  the link's token and no session.
- **`POST /oauth/token`, `/oauth/revoke`, `/oauth/register`** — they authenticate a
  client from the body of the request and read no cookie (ADR-0200, ADR-0510).

Nothing else is exempt. `/mcp` and `/api/v1` are not: a page of another origin cannot
call them today either, because no CORS header is sent for them and a bearer credential
is not something a browser adds by itself.

### The origin the server was told is its own

Behind a proxy that does not pass `Host` on, the origin a browser names and the host
the request arrives for are two strings for one server. A browser that sends
`Sec-Fetch-Site` is unaffected. For the others, the origin in `--external-url` is
trusted: the operator has already answered the question the request can no longer
answer. The proxy configuration in `docs/install.md` passes `Host` on, and every
absolute URL the server builds (`externalBase`) already depends on one of the two.

### Where it sits

In front of `withAuth`: a refused request is not authenticated, spends nobody's login
budget and reaches no handler. Behind the baseline headers and the transport policy, so
a refusal carries them like every other answer.

### Scale-out readiness

Stateless per request and per node: the decision reads the request's headers and one
setting. Nodes behind one name must share `--external-url`, as they already must for
the discovery documents. Nothing touches partition state, routing or the log.

### Consequences

- **Positive:** a page of a sibling host, of another port or of any other site can no
  longer cause a state change as the signed-in person, and a server run open on
  `localhost` is no longer writable by whatever page its user has open. The login
  itself is covered, so a page elsewhere cannot sign a browser in as somebody else. No
  token, no store, no change to the Console's code.
- **Negative / trade-offs accepted:**
  - An installation behind a proxy that rewrites `Host`, without `--external-url`, used
    from a browser that sends no `Sec-Fetch-Site` (plain HTTP on a name other than
    `localhost`, or an old browser), loses every state-changing request of the Console
    until one of the two is set. The refusal and the log line say so.
  - A browser application on another origin that calls `/api/v1` with the person's
    session is refused. That is the request this record exists to refuse; it is listed
    because somebody may have built on it.
  - A desktop shell or an extension that sends `Origin` from an opaque or foreign
    origin together with a state-changing request is refused. None is known.
  - `GET` is trusted to change nothing. A route that changed state on `GET` would be
    outside this rule; none is known, and none was searched for exhaustively.
  - One more line an internet scanner can cause in the log, at WARN.
- **Follow-ups / risks to watch:** the open question above; a route that grows a CORS
  rule of its own has to be added to the exemptions or it will be refused before the
  rule applies; ADR-0204's hosted apps, if built, inherit this boundary and its
  sibling-subdomain question is answered by it.

## Pros and cons of the options

### 1. The standard library's check
- Good: reads what only the browser can say; no state; no client-side change; the
  implementation is the Go project's and is maintained there.
- Bad: rests on `Origin` against `Host` where `Sec-Fetch-Site` is missing, which a
  proxy can break; trusts `GET`.

### 2. A synchroniser token
- Good: independent of browser version and of what a proxy does to `Host`.
- Bad: every form and every `fetch` in the Console carries it, the public pages need an
  exception anyway, and it is one more secret to keep out of logs and caches.

### 3. `SameSite=Strict`
- Good: one word.
- Bad: still a site and not an origin, so it closes none of the three cases above; and a
  person who follows a link to Atlas from a mail or a ticket arrives signed out.

### 4. Behind a setting, off by default
- Good: nothing changes for anybody who does not ask.
- Bad: the installations that need it most are the ones that will not know to ask, and
  the one configuration it can break announces itself in the first refused request.

### 5. Nothing
- Good: no risk of refusing the Console behind an unusual proxy.
- Bad: leaves a protection that stops at the site boundary while the hosts beside Atlas
  under one domain are exactly where another team's software runs.

## Links

- refines [ADR-0044](0044-user-management-and-authentication-boundary.md) (the session
  cookie and its `SameSite` attribute)
- keeps [ADR-0186](0186-embed-public-forms-cross-origin.md) (the public form family is
  called and framed from other origins)
- relates to [ADR-0200](0200-mcp-oauth-resource-server.md) and
  [ADR-0510](0510-tasks-for-a-connected-system.md) (the endpoints that authenticate a
  client)
- answers the residual question of [ADR-0204](0204-hosted-apps-on-an-isolated-origin.md)
- relates to [ADR-0560](0560-https-redirect-and-hsts.md) (the other header that is the
  operator's to switch on) and the baseline headers every answer carries
  (`api/securityheaders.go`): `nosniff`, `Referrer-Policy: same-origin`, and framing by
  this origin only, with the public form family left frameable for ADR-0186
