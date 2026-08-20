# ADR-0149: A bounded outbound-call budget for every connector

- **Status:** Accepted (amended 2026-08-19 — implicit-TLS submission; see the amendment note below)
- **Date:** 2026-08-19
- **Deciders:** Atlas maintainers

> **Amendment (2026-08-19): implicit-TLS (SMTPS) submission.** Bounding the SMTP
> send surfaced a second, older gap. `smtp.SendMail` always opens a *plaintext*
> connection and upgrades it with STARTTLS, so an endpoint on the SMTPS port —
> which expects a TLS handshake as its first byte (RFC 8314) — could never work:
> the client waited for a greeting the server would never send while the server
> waited for a ClientHello. Under `SendMail` that hung forever; under the budget it
> merely timed out (`read tcp …:465: i/o timeout`) — visible, but still undeliverable.
>
> `sendMailOver` therefore chooses the transport: an endpoint on port 465 is dialled
> with `tls.DialWithDialer` and skips STARTTLS (the session is already encrypted);
> everything else keeps the previous plaintext-plus-STARTTLS path. The choice is by
> port because the connector configuration carries no TLS mode and 465 is registered
> for exactly this; it is isolated in `usesImplicitTLS` so an explicit per-connector
> override — the natural follow-up — changes only that function's caller.

## Context and problem statement

The single-binary server drives connector jobs **synchronously on the run-loop
goroutine**. `job.Runner.Drive()` is called from every mutating handler (inside
`s.do`) and, unconditionally, once per second by the timer scheduler
(`api/server.go`, `go s.timerScheduler(time.Second)`). Every network connector's
worker — mail, REST, SharePoint, Remedy, Clio, temis, web scrape — plus the DMN
model resolver executes there.

That goroutine is the partition's single writer (invariant I3), and therefore the
only goroutine that can serve *any* handler's `s.do` closure. So an outbound call
made by a connector holds the entire engine for as long as it runs.

Until now those calls were unbounded. Every connector used `http.DefaultClient`,
whose zero `Timeout` means "wait forever", and the SMTP client used
`net/smtp.SendMail`, which dials and converses with no deadline at all (a
long-standing `TODO` in `connector/mail/client.go`).

This is not a hypothetical. A production instance wedged exactly this way: a mail
connector whose OAuth token endpoint stopped answering parked the run loop
indefinitely. The failure mode is especially nasty because the instance still
*looks* alive — `GET /api/v1/info` needs no run loop and keeps returning 200,
while `/stats`, the Console, and every other loop-touching request hang forever.
Nothing recovers it but a restart.

The question: how do we guarantee that a misbehaving third-party host can never
again stall the whole engine?

## Decision drivers

- **The single writer must stay available.** I3 makes one goroutine authoritative;
  that is only safe if nothing it does can block without bound.
- **Failure should be local and visible.** A bad host should stall *its* process
  instance — as a retry and then an incident (ADR-0061) — not the instance.
- **One rule, applied everywhere.** A per-connector convention that must be
  remembered will be forgotten by the next connector.
- **Regression-proof.** The hazard is a single innocuous-looking identifier
  (`http.DefaultClient`); nothing about it warns the author.
- **Don't break legitimate calls.** The budget must comfortably fit a normal API
  round trip.

## Considered options

1. **Per-connector timeouts, configured by the operator.** Each connector grows a
   timeout field in its managed configuration.
2. **One shared default budget applied by every connector**, with a shared helper
   and a test that enforces its use.
3. **Run connector workers off the run-loop goroutine.** Make the job runner
   asynchronous so a blocking call cannot hold the writer at all.

## Decision outcome

Chosen option: **Option 2 — a shared, bounded call budget every connector uses**,
implemented as `connector/nettimeout` with `Default = 10s` and a
`nettimeout.HTTPClient()` constructor, plus an AST-based drift test that fails the
build if any code under `connector/` or `dmn/` reaches for `http.DefaultClient`.

Specifically:

- **`nettimeout.Default` is 10 seconds**, covering a whole exchange (connect, TLS
  handshake, request, response body) rather than any single phase. It is generous
  for the calls connectors actually make — a token grant, a REST POST, a message
  send — while capping the worst-case engine stall at ten seconds.
- **Every HTTP connector** builds its client with `nettimeout.HTTPClient()`.
- **SMTP is bounded explicitly.** `http.Client.Timeout` cannot help a raw TCP
  conversation, so `connector/mail` replaces `smtp.SendMail` with
  `sendMailBounded`: the same sequence (greeting, STARTTLS when offered, AUTH when
  supplied, envelope, data) over a connection created with a dial timeout and an
  absolute `SetDeadline`, which bounds every later read and write too.
- **A drift test enforces it.** `TestNoUnboundedHTTPClientInConnectors` parses
  every non-test file under `connector/` and `dmn/` and fails on an
  `http.DefaultClient` selector. It parses to an AST rather than grepping, so
  prose mentioning the identifier does not trip it.

Option 3 is the deeper fix and remains the right long-term direction, but it
changes the engine's concurrency model — the very thing I3 constrains — and would
be a large, risky change to make while instances are wedging today. The budget is
cheap, total, and does not foreclose it. Option 1 is additive later: a
per-connector override can layer on top of the default, which is why the
constructors return a fresh client rather than a shared one.

### Consequences

- **Positive:** No third-party host can stall the engine for more than the budget.
  A hung host now fails its job, retries, and raises an incident — visible in the
  Incidents view and scoped to one instance. The rule is one identifier, enforced
  mechanically, so a new connector cannot silently reintroduce the hazard.
- **Negative / trade-offs accepted:**
  - A legitimately slow endpoint (>10s) now fails where it previously succeeded.
    That is the intended trade: a call that slow has no business holding the single
    writer, and the answer is an asynchronous design, not a longer stall.
  - The budget is global, not per connector, until option 1 lands.
  - `sendMailBounded` re-implements `smtp.SendMail`'s sequence using the exported
    `net/smtp` API, so it must track that flow if it ever changes.
- **Follow-ups / risks to watch:** per-connector configurable timeouts (ADR-0067 /
  0106 / 0118 already note these as follow-ups); and, longer term, moving connector
  execution off the run-loop goroutine (option 3), which would turn the budget from
  a safety net into an ordinary policy.

## Pros and cons of the options

### Option 1 — per-connector configured timeouts
- Good: operators tune each integration to its real latency.
- Bad: does not fix anything by itself — an unconfigured connector still defaults
  to unbounded; spreads the safety decision across configuration.

### Option 2 — one shared budget, mechanically enforced (chosen)
- Good: closes the hazard everywhere at once, in one small package; enforced by a
  test rather than by discipline; leaves both other options open.
- Bad: one number for very different integrations; a genuinely slow endpoint must
  be redesigned rather than configured.

### Option 3 — connector workers off the run loop
- Good: removes the hazard structurally — a blocking call could not hold the
  writer no matter how long it ran.
- Bad: changes the engine's concurrency model and the job runner's contract; large
  and risky next to a one-line-per-connector fix; still wants a timeout anyway.

## Links

- protects invariant I3 (single writer) — see [invariants](../architecture/invariants.md)
- failure surfaces through ADR-0061 (job retries and incidents)
- bounds the connectors of ADR-0036 (clio), ADR-0067 (REST), ADR-0079/0093 (mail),
  ADR-0106 (Remedy), ADR-0118 (web scrape), and the DMN resolver of ADR-0014/0034
