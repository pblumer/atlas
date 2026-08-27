# ADR-DRAFT: The Prometheus exposition moves behind the boundary

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

`/metrics` has been served without authentication since ADR-0142, beside
`/healthz` and `/readyz` and for the same stated reason: a scrape carries no
session, so gating it would silently break monitoring the moment `--auth` was
turned on. The deployment guidance was the one `/mcp` also carried — put a
reverse proxy in front of it.

Everything else in that sentence has since changed. `/mcp` is gated
(ADR-draft-authenticated-mcp-transport). Which routes are public is a declared
class per route rather than a side effect of where they were mounted
(ADR-draft-route-access-classes). A login is the default
(ADR-draft-auth-on-by-default). And a machine can now hold a named, scoped,
expiring, revocable credential (ADR-draft-api-tokens) — which is precisely what
ADR-0142 lacked when it decided a scraper could not present one.

So `/metrics` is the last route in Atlas whose protection depends on somebody
else's configuration. That is the whole of the problem, and it is worth being
plain about how much it is and is not: **the payoff here is structural, not
confidential.** The exposition carries operational telemetry — instance and job
counts, batch latencies, queue depth. No process variables, no business data.
Leaving it open is nothing like leaving `/mcp` open, and this record should not
pretend otherwise. What it buys is that the sentence "no interface is reachable
without a credential" becomes true without a footnote, and that R-08 in the ISDS
concept stops being yellow for one route.

The question: **should the exposition require a credential, now that a scraper
can hold one?**

## Decision drivers

- **No route's protection should depend on a proxy rule.** That is the claim the
  rest of this work makes; one exception makes it a claim with a footnote.
- **Monitoring must keep working**, and the change to make it keep working must be
  small enough that an operator makes it rather than turns the exposition off.
- **A scraper is a machine.** It should authenticate the way every other machine
  now does, not through a mechanism of its own.
- **The probes are not this.** `/healthz` and `/readyz` must stay open: a
  readiness probe that needs a credential does not work in the incident it exists
  for.

## Considered options

1. **Leave it public**, and keep the proxy guidance.
2. **A separate listener** (`--metrics-addr`), so the exposition can be bound
   somewhere only the monitoring network reaches.
3. **Gate it like every other route**, and give a scraper an API token scoped to
   it.
4. Gate it behind a new flag, off by default.

## Decision outcome

Chosen: **option 3.**

`/metrics` is mounted with `accessAuthenticated` instead of `accessPublic`, and a
new API-token scope, `metrics`, allows exactly one pattern: `GET /metrics`. That
is the narrowest scope Atlas has, and the easiest to hand out — a scraper needs
exactly one GET, forever.

Three consequences fall out of using the machinery that already exists rather
than building for this case. A `worker`-scoped token scraping is refused, and a
`metrics`-scoped token reaching for `/api/v1/instances` is refused, both by the
same check that confines every other machine credential. A signed-in person still
reaches the exposition, because they are authenticated and the numbers are
operational data rather than a secret kept from users. And the probes are
untouched, because their class was always a separate decision from this one.

The scope also demonstrates something about the mechanism that was implicit
before: a scope is a set of *mounted patterns*, not of `/api/v1` operations.
`/metrics` is mounted beside the probes and is in no route table, and the scope
covers it anyway. The test that guards scope allowlists against typos was
checking the `/api/v1` table; it now checks everything the server mounted, which
is what it should have compared against all along.

Option 1 is rejected because it is the footnote. Option 2 is rejected as the
answer to a question that is no longer being asked: a second listener existed as
an idea only because a scraper could not authenticate, and it substitutes one
operator responsibility (a proxy rule) for another (a bind address) while adding
a second `http.Server` with its own lifecycle and shutdown. An operator who wants
the exposition on a separate network path still has a firewall and a proxy, and
now has a credential as well. Option 4 is the shape ADR-draft-auth-on-by-default
argued against: a default that the documentation tells you to change.

### Consequences

- **Positive:** every route Atlas serves is now either authenticated or on a
  written-out public list with a stated reason, and that list is four probes and
  share links, the login screen's own reads, and the UI. R-08 goes to green. A
  scrape credential is named, scoped to one route, expiring and revocable like any
  other, and its use is attributable.
- **Negative / trade-offs accepted:** **this breaks every existing scrape config.**
  A Prometheus job needs `authorization: { credentials: <token> }` added, which is
  two lines, but it is two lines in a file that has to be found. It is also the
  smallest payoff of any measure in this line of work, as said above — an operator
  who judges the telemetry uninteresting and the change unwelcome can still run
  `--metrics=false`, or a person's session reaches it unchanged. The exposition
  itself is unmodified: this is a boundary change, not a metrics change.
- **Follow-ups / risks to watch:** a scrape that fails on `401` is a monitoring
  outage that looks like a healthy server, so the release note has to be read
  before upgrading rather than after. `auth.denied` fires on a refused scrape,
  which is the signal that says so — and a failed *token* authentication (an
  expired scrape credential) is still not its own audit event, which is where that
  would be most useful. Nothing else about `/metrics` changes: it is still on the
  main port, still `--metrics=false`-able, still not traced.

## Pros and cons of the options

### 1 — leave it public
- Good: nothing breaks; the disclosure is genuinely small.
- Bad: keeps one route whose protection is a proxy rule, so the claim the rest of
  this work makes needs a footnote, and R-08 stays yellow for it.

### 2 — a separate listener
- Good: an operator can put the exposition on a network path of its own.
- Bad: answers a question that API tokens already answered; a second
  `http.Server`, its lifecycle and its shutdown, to move one responsibility from
  the proxy to a bind address; and it still leaves the port itself unauthenticated
  for anything that reaches it.

### 3 — gate it, with a scope for scrapers (chosen)
- Good: one mechanism for every machine; the narrowest scope in the system;
  nothing new to build; the public list becomes short enough to read at a glance.
- Bad: breaks existing scrape configs, for the smallest security payoff of the
  set.

### 4 — a flag, off by default
- Good: nobody's monitoring breaks.
- Bad: the exact shape ADR-draft-auth-on-by-default rejected — a default that
  every document tells you to change is not a default.

## Links

- revises the exposition's posture from [ADR-0142](0142-prometheus-metrics.md);
  what it says about *what* is exposed and at what cost is unchanged
- completes the follow-up named in ADR-draft-route-access-classes, which left
  `/metrics` public deliberately so that record changed no behaviour it was not
  asked to
- possible only because of ADR-draft-api-tokens: the credential a scraper
  presents, and the scope that confines it
- closes O-07 in
  [`docs/compliance/isds-offene-punkte.md`](../compliance/isds-offene-punkte.md)
  and takes R-08 in [`isds-konzept.md`](../compliance/isds-konzept.md) to green
- the product-side concept this implements:
  [`docs/compliance/zugriffsschutz-konzept.md`](../compliance/zugriffsschutz-konzept.md), measure M6
