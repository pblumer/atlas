# ADR-DRAFT: Hosted apps — user HTML/JS served from an isolated origin

- **Status:** Proposed
- **Date:** 2026-08-27
- **Deciders:** Atlas maintainers

## Context and problem statement

Three surfaces already put an Atlas process in front of a person, and none of them
is a place to put a *page*:

- **Forms** ([ADR-0028](0028-forms-and-the-tasks-app.md)) — a form-js schema
  authored in the Modeler, filed in `api/formstore.go`, read back over
  `GET /api/v1/forms/{id}` and rendered by `api/web/formviewer.js`. Declarative:
  no markup, no code, no layout beyond what the renderer offers.
- **Public start links** ([ADR-0029](0029-public-process-start-links.md)) —
  `/public/forms/{token}` serves one bundled page (`api/web/public-form.html`)
  plus `/schema` and `/start`. The token is the whole authorization; cookieless,
  rate-limited, outside `/api/v1`.
- **Scoped CORS** ([ADR-0186](0186-embed-public-forms-cross-origin.md)) — an
  operator allow-lists an origin and a page on *that* origin drives those two
  endpoints as a custom widget. `examples/account-bestellung/account-order-widget.html`
  is the worked example.

What none of them covers is the thing customers keep asking for under a different
name each time: **the public face of an application** — a branded, multi-step page
that belongs to a process application, travels with it, and is served by Atlas.
Today the only way to get one *from Atlas* is to drop the file into `api/web/` and
rebuild: that directory is compiled in with `//go:embed web` (`api/server.go:127`)
and served as the catch-all at `/` (`api/server.go:2166`). That is how
`order-to-cash-live.html`, `order-to-cash-jobs.html` and `reisebuchung-kunde.html`
exist. It is a product-release path, not a customer path — it needs the source
tree, a Go toolchain and a tagged build — and nothing an operator authors that way
survives an upgrade.

ADR-0186 met this exact question and deliberately left it open:

> A tempting adjacent idea — let Atlas *host* arbitrary user HTML/JS artifacts and
> serve them (publicly) — is deliberately **out of scope and rejected here**: JS
> served from the Atlas origin can call the authenticated API with the visitor's
> session cookie, which is stored XSS with full reach. If Atlas ever hosts user
> pages it must be from an isolated, sandboxed origin, a much larger decision.

It named the follow-up — "a sandboxed-origin static-artifact host if Atlas should
ever serve the page itself" — and this record is it.

The hazard is worth stating as mechanism rather than as a warning, because it
decides the whole shape of the answer. `setSessionCookie` (`api/auth.go:247`)
writes `atlas_session` with `Path: "/"`, `HttpOnly`, `SameSite=Lax` and no `Domain`
attribute. `HttpOnly` stops script from *reading* the cookie; it does nothing to
stop script from *using* it. A page served from the Atlas origin that calls
`fetch("/api/v1/…")` issues a request the browser attaches that session to, and the
boundary in `api/access.go` then classifies it as that visitor — admin included.
No Content-Security-Policy fixes this: `connect-src 'self'` *is* the API, and the
`sandbox`-plus-`default-src 'none'` posture that makes an uploaded SVG safe
(ADR-0148) works precisely because it forbids execution, which is the one thing an
app needs.

Two further constraints come from the shape of the product rather than from
safety. A hosted app is **design-time source**: it must travel in the curated
source tree (`api/appsource.go`, [ADR-0134](0134-git-backed-applications.md)), in
the release bundle (`api/appimport.go`, ADR-0129) and in the design-time backup
(ADR-0107), or it is a file that exists on one server and nowhere else — the
failure ADR-0134 was written against. And it must need **no build step**: ADR-0011
and ADR-0012 sell one binary and a buildless UI, so an app format that assumes npm
is an app format Atlas cannot host.

So: **what is a hosted app, how does one get into Atlas, and from where may Atlas
serve it?**

*(Naming: ADR-0012 already calls the shell's five workspaces "apps". This record
qualifies the word — a **hosted app** is always the user-authored artifact, never
Console/Modeler/Tasks/Operations/Panorama.)*

## Decision drivers

- **Origin isolation is the whole decision.** Whatever is chosen must make it
  impossible for app JS to ride the visitor's Console session, by construction and
  not by a header a later refactor can drop.
- **An app belongs to an application.** It is versioned, exported, released,
  shared and backed up with the process application it faces, on the existing
  machinery — not in a parallel store with its own lifecycle.
- **No build tool, no CDN.** Authoring must work from the Console with a text
  editor and a zip, and the served page must be self-contained (ADR-0011/0012).
- **Fail closed at the boundary.** The access classes (ADR-0199) are the model:
  a route's reach is provable by reading one short list. A new serving surface
  must not reintroduce reach-by-omission.
- **Reuse the sidecar discipline.** A new artifact kind is one `sidecar.NewStore`
  plus its guards, not hand-rolled read/write/list (AGENTS.md, `api/sidecar`).
- **Nothing touches the engine.** Design-time throughout: no event log, no
  processor, no `applyToState` (I3/I4).
- **Say what is not covered.** A slice that quietly excludes authenticated apps
  must say so out loud rather than let an operator discover it.

## Considered options

### Product scope

1. **Do nothing** — keep `api/web/` as the only hosting path and point operators at
   ADR-0186 for everything else.
2. **App artifacts, export only** — model an app as an application artifact that
   travels in the source tree and the release bundle, but let the operator host it
   on their own static host.
3. **App artifacts, hosted by Atlas** — the above, plus a serving surface.

### Where a hosted app is served from

- **(a) The Atlas origin, under `/apps/…`,** with a strict CSP.
- **(b) The same host, made opaque** with `Content-Security-Policy: sandbox
  allow-scripts allow-forms`, which puts the document in a unique origin.
- **(c) A second port** — `https://host:8081` is a different origin from
  `https://host:8080`.
- **(d) A separate host,** configured by the operator: `--apps-origin`.
- **(e) A subdomain per app** — `<app>.apps.example.com`.

## Decision outcome

Chosen: **scope option 3 with origin option (d)** — hosted apps are application
artifacts, and Atlas serves them **only** from a separate, operator-configured
origin, never from the origin the Console and `/api/v1` live on.

### What a hosted app is

A hosted app is one artifact of a process application (ADR-0034/0128): a bounded
set of static files with `index.html` at its root. The record — id, application id,
name, slug, published flag, revision, timestamps — is a `sidecar.NewStore`
(`api/hostedappstore.go`). The files live beside it under `Store.Dir()`, the
accessor that exists for exactly this ("callers that keep sibling artifacts beside
a record", `api/sidecar/store.go:80`).

Bounds are part of the format, not a later hardening pass: an extension allow-list
(`.html .css .js .mjs .json .svg .png .jpg .webp .woff2 .ico .txt .map`), 2 MiB per
file, 16 MiB and 200 files per app. Every path is validated the way the store
already validates a key — the predicate that keeps a request-supplied `"../secret"`
from addressing a file outside the directory (`api/sidecar/store.go:91`) — plus a
rejection of absolute paths, `..` segments and symlinks. Nothing is parsed,
rewritten or sanitized; the safety property is *where it is served*, not *what it
contains*.

### How one gets in

- `POST /api/v1/hosted-apps` creates the record; `GET`/`DELETE` the usual pair.
- `PUT /api/v1/hosted-apps/{id}/files/{path...}` writes one file — the path the
  Modeler's existing `code-editor.js` drives for the single-file case, which is
  what most of these are.
- `POST /api/v1/hosted-apps/{id}/import` takes a zip and replaces the file set
  atomically, refusing the whole archive if any member fails a bound. Import is
  all-or-nothing for the same reason bundle import is (ADR-0034/0129): a
  half-written app is a failure mode nobody can reason about.
- `POST /api/v1/hosted-apps/{id}/publish` flips the published flag. Only published
  apps are served, so editing is never live editing of a customer-facing page.

All of these are ordinary `/api/v1` routes: authenticated, and inside the
application's sharing scope (ADR-0071).

### Where it is served

`--apps-origin https://apps.example.com` (env `ATLAS_APPS_ORIGIN`). Unset — the
default — means **Atlas hosts nothing**, and every route behaves exactly as today.

When it is set, the origin is a wall, in both directions:

- A request whose `Host` matches the apps origin is served **only** by the hosted-app
  handler: `GET /{slug}/…` resolves within that published app, `/` lists the
  published apps or 404s. On that host there is no Console, no `/api/v1`, no
  `/public/*`, no `/mcp`, no metrics — an unmatched path is a 404, not a fallthrough.
- A request for `/apps/…` on the Atlas origin is a 404. Hosted apps are never
  reachable there.

That is a new dimension for `api/access.go`, and it is modelled the same way: the
host a route may be served on is stated at the mount site, an unstated one defaults
to the Atlas origin, and a test holds the apps-origin route set against a
written-out list — so opening a route there is a reviewable diff.

Responses carry `X-Content-Type-Options: nosniff`, a `Content-Security-Policy`
(`default-src 'self'; connect-src 'self' <atlas-origin>; frame-ancestors 'none'`,
the frame-ancestors part configurable for embedding), and never a `Set-Cookie`.

### How a hosted app talks to Atlas

Through the cookieless public surface and nothing else: `/public/forms/{token}`
and `/public/docs/{token}`, with the apps origin named in `--public-forms-cors`
(ADR-0186). This is the pairing that makes the whole thing coherent — ADR-0186
opened a narrow cookieless API for a page on someone else's origin, and a hosted
app *is* a page on another origin that Atlas happens to serve.

**Authenticated hosted apps are explicitly out of scope for this slice.** An app
that needs `/api/v1/tasks` — the shape `api/web/reisebuchung-kunde.html`
demonstrates — cannot be a hosted app yet. Saying so is the point: the honest
answer today is that such a page stays a product page in `api/web/`, or lives on
an origin where the operator runs their own auth. A token-scoped, per-app
credential is the obvious follow-up and needs its own record.

### What travels

- **Source tree** (ADR-0134): `apps/<slug>/…` verbatim, indexed in `atlas.json`
  under a new `apps` array with id, name, slug and entry path. Native content, no
  Atlas wrapper — the same rule that keeps a `.bpmn` openable in any modeler.
- **Release bundle** (ADR-0129): a new `importArtifact` kind `"hostedApp"` carrying
  the file set. It rides the existing validate-all-then-deploy-all atomicity.
- **Backup** (ADR-0107): automatic, because the store is a design-time directory.

### Invariants

Untouched. This is design-time state end to end: nothing enters the event log,
nothing affects recovery, and serving a file is I/O off the run loop. The store is
owned by the run-loop goroutine like every other sidecar (I3).

### Consequences

- **Positive:** an application can finally carry its own public face, authored in
  the Console, versioned and released with the process it fronts, with no build
  step and no second web server. The dangerous version of this feature is
  unreachable by construction rather than by policy: the Atlas origin never serves
  app files, so no CSP regression and no forgotten header can turn one into a
  session-riding page. Existing deployments are unchanged until `--apps-origin` is
  set.
- **Negative / trade-offs accepted:** hosting requires a second DNS name and, with
  ADR-0191's listener or a proxy, a certificate covering it — real operational
  cost, and the reason the feature is off by default. Hosted apps are limited to
  the public cookieless API, so the most interesting existing demo pages could not
  become hosted apps today. All apps on one origin share it, so one app's script
  can reach another's `localStorage` and DOM; per-app isolation is option (e),
  deferred. Static files only — no server-side rendering, no templating, no
  bundler.
- **Follow-ups / risks to watch:** a per-app credential for authenticated apps;
  wildcard-subdomain isolation (option e) if apps ever come from different tenants;
  a per-token CORS origin scope, which ADR-0186 already lists; guidance on the
  residual CSRF surface when the apps origin is a *sibling subdomain* of the Atlas
  origin rather than a different registrable site (see option (c) below — SameSite
  is site-scoped, not origin-scoped); and whether the Modeler should offer a
  starter app scaffolded from a process's start form.

### On the ArchiMate reading

The framing this question usually arrives in is worth pinning down, because
[ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) will have to
bind these elements. A hosted app is **not** the Application Interface. In
ArchiMate terms: the *Application Service* is the business-facing capability ("order
an account"); the *Application Interface* is the access point that exposes it —
`/public/forms/{token}`, `/api/v1`, the MCP endpoint; the hosted app is a distinct
*Application Component* that consumes that interface, and its file set is an
*Artifact* deployed on a Node. ADR-0189's first editor slice already includes all
four, and its warning applies unchanged here: an ArchiMate Application Component is
not an Atlas process application, and one may map to several of the other.

## Pros and cons of the options

### Scope 1 — Do nothing
- Good: no new surface, no new failure modes; ADR-0186 genuinely covers the widget
  case.
- Bad: leaves "the application's public face" answerable only by forking the
  repository or running a second web server, and leaves anything authored that way
  outside export, release and backup.

### Scope 2 — Export only
- Good: gets the artifact into the source tree and the release bundle with none of
  the serving risk.
- Bad: every install still needs somewhere to put the files; the single-binary
  claim (ADR-0011) picks up the same footnote ADR-0191 wrote about TLS.

### Scope 3 — Hosted (chosen)
- Good: the artifact and the serving arrive together, so the app is a first-class
  part of an application rather than a file the operator remembers to copy.
- Bad: a new serving surface to secure, document and support.

### Origin (a) — The Atlas origin under `/apps/`
- Good: zero configuration; one host, one certificate.
- Bad: this is the case ADR-0186 rejected, and CSP cannot rescue it — the cookie is
  `Path: "/"`, and the API the app needs to reach is `'self'`. Any app is stored
  XSS with the visitor's full reach.

### Origin (b) — Same host, opaque via `CSP: sandbox`
- Good: no DNS, no certificate, one header; the document really does get a unique
  origin and cannot read `document.cookie` or a same-origin response.
- Bad: rejected as the primary mechanism because the guarantee rests on subtle,
  browser-version-dependent semantics — in particular whether a *top-level*
  sandboxed document is treated as cross-site for `SameSite=Lax`, which decides
  whether a state-changing request still carries the session even though its
  response is unreadable. A separate host is a coarse boundary a reviewer can
  verify by reading one flag. And since a sandboxed app is cookieless anyway, this
  buys nothing option (d) does not.

### Origin (c) — A second port
- Good: trivially configured; the same-origin policy does include the port, so an
  app cannot read a Console response.
- Bad: **cookies are not port-scoped.** `atlas_session` set on `host:8080` is sent
  to `host:8081`, so the request still carries the session even where the response
  is blocked — read-isolation without cookie-isolation, which is the half that
  matters. Same-host also means same-site, so `SameSite=Lax` does not help.

### Origin (d) — A separate host (chosen)
- Good: a host-only cookie (no `Domain` attribute, as `setSessionCookie` writes it)
  is simply not sent to another host, so isolation holds for reads *and* for the
  request itself. One flag makes the property auditable, and one host serving
  exactly one thing keeps the boundary list short.
- Bad: DNS and certificate work for the operator; all hosted apps share one origin;
  a sibling-subdomain deployment is same-site, which leaves a residual CSRF
  question worth documenting rather than papering over.

### Origin (e) — A subdomain per app
- Good: strictest — apps are isolated from the Console *and* from each other.
- Bad: wildcard DNS and a wildcard certificate for a feature that starts with one
  or two apps per install. Deferred, and (d) is forward-compatible with it.

## Links

- answers the follow-up left open by [ADR-0186](0186-embed-public-forms-cross-origin.md)
  ("a sandboxed-origin static-artifact host if Atlas should ever serve the page
  itself"), and reuses its cookieless CORS surface as the way a hosted app talks
  back to Atlas
- builds on [ADR-0029](0029-public-process-start-links.md) (the public, token-scoped
  start route family a hosted app drives)
- builds on [ADR-0128](0128-process-applications.md) and
  [ADR-0034](0034-projects-and-artifacts.md) (the application a hosted app is an
  artifact of)
- extends [ADR-0134](0134-git-backed-applications.md) (the curated source tree gains
  `apps/<slug>/…` and an `apps` index in `atlas.json`) and ADR-0129 (the release
  bundle gains a `"hostedApp"` artifact kind)
- follows [ADR-0148](0148-org-wide-brand-logo.md) — the same question about
  attacker-influenced bytes, answered differently because a logo must never execute
  and an app must
- follows [ADR-0199](0199-route-access-classes.md) — the serving host is stated at
  the mount site and held against an allow-list, the same shape as the access class
- relates to [ADR-0191](0191-built-in-tls-listener.md) (a second origin needs a
  certificate covering it) and [ADR-0071](0071-sharing-scopes.md) (a hosted app is
  inside its application's sharing scope)
- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) —
  a hosted app is an ArchiMate Application Component and Artifact, not the
  Application Interface
