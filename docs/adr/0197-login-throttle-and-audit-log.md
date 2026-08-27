# ADR-0197: A throttle on the login, and a security audit trail

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

Requiring a login is now the default (ADR-0195). That makes
`/api/v1/auth/login` the door everybody comes through, and it had two things
wrong with it.

**Nothing stood in front of it.** The token bucket in `api/ratelimit.go` existed
and was used by exactly three routes — the public start-form endpoints of
ADR-0029. Password guessing was bounded by nothing but how fast bcrypt would
answer, and bcrypt answering slowly is the wrong way round: each attempt costs
the server about 100 ms of CPU and the caller one request. So the same absence is
both an online-guessing problem and a cheap way to make an unauthenticated caller
expensive. The ISDS concept records it as part of R-12 and as open point O-04.

**Nothing was written down.** Atlas's business trails are strong — every state
transition is an event, every external variable change names its actor
(ADR-0098), every manual task completion the same (ADR-0159). But who signed in,
who failed to, and who changed an account or minted a credential appeared
nowhere, and there is no HTTP access log either. The concept records that as R-13
and O-03, and the only answer it could give was M-06: the reverse proxy must
supply it. That is an answer about somebody else's software, and it cannot say
*which account* a refused attempt was against, only which URL.

The two are one problem seen twice. A throttle whose refusals are invisible
cannot be tuned, alerted on, or shown to work; a trail with nothing to say about
failed logins is missing the entry every audit opens with. And both matter more
now than they did a week ago: this is the control that everything else in the
access work rests on, and a control nobody can see working is a claim rather than
a control.

The question: **how are authentication attempts bounded, and how does an operator
find out what happened at the door?**

## Decision drivers

- **Bound guessing without becoming a lockout weapon.** Any per-account limit can
  be aimed at a victim; the design has to make that cheap to recover from.
- **Say nothing new about who exists.** The login is careful to answer
  identically for a wrong password and an unknown name. A throttle that only
  throttles real accounts undoes that.
- **One mechanism, not two.** The token bucket already exists and is covered.
- **One log stream.** An audit sink that is a separate file, endpoint or format is
  a second thing to configure, back up and forget.
- **No secret ever becomes an attribute.** An attribute is precisely what a log
  shipper extracts, indexes and keeps.
- **Signal, not volume.** A trail that records every anonymous 401 buries the
  entry that matters under every probe that finds the port.

## Considered options

For **the throttle**:

1. **Per-address bucket only.**
2. **Per-account lockout only** — N failures, then refuse for a window.
3. **Both, as two keys into the same token bucket.**
4. Progressive delay: sleep longer after each failure.

For **the trail**:

A. **A separate audit sink** — its own file or endpoint, its own format.
B. **Stable `event=` names on the existing log stream.**
C. Engine events, so the audit trail is replayable like process history.

## Decision outcome

Chosen: **option 3 for the throttle, option B for the trail.**

**The throttle** (`api/loginguard.go`) is two `rateLimiter` instances with
different keys. By address: 20 back to back, refilling at one every two seconds —
generous, because a whole office behind one NAT address is an ordinary
deployment and a limit that punishes that gets turned off. By account: 5, with a
fully spent bucket refilling over 15 minutes.

Three details carry most of the design.

*It is charged before the account is looked up*, and charged whether or not that
account exists. That is what keeps it from answering the question the login
itself refuses to answer: if only real accounts could be throttled, six refused
attempts would confirm a name. It also means a flood costs this server a map
lookup rather than a bcrypt verification, which is the CPU half of the problem.

*Both buckets are spent even when one has already refused.* An attempt turned
away by the address limit still counts against the account, so cycling addresses
does not preserve an account's budget and cycling accounts does not preserve an
address's.

*A successful login clears the account's bucket.* Somebody who mistypes twice and
then gets it right is not left one attempt from a lockout. The account limit's
real cost — that a stranger guessing at your name can lock you out — is bounded
by the same property that makes it work: the bucket refills on its own, in
minutes, with nobody's intervention.

**The trail** goes through `logging`, so it is one stream with stable `event=`
names, rendered as JSON with `--log-format=json` and shipped wherever the
operator already ships logs. `api/audit.go` holds two helpers so every line has
the same shape: `audit` at INFO for something that happened, `auditRefusal` at
WARN for something that was turned away — levelled apart so an alert can be built
on the refusals without a query to separate them. Both attach the client address
always and the acting principal when the request has one.

Ten events: `auth.login`, `auth.login_failed`, `auth.login_throttled`,
`auth.logout`, `auth.denied`, `auth.user_created`, `auth.user_updated`,
`auth.user_deleted`, `auth.password_set`, `auth.token_minted`,
`auth.token_revoked`.

Two judgements inside that list. `auth.denied` records an *authorization*
refusal — somebody signed in reaching for something they may not have — and not a
401, because logging every anonymous 401 buries the rare, meaningful line under
every unauthenticated probe on the internet. And a failed login records *why* it
failed (no such account, disabled, wrong password) in the log while the response
stays the one uniform message: the log is for the operator, the wire is what an
attacker gets to read.

`TestAuditTrailNeverCarriesASecret` drives passwords, a set-password call and a
freshly minted deploy token through the handlers and then asserts none of them —
nor a bcrypt digest prefix — appears anywhere in the output. It is a standing
guard rather than a one-time check: the next attribute somebody adds is the one
this is for.

Option 1 is rejected as incomplete: an attacker with a botnet pays nothing for a
new address, so an address limit alone never bounds how many guesses one account
takes. Option 2 is rejected for the mirror reason and because it leaves the CPU
cost unbounded. Option 4 is rejected because a sleeping handler is a held
goroutine, so the defence is itself a denial-of-service vector. Option A is
rejected as a second thing to configure and forget, for no gain — a stable event
name in one stream is already what a SIEM ingests. Option C is rejected outright:
the WAL is process history, `applyToState` must stay deterministic and
side-effect-free (invariant I4), and account administration is config data that
ADR-0044 deliberately kept off the engine.

### Consequences

- **Positive:** online guessing is bounded per address and per account without
  becoming an enumeration oracle. An unauthenticated caller can no longer make
  the server spend bcrypt time at will. O-04 is closed and O-03 substantially so:
  logins, failures, throttling, logouts, authorization refusals and the whole
  account and machine-credential lifecycle are on one machine-readable stream
  with the actor on every line. R-13's compensating measure M-06 stops being the
  only answer, and an alert can now be built on `auth.login_failed` and
  `auth.denied` rather than on proxy status codes.
- **Negative / trade-offs accepted:** a per-account limit can be aimed at
  somebody — five wrong guesses at your name and you wait, at worst, a quarter of
  an hour. That is the cost of the mechanism and it is why the lockout heals
  without an operator. The bucket map is bounded at 10 000 keys and flushed when
  it grows past that, so an attacker who can push 10 000 distinct usernames
  through resets everyone's account budget — but the address limit throttles that
  flood to hours from any one address, and the alternative is an unbounded map.
  The `429` carries no `Retry-After`, because the bucket does not track a
  deadline and a made-up one would be worse than none. Audit lines share the
  application log, so retention and volume are that log's, which is the point but
  also means an operator who does not ship logs anywhere still has nothing.
- **Follow-ups / risks to watch:** configurable password rules and MFA remain out
  (O-04's remainder, and better solved by federation — O-01). The throttle's
  numbers are constants; if a real deployment finds them wrong they should become
  flags rather than be quietly widened. Session lifecycle events (a session
  expiring, an operator ending one) belong here too once there is session
  management to emit them (O-14). Failed *token* authentication — a bad bearer
  rather than a bad password — is not recorded, and would be the natural next
  event once API tokens exist.

## Pros and cons of the options

### The throttle

**1 — per-address only**
- Good: no lockout risk at all; simplest.
- Bad: a botnet bypasses it entirely, so it never bounds guesses at one account.

**2 — per-account lockout only**
- Good: directly bounds guessing at an account.
- Bad: leaves the CPU cost of bcrypt unbounded; a single address can still flood.

**3 — both, one bucket type, two keys (chosen)**
- Good: neither axis is bypassable on its own; one mechanism already tested;
  charged before the lookup, so it is cheap and reveals nothing.
- Bad: two budgets to reason about; the account one is aimable at a victim.

**4 — progressive delay**
- Good: no hard refusal, so no lockout.
- Bad: a sleeping handler holds a goroutine, making the defence a
  denial-of-service vector of its own.

### The trail

**A — a separate audit sink**
- Good: retention and access can be set independently of application logs.
- Bad: a second sink to configure, ship, back up and forget; nothing a stable
  event name on the existing stream cannot do.

**B — stable event names on the existing stream (chosen)**
- Good: one stream, one format, already JSON-capable and already shipped; no new
  configuration surface.
- Bad: retention and volume are the application log's; an operator who ships no
  logs gets no trail.

**C — engine events**
- Good: replayable, immutable, the same machinery as process history.
- Bad: puts config data in the WAL, drags account writes onto the single-writer
  loop, and complicates `applyToState` — everything ADR-0044 decided against.

## Links

- closes O-04 and substantially closes O-03 in
  [`docs/compliance/isds-offene-punkte.md`](../compliance/isds-offene-punkte.md);
  improves R-12 and R-13 in [`isds-konzept.md`](../compliance/isds-konzept.md)
- follows ADR-0195, which is what makes the login the door
  everybody comes through
- reuses the token bucket of [ADR-0029](0029-public-process-start-links.md) and
  the stable event names of [ADR-0142](0142-prometheus-metrics.md)
- keeps identity off the engine, per
  [ADR-0044](0044-user-management-and-authentication-boundary.md)
- the product-side concept this implements:
  [`docs/compliance/zugriffsschutz-konzept.md`](../compliance/zugriffsschutz-konzept.md), measures M7 and M8
