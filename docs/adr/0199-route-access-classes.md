# ADR-0199: Every mounted route declares its access class

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0044 built the authentication boundary: a request is resolved to a
`*Principal`, and when `--auth` is on, a gated request that carries none is
refused. The boundary itself is sound and has not needed changing since.

What it could not say precisely is *which* requests are gated. That decision was
a path-prefix test in `requiresAuth`:

```go
if path != "/api/v1" && !strings.HasPrefix(path, "/api/v1/") {
    return false          // everything outside /api/v1 is open
}
```

Two consequences followed, and both are defects rather than trade-offs.

**A route was public by omission.** Nothing outside `/api/v1` was gated — not
because anyone decided it should be reachable, but because of where it happened
to be registered. The MCP transport was mounted on a separate mux in
`cmd/atlas`, so the boundary never saw it at all; the Prometheus exposition is
mounted beside `/healthz` and inherited the same silence. A reviewer reading
`requiresAuth` could not tell which routes it left open, because the answer was
not in that function — it was distributed across every mount site in the
repository, including one in a different package.

**A path was one decision, both ways.** The rule keys on the path, so it cannot
distinguish `GET /api/v1/settings/theme` — which the login screen must read
before anyone has a session — from `PUT` of the same path, which is an admin
act. All three settings routes were exempted wholesale, leaving `requireAdmin`
inside each handler as the only thing refusing an anonymous write. That worked,
but it means the boundary was not the boundary for those paths: a handler that
forgot the check would have been open, and nothing outside the handler would
have said so.

An external review put it plainly: not all interfaces are protected by a user
login. The finding was correct, and the reason it was correct is structural.

The question this record answers: **how does the server state, in one reviewable
place, which routes are reachable without a principal — such that a route added
later cannot become public by accident?**

## Decision drivers

- **Fail closed.** The failure mode of forgetting something must be "gated", not
  "open". Today it is the wrong way round.
- **Provable by reading, not by auditing.** The answer to "what can an anonymous
  caller reach?" must be a short list, not a sweep of every mount site.
- **Reuse the shape the repository already trusts.** `deployAgentAllowed`
  (ADR-0129) is a fail-closed allowlist resolved through an `http.ServeMux`, with
  the same argument in its comment. This is that idea one level up.
- **No behavioural surprises.** Beyond tightening what was loose by accident,
  which routes are public must not change in this record.
- **The class must be visible where the route is mounted**, so a reader of the
  mount site knows without going elsewhere.

## Considered options

1. **Keep the prefix rule, extend the exception list.** Add `/mcp`, `/metrics`
   and anything else to a second list of "gated even though outside /api/v1".
2. **Invert the prefix rule.** Gate everything by default, exempt by path.
3. **Give every mounted route an access class**, declared where it is mounted,
   and classify a request by the pattern that will serve it.

## Decision outcome

Chosen: **option 3.**

`api/access.go` introduces `accessClass` with two values today,
`accessAuthenticated` (the zero value) and `accessPublic`. `Handler` is split so
that `mountRoutes` builds the mux and the policy in one pass:

```go
mount := func(class accessClass, pattern string, h http.Handler) {
    mux.Handle(pattern, h)
    policy.declare(pattern, class)
}
```

`mount` is the only way a route reaches the mux and it cannot be called without a
class, so "public because nobody said otherwise" is not a state this function can
produce. The `/api/v1` table is registered in a loop and cannot say "except these
five", so its public entries are named in `publicAPIRoutes` — five patterns, each
with the reason it must work before login. Every route mounted outside that table
states its class at its own mount site.

`classify` then resolves a request by asking the *serving* mux which pattern wins
and looking that pattern up in what was declared:

```go
func (p *accessPolicy) classify(r *http.Request) accessClass {
    _, pattern := p.served.Handler(r)
    return p.class[pattern]
}
```

Two properties fall out of that one line. Precedence is decided by `net/http`
once, rather than by a second hand-written copy of its rules that could disagree
with the router — which is exactly where an allowlist springs a leak. And an
unknown pattern yields the zero value, `accessAuthenticated`: a route registered
without a declaration is gated rather than inheriting the `/` catch-all's public
class, so bypassing `mount` fails safe.

Because patterns carry methods, the class does too. `GET /api/v1/settings/theme`
is public; `PUT` and `DELETE` of the same path are not, and are now refused at the
boundary as well as by `requireAdmin` in the handler.

One route exists only to keep the boundary whole: a gated `/api/v1/` catch-all.
Without it, an `/api/v1` path that no route claims would be served — and therefore
classified — by the public `/` catch-all that serves the web UI, so a gap in the
route table would become a gap in the boundary. With it, such a path stays gated
and is answered in the API's own error envelope instead of by the file server.

Three tests hold it: `TestPublicRoutesAreExactlyTheAllowlist` compares the
resulting public set against a written-out list, so opening a route is a
reviewable diff; `TestEveryPublicAPIRouteEntryIsRegistered` catches an allowlist
entry that matches nothing, which would otherwise be silent; and
`TestUndeclaredRouteIsGated` states the fail-closed property directly.

Option 1 is rejected because it keeps the default backwards: the next route
mounted outside `/api/v1` is still open until somebody remembers a second list.
Option 2 gets the default right but keeps a path-shaped rule that still cannot
tell a read from a write, and still leaves the public set implicit in a `switch`
rather than assertable as a set.

### Consequences

- **Positive:** the public surface is one list, checked by a test. A route added
  anywhere is gated unless its author says otherwise. Read and write of the same
  path can differ, so the boundary now covers the settings writes that only
  `requireAdmin` used to refuse. `requiresAuth` and its prefix rule are gone.
- **Negative / trade-offs accepted:** classification costs one extra `ServeMux`
  lookup per request — negligible at the HTTP boundary, and nowhere near the
  processor's hot path (invariant I1). An anonymous write to a settings path now
  answers `401` rather than `403`; that is a more accurate answer (nothing was
  presented), but it is a visible change to a response code.
  `wantPublicRoutes` must be edited when the public surface legitimately changes,
  which is the intended friction.
- **Follow-ups / risks to watch:** `/metrics` stays public here, unchanged, so
  this record does not alter behaviour it was not asked to; giving it its own
  listener or an `accessOperator` class is the next step, and would mean its
  disclosure no longer depends on a proxy rule. `/api/v1/openapi.json` likewise
  stays public and would sensibly move behind the boundary when `--auth` becomes
  the default. Neither belongs in a record about how classes are declared.

## Pros and cons of the options

### Option 1 — keep the prefix rule, add a second exception list
- Good: smallest diff; no new concept.
- Bad: the default stays open outside `/api/v1`, so the next route mounted off to
  the side repeats the bug this record exists for; two lists to keep in step; the
  public set is still not assertable.

### Option 2 — invert the prefix rule
- Good: fail-closed default; small change.
- Bad: still path-shaped, so a read-public/write-admin path cannot be expressed;
  the public set stays implicit in a `switch` statement; a route can still be
  mounted somewhere the rule does not reach, as `/mcp` was.

### Option 3 — access class per mounted route (chosen)
- Good: fail closed twice over (unstated class, and undeclared pattern); the class
  is visible at the mount site; the public set is a value a test can compare;
  method-aware; reuses `deployAgentAllowed`'s shape and its reasoning.
- Bad: one more concept in the `api` package; an extra mux lookup per request.

## Links

- replaces the `requiresAuth` rule of
  [ADR-0044](0044-user-management-and-authentication-boundary.md); the
  `*Principal` boundary itself is unchanged
- generalizes the fail-closed allowlist of
  [ADR-0129](0129-remote-deployment-targets.md)
- makes ADR-0196 expressible: `/mcp` is gated by
  being a declared route rather than by a rule written for it
- relates to [ADR-0142](0142-prometheus-metrics.md) (`/metrics` is public by
  declaration now, not by omission) and
  [ADR-0043](0043-openapi-spec-and-embedded-api-explorer.md)
- the product-side concept this implements:
  [`docs/compliance/zugriffsschutz-konzept.md`](../compliance/zugriffsschutz-konzept.md), measure M1
