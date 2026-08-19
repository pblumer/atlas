# ADR-0150: A preview mail provider, and incidents on the live diagram

- **Status:** Accepted (amended)
- **Date:** 2026-08-19
- **Deciders:** Atlas engine team

## Context and problem statement

Two reports arrived from one install on the same afternoon, and they turned out to be
the same problem seen from two ends.

**"No mail is going out any more."** Two unrelated causes, both silent:

1. A Gmail connector's refresh token had been revoked — `invalid_grant`, "Token has
   been expired or revoked". A Google OAuth client left in *Testing* publishing status
   expires its refresh tokens after seven days, which nothing in Atlas said at the time
   the credential was configured.
2. A newly configured SMTP connector's endpoint had been written as `mx1.example.ch`,
   with no port. `validateMailConnector` checked only that the field was non-empty —
   its message said `host:port` but nothing enforced it — so the record was stored as
   typed and the endpoint went straight to `net/smtp.SendMail`, which failed with
   `dial tcp: address mx1.example.ch: missing port in address`. The failure was
   accurate and arrived hours after the mistake, in a place the person who made it was
   not looking.

**"The service task just stays open and never becomes an incident. Shouldn't that
happen automatically?"** It does, and it had: the incident was raised within
milliseconds of the send failing, exactly as ADR-0061 specifies, and sat in
`GET /api/v1/incidents` the whole time. What did not happen was anyone seeing it. The
live Operations view polls `GET /api/v1/processes/{key}/runtime` — tokens per element —
and never asks about incidents, so a parked token is drawn *pixel-identical* to one
that is legitimately waiting for a worker. The operator's reading ("the task is still
open, nothing is wrong with the engine") was the only reading the screen supported.

Behind both sits a third thing, which is why they are one ADR. The cost of mail in
Atlas is paid **before the first message**: every provider wants a submission host, or
an app registration plus an OAuth bundle in the vault, before an author can find out
whether their subject line renders or their recipient expression resolves. So the
first run of a first mail task fails on infrastructure the author was not yet thinking
about — and, per the above, fails invisibly. Mail is a primary channel for the
processes Atlas runs; "notify a human" was the archetypal connector ADR-0067 named. It
should be the easiest thing in the product to try, and it was among the hardest.

## Decision drivers

- **The first message should cost nothing.** Trying a mail task must not require
  owning a mail server or an OAuth app registration.
- **A failure must be legible where the operator already is.** An incident an operator
  has to know to go looking for is, for the person who does not know, no incident.
- **The engine is not at fault.** ADR-0061 already parks the token and raises the
  incident. This is a *boundary* and *reporting* problem: no new value type, event,
  intent, or recovery path may come out of it (I4/I6).
- **One framing, not two.** A preview that renders messages its own way would drift
  from what is really sent, and would then prove nothing.
- **Bounded work on the run loop.** The overlay is polled every 1.5 s; whatever it
  reads must stay bounded, keeping the O(elements) posture of ADR-0080.

## Considered options

1. **Document it.** Write down that SMTP wants `host:port` and that incidents live in
   the Incidents view.
2. **A "test connection" button only** on the connector form: dial, STARTTLS, AUTH —
   or fetch an OAuth token — and show the error inline.
3. **Three changes at three boundaries:** normalize the endpoint where it is typed, add
   a preview provider that delivers into an in-server outbox, and put incidents on the
   live overlay.
4. **Ship a catch-all SMTP server in the binary** (a MailHog-shaped listener) that mail
   connectors point at during development.

## Decision outcome

Chosen option: **3 — fix the boundary, the first use, and the visibility together**,
because each of the three alone leaves the reported experience intact.

**The endpoint is completed or refused where it is typed.**
`mail.NormalizeSMTPEndpoint` canonicalizes what a human writes into what `net/smtp`
dials: an optional `smtp://` / `smtps://` scheme (the latter selecting 465), a dropped
path from a pasted URL, a bracketed bare IPv6 address, and a missing port filled in
with 587 — RFC 6409 submission. What cannot be completed is rejected with a sentence
naming what was typed, including the mistake people actually make (the mailbox address
in the server field). It runs in three places, because a record can arrive from three:
`validateMailConnector` on create, `normalizeConnectorUpdate` on a PATCH — which
carries no kind and no provider and therefore never reached the create validator, the
gap the outage came through — and `mail.NewProviderClient`, so a record already on disk
in the old shape heals into a working client instead of parking one token at a time.

**A fourth provider, `preview`, needs no configuration at all.** It frames the message
with the very same `buildRFC822` the SMTP and Gmail providers send and appends it to an
in-server `Outbox` that Operations renders. It is a rehearsal, not a bypass: the sender
and recipient checks a real provider applies are applied here, so a message that
previews cleanly is one that can be sent, and what the outbox shows is the RFC 5322
bytes — headers, MIME structure, encoding — that would have gone on the wire. The
outbox is bounded (newest 200, each stored body clipped) and explicitly *not* durable:
nothing in it was ever sent, so nothing in it survives a restart. It holds its own
mutex — the one piece of shared state in the package that needs one — because a mail
worker writes it off the run loop after fsync (I2/I3) while an HTTP read serves the
view; the read deliberately does not enter `s.do`, so a browser polling the outbox can
never slow a running process down.

**The runtime overlay carries the incidents.** `runtimeResp` gains an `incidents` list
(with the *BPMN* element id resolved from the compiled index, which is what the diagram
speaks) and a per-element count, so the live view marks a parked element red, badges it
with the provider's own message, and offers the resolve next to the diagram. The two
paths cost differently and are treated differently: the single-instance view already
walks that instance's element instances, so it point-looks-up the incident of each
token it is drawing anyway — exact, no scan; the aggregate view attributes each
incident through its process instance (an incident carries no definition key) under a
scan cap and a response cap, and marks a capped page rather than reading unboundedly on
the run loop.

Option 2 is not rejected — a test-send is the obvious next step and would have caught
both of the reported failures at configuration time. It is a follow-up rather than part
of this change because it does not help the author who has no provider *to* test yet,
which is the harder half of the problem.

### Consequences

- **Positive:** A mail task can be modeled, run, and read end to end with no host, no
  credential, and no recipient — the first message costs one form field (the sender).
  The endpoint mistake that caused the outage is now impossible to store. A parked
  token is never again drawn as a healthy one, and the resolve is one click from the
  diagram instead of a view away. What preview shows is what a real provider sends,
  guaranteed by a test that compares the two byte for byte.
- **Negative / trade-offs accepted:** A fourth provider is a fourth thing to explain,
  and a "preview" connector left enabled in production would silently *not* send —
  mitigated by naming it plainly everywhere it appears, but it is a real foot-gun. The
  outbox costs memory bounded by capacity × message size, and is lost on restart. The
  aggregate overlay does one point lookup per incident and can report a capped page;
  when it does, the per-element counts are a floor rather than a total.
- **Follow-ups / risks to watch:** a test-send / connection check on the connector form
  (option 2); guided per-provider credential forms and an OAuth redirect flow, so
  nobody hand-writes a vault bundle — with the *Testing*-status refresh-token expiry
  called out where the credential is entered; a periodic credential health probe that
  raises **one** connector-level warning instead of N identical incidents across N
  instances; auto-provisioning a preview connector on a fresh install so the first mail
  task has something to point at; and an incident count in the Operations nav, so the
  visibility this ADR gives the diagram also reaches the shell.

## Amendment (2026-08-19): the check button, and the transport it forced

The follow-up this ADR named first — "a test-send / connection check on the connector
form (option 2)" — is now part of it, because the argument for deferring it did not
survive contact with the second report: the preview provider helps the author who has
no provider yet, and nothing helped the operator who *had* one and could not tell
whether it worked.

**What a check does is the provider's own question.** `mail.Prober` is a separate,
optional interface rather than a method on `Client`, so "can this be checked?" stays
answerable with "no" — a provider that cannot be checked short of sending says so
instead of returning a hollow success. Each provider answers what its configuration
actually raises: SMTP opens the session a send opens (connect, STARTTLS, AUTH) and
hangs up; a native provider acquires an access token, which is precisely the step that
fails on a revoked refresh token; preview confirms it has an outbox. `POST
/api/v1/connectors/test` takes the connector's fields rather than its id, so the form
can check what is *typed*, before it is saved — the moment a mistake is cheapest to
fix and the only moment someone is looking. An optional `to` turns the check into a
real send, the one thing that proves delivery rather than reachability. A failed check
is a 200 carrying `ok:false`: the request was served correctly and the answer is "no";
only an unusable request (a kind with nothing to check, an endpoint that cannot dial)
is a 400.

**A truthful check needed a truthful transport.** The endpoint normalization above maps
`smtps://` to port 465 — and `net/smtp.SendMail`, which the SMTP client was built on,
cannot reach a submissions-port server at all: it dials in the clear and waits for a
greeting that a TLS-first server will never send, so the send does not fail, it hangs.
A check that reported "connected" there would have been worse than no check. The
transport is therefore ours now (`dialSMTP` + `submit`): TLS from the first byte on the
submissions port, STARTTLS wherever a server offers it, AUTH after the upgrade, then
the envelope — each step naming itself, so "relay denied" points at the address it was
about. The probe and the send share the front half of it, which is what makes the check
meaningful; a check that exercised a different path could only tell you about that path.
It also closes an ADR-0079 follow-up in passing: the transport takes the caller's
context, so a send is bounded by the job that asked for it rather than by the network.
It carries ADR-0149's budget unchanged — that decision bounded the same send for a
different reason, and the two met here: the budget is the ceiling, a caller's own
deadline can only lower it, and the transport that applies it is now also the one the
check walks.

One behavior is inherited rather than chosen: `net/smtp` refuses PLAIN auth on an
unencrypted connection (outside localhost) rather than putting a password on the wire.
That is right, but "unencrypted connection" says nothing about what to do, so a check
against such a server answers with what it means — no STARTTLS offered, use 587 or 465.

- **Positive:** both failures from the original reports — a revoked Gmail refresh token
  and an SMTP endpoint that could not dial — are now answerable in a second, at the
  form, by the person who typed them. A submissions-port (465) connector works at all,
  which it did not before.
- **Negative / trade-offs accepted:** the send path is our code rather than the standard
  library's, so its correctness is now our problem — covered by tests that speak SMTP to
  a real socket rather than substituting the seam. The check is not admin-gated, matching
  the rest of the connector API: anyone who can configure a connector can already send
  through it from a process.
- **Follow-ups:** the remaining items above are unchanged, and the check makes the first
  of them cheaper — a periodic credential health probe is this same `Prober` on a timer.

## Pros and cons of the options

### Option 1 — document it
- Good: no code, no new surface.
- Bad: the two failures were both *silent*, and documentation is read by people who
  already suspect there is something to read. It answers neither report.

### Option 2 — a test-send button only
- Good: catches both reported failures at the moment of configuration, and keeps
  working for providers Atlas will never emulate.
- Bad: needs a provider to exist before it helps, so the first-use cost stays exactly
  where it was; and it does nothing for the invisible incident.

### Option 3 — normalize + preview provider + incidents on the overlay
- Good: addresses the boundary (what is stored), the first use (what it costs to try),
  and the visibility (what a failure looks like) — the three points the two reports
  actually touched; rides the existing `Client` seam and the existing incident model,
  so no engine concept is added.
- Bad: three changes in one decision; a preview provider is a new operational mode that
  can be misconfigured into production.

### Option 4 — a catch-all SMTP server in the binary
- Good: exercises the real SMTP path, including auth and TLS negotiation.
- Bad: a listening mail server inside a workflow engine is a security surface and an
  operational question ("which port, exposed to whom?") no one asked for; it also still
  requires configuring a connector to point at it, so the first-use cost barely moves.
  Against the single-binary posture's restraint (ADR-0011).

## Links

- relates to ADR-0061 (incidents and the resolve loop — unchanged; this makes them visible)
- relates to ADR-0079 (the outbound mail connector and the `Client` seam this rides)
- relates to ADR-0093 (native mail providers and the vault credential bundle)
- relates to ADR-0080 (bounded, O(elements) runtime overlay reads)
- relates to ADR-0041 (managed connectors and the secret model)
- relates to ADR-0148 (untrusted markup is rendered sandboxed, never inlined)
- relates to ADR-0149 (the outbound call budget this transport applies)
