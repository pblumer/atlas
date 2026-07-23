# ADR-0029: Public process start via a published form link

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The reference modeler's start-event panel has a **Publication** section: link a
form to a start event, flip **Public access**, and anyone with the resulting web
link can start an instance by filling in that form — no account, no API client.
It is the "public intake form" pattern: a support request, a signup, an expense
submission, kicked off by an unauthenticated visitor.

Atlas can start instances via the HTTP API and the Modeler's Deploy & run, both of
which assume a trusted caller. Publication is different in kind: it deliberately
exposes an **unauthenticated, public** entry point that mints a real process
instance. That is a security decision, not just a UI toggle, so it deserves its
own ADR rather than riding along with forms (ADR-0028).

The question: **how does Atlas expose a public, unauthenticated start endpoint
scoped to exactly one start form**, without turning the whole API into an open
door and without violating any engine invariant?

## Decision drivers

- **Least authority.** A public link must start **one** specific process via
  **one** specific start form and nothing else. It must not read state, list
  instances, or reach any other API.
- **Explicit, revocable opt-in.** Public access is off by default, enabled per
  start event by the author, and revocable, killing the link.
- **No new engine trust boundary.** Starting an instance already funnels through
  the single-writer run loop (ADR-0002/ADR-0016); the public endpoint reuses that
  path — it changes *who may call*, not *how the engine is touched*.
- **Durable before visible (I2).** A public start is an ordinary instance
  creation: variables from the form are applied through the normal
  fsync→commit→side-effect ordering.

## Considered options

1. **A scoped public endpoint** — `POST /public/forms/{token}/start` served under
   a separate, unauthenticated route prefix, where `{token}` is an opaque,
   revocable handle bound to one process + start form. It accepts only the form's
   fields, validated against the form schema, and does nothing else.
2. **Reuse the main API** with an "allow anonymous" flag on the start route.
3. **No public start** — publication is a non-goal; humans start via the Tasks app
   (authenticated) only.

## Decision outcome

Chosen option: **Option 1 — a separate, narrowly scoped public route.** Enabling
publication on a start event mints an opaque token bound to `(process id, start
form id)`. The public route accepts a submission for that token, validates it
against the form schema (ADR-0028), and starts the instance through the same
single-writer path every other start uses. The route exposes nothing else: no
reads, no enumeration, no other process. Revoking publication invalidates the
token. The endpoint is rate-limitable and, like `/mcp` (ADR-0016), is expected to
sit behind a reverse proxy when internet-facing.

Option 2 is rejected because bolting "anonymous" onto the trusted API blurs the
trust boundary — one misconfigured flag exposes far more than a start form. Option
3 forecloses a genuinely useful pattern (public intake) that a workflow engine is
well placed to serve; we keep it opt-in and scoped instead of refusing it.

### Consequences

- **Positive:** A public link does exactly one thing; the trusted API keeps its
  authentication assumptions; the engine path is unchanged (only the caller
  differs); opt-in and revocable by design.
- **Negative / trade-offs accepted:** A public write endpoint is abuse-exposed —
  it needs rate limiting, payload caps, and spam/DoS thought before it ships;
  operators must understand it is unauthenticated by design (documented like the
  `/mcp` caveat). It depends on forms (ADR-0028) existing first.
- **Follow-ups / risks to watch:** Define token format and storage; rate limiting
  and CAPTCHA-style abuse mitigation; whether a public submission may carry
  attachments; audit logging of public starts.

## Links

- depends on ADR-0028 (forms and user tasks — a start form is what gets published)
- security posture mirrors ADR-0016 (unauthenticated endpoint, front with a proxy)
- relates to ADR-0002 (single-writer path the start reuses)
