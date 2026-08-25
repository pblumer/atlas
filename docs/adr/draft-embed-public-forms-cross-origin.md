# ADR-DRAFT: Embedding a public start form cross-origin (scoped CORS)

- **Status:** Proposed
- **Date:** 2026-08-25
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0029 gave Atlas a public, unauthenticated way to start a process from a start
form: a revocable token mints the route family `/public/forms/{token}` — a bundled
HTML page, a `/schema` read, and a `/start` submission — living outside the `/api/v1`
auth surface. A visitor with the link fills the form and starts an instance, no
account required.

What that does not cover is **embedding the form in someone else's site**. A team
wants an "order an account" form on their own marketing page or intranet, styled to
match, submitting into Atlas. Two shapes of embedding exist:

1. **iframe** the bundled `/public/forms/{token}` page. This already works — nothing
   sets `X-Frame-Options`, so the page can be framed. But it is the generic form-js
   renderer, and an iframe is awkward to size and style.
2. **A custom widget** on the external origin that renders its own (nicer, dynamic)
   form and talks to Atlas through the two public JSON endpoints. This is what most
   teams actually want — and it is **blocked by the browser**: a cross-origin
   `fetch` of `/schema`, and especially a `POST /start` of `application/json` (a
   preflighted request), fail because Atlas sends no CORS headers.

So the question: **how do we let a start form be embedded as a custom cross-origin
widget, without widening Atlas's attack surface or letting arbitrary sites reach the
authenticated API?**

A tempting adjacent idea — let Atlas *host* arbitrary user HTML/JS artifacts and
serve them (publicly) — is deliberately **out of scope and rejected here**: JS served
from the Atlas origin can call the authenticated API with the visitor's session
cookie, which is stored XSS with full reach. If Atlas ever hosts user pages it must
be from an isolated, sandboxed origin, a much larger decision. This ADR keeps the
page on the *customer's* origin and opens only a narrow, cookieless API to it.

## Decision drivers

- **Open only the cookieless surface.** The `/public/forms` endpoints carry no
  session and already gate themselves (token-scoped, rate-limited, payload-capped,
  ADR-0029). CORS on *them* grants a cross-origin caller nothing a visitor to the
  public link lacks. The authenticated `/api/v1` surface must never be CORS-enabled.
- **Opt-in, zero blast radius.** Existing deployments must be unchanged until an
  operator asks for embedding, mirroring how `--auth` (ADR-0044) and `--docs`
  (ADR-0043) gate their features.
- **No credentials, ever.** `Access-Control-Allow-Credentials` is never sent, so even
  a permissive origin cannot ride a cookie into anything.
- **Don't touch the invariants.** CORS is a pure HTTP-header concern — no engine, WAL,
  processor, or `applyToState` contact.

## Decision outcome

Add an **operator-configured origin allow-list** that enables CORS on **exactly** the
`/public/forms` schema and start endpoints, and nowhere else.

- A new server option `WithPublicFormsCORS(origins []string)`, wired from the flag
  `--public-forms-cors` / env `ATLAS_PUBLIC_FORMS_CORS_ORIGINS` (comma-separated).
  Empty (the default) sends no CORS headers — cross-origin embedding stays off.
- When a request's `Origin` matches an allow-listed entry, the schema and start
  responses carry `Access-Control-Allow-Origin: <origin>` (echoed, with `Vary:
  Origin`), plus `Access-Control-Allow-Methods: GET, POST, OPTIONS` and
  `Access-Control-Allow-Headers: Content-Type`. A registered `OPTIONS` handler
  answers the `POST /start` preflight with those headers and `204`.
- The sentinel `"*"` allows any origin, but still **echoes the caller** rather than a
  literal `*` and still sends no credentials — "embeddable anywhere", with the same
  cookieless guarantee.
- `Access-Control-Allow-Credentials` is never set. A disallowed origin (or a server
  with no allow-list) simply receives no CORS headers; the request still succeeds
  server-side, but the browser withholds the cross-origin read — the safe default.

Enforcement is a two-line addition at the top of `handlePublicFormSchema` and
`handlePublicFormStart` (`setPublicCORS`) plus one `OPTIONS` route; the allow-list is
read from a field set once at construction (`publicCORSOrigins`), never mutated.

## Considered options

1. **iframe only (do nothing).** Zero code; but only the generic bundled page, and
   iframes are hard to style/size. Rejected as the *sole* answer — it doesn't serve
   the custom-widget case — but it remains available and is the right choice when no
   custom form is needed.
2. **Scoped CORS allow-list on the public endpoints (chosen).** Opens the custom-widget
   path with a small, auditable surface; opt-in; cookieless; never touches `/api/v1`.
3. **Blanket CORS (`*`) on the whole server.** Rejected: it would expose the
   authenticated API to every origin; the whole point is to open *only* the public,
   cookieless endpoints.
4. **Host user HTML/JS artifacts on the Atlas origin.** Rejected here: arbitrary JS on
   the app origin is stored XSS against the authenticated session. If ever done, it
   must be from a sandboxed, separate origin — a distinct, larger ADR. The
   host-anywhere widget + this narrow API is the isolation-preserving alternative.

## Consequences

- **Positive:** a start form embeds as a branded, cross-origin widget with one opt-in
  flag; the opened surface is exactly the already-public, cookieless endpoints;
  `/api/v1` and the session are untouched; no credentials are ever exposed; existing
  deployments are unaffected until configured; no invariant is touched.
- **Negative / trade-offs accepted:** an allow-listed (or `*`) origin can drive the
  public *start* endpoint cross-origin — but that is the same capability the public
  link already grants anyone, now reachable from an embedding page; the rate limiter
  (per-IP) is the abuse control, unchanged. The allow-list is process-global, not
  per-token; a finer per-link origin scope is a possible follow-up if needed.
- **Follow-ups:** per-token origin scoping; a sandboxed-origin static-artifact host if
  Atlas should ever serve the page itself; a small snippet/helper documenting the
  widget wiring.

## Links

- builds on [ADR-0029](0029-public-process-start-links.md) (the public start link and
  the `/public/forms/{token}` route family this opens cross-origin)
- gated opt-in in the spirit of [ADR-0044](0044-user-management-and-authentication-boundary.md)
  (`--auth`) and [ADR-0043](0043-openapi-spec-and-embedded-api-explorer.md) (`--docs`)
- relates to [ADR-0128](0128-process-applications.md) (an application's public order
  form is a natural consumer of this)
