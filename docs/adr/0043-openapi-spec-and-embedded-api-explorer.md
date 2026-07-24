# ADR-0043: An OpenAPI spec and an embedded API explorer for the HTTP API

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas runs as a single binary that serves a JSON HTTP API under `/api/v1` and an
embedded web UI at `/` (ADR-0011). The API surface has grown well past the
Milestone S skeleton: deployments, processes, instances, drafts, forms,
projects, DMN references, tasks, messages, FEEL evaluation, runtime and stats —
roughly forty routes registered by hand in `api/server.go`.

There is today **no machine-readable description of that surface and no way to
exercise it interactively**. A developer who wants to try an endpoint has three
options: read the Go handler source, drive the BPMN Modeler UI (which only
covers the subset the UI happens to use), or hand-craft `curl` calls. The MCP
adapter (ADR-0016) exposes a curated slice of the API to agents but is not a
human-facing API explorer and deliberately does not track the full surface.

The concrete request is a **dynamic API UI where the endpoints can be tried out
directly against the running server** — in the common shorthand, a "Swagger UI".
Two things are actually being asked for and are worth separating:

1. a **contract**: a standard, machine-readable description of `/api/v1`
   (OpenAPI) that tools, clients, and tests can consume; and
2. an **explorer**: a browser UI, served by the same binary, that renders that
   contract and issues live "try it out" requests against the running API.

The question this ADR answers is *where the contract comes from* (and how it
stays honest as routes change) and *how much machinery the explorer is allowed
to pull in*, given Atlas's buildless, low-dependency posture.

## Decision drivers

- **The contract must not drift from the handlers.** A spec that is maintained
  separately from the routes it describes rots the moment someone adds an
  endpoint and forgets the spec. Whatever we choose, adding a route and
  describing it should be one change in one place, ideally enforced by a test.
- **No build step.** The web UI is deliberately buildless and self-contained —
  no bundler, no `npm` in the serving path (ADR-0012). An explorer that requires
  a Node toolchain to produce assets violates that.
- **No heavy Go dependency and no codegen toolchain.** Atlas hand-writes its
  codec and avoids large SDKs and generators (ADR-0009, ADR-0010). A spec
  generator that bolts a `go generate` CLI and struct-tag DSL onto the build is
  the same kind of dependency we have declined elsewhere.
- **Try-it-out must reach the live engine, same-origin.** The value is testing
  against real state — deploy a model, start an instance, read runtime — so the
  explorer must call the same `/api/v1` it documents, with no CORS gymnastics.
- **Vendor UI assets, don't fetch them.** Third-party UI ships as a pinned,
  vendored asset with its license, exactly as bpmn-js and form-js do
  (ADR-0011/0013), so the binary stays self-contained and offline-capable.
- **Honesty about the unauthenticated surface.** The HTTP API and the MCP
  endpoint are unauthenticated by design, with auth expected in front (ADR-0016).
  An explorer with a mutating "try it out" button must not quietly widen that
  exposure or imply the surface is safe to publish.

## Considered options

1. **Struct-tag codegen (e.g. `swaggo/swag`).** Annotate handlers with comments,
   run a generator at build time to emit `swagger.json`, serve a vendored
   Swagger UI over it.
2. **A separately hand-maintained `openapi.yaml`** checked into the repo,
   embedded and served, with a vendored Swagger UI over it.
3. **A runtime spec built from an in-code route registry (single source of
   truth), plus a vendored, buildless API explorer.** Routes are declared once
   in a small table next to the mux — method, path, summary, and request/response
   schema references — and that table both registers the `http.HandlerFunc`s and
   renders an OpenAPI 3.1 document at `/api/v1/openapi.json`. A vendored,
   self-contained explorer at `/api/docs` renders that document and issues
   same-origin live requests. A test walks the mux and the registry and fails if
   any served route is undocumented (and vice versa).
4. **Ship only the spec, no embedded UI.** Serve `/api/v1/openapi.json` and let
   developers point an external Swagger UI, Postman, or Bruno at it.

## Decision outcome

Chosen option: **"3 — a runtime spec from an in-code route registry, plus a
vendored buildless explorer"**, because it is the only option that keeps the
contract honest by construction while respecting the buildless, low-dependency
posture and giving the requested try-it-out experience.

**Contract.** The routes currently listed as bare `mux.HandleFunc(...)` lines in
`Server.Handler` become entries in a small route table — the same information
(method, pattern, handler) plus a lightweight description (summary, tags, and
references to request/response schemas). `Handler` iterates the table to
register handlers exactly as today; a sibling function iterates the same table
to emit an OpenAPI 3.1 JSON document. The document is built in-process from Go
values — no annotations parsed from comments, no generator CLI, no new
dependency — consistent with how Atlas hand-writes the things other projects pull
SDKs in for. Because registration and description read the *same* table, a route
cannot be served without appearing in the spec; a test asserts the two sets are
equal so drift fails CI rather than shipping.

**Explorer.** A single, vendored, self-contained API-doc renderer is served at
`/api/docs`, alongside the existing `/` UI and `/mcp` endpoint, and points at
`/api/v1/openapi.json`. It runs same-origin, so "try it out" issues real
requests to the live engine with no CORS configuration. The chosen renderer is
**Scalar** (`@scalar/api-reference`, the `standalone.js` browser build): a
single self-contained JS asset that exposes
`window.Scalar.createApiReference(el, { url })`, with no build step and no
runtime chunk loading. A single pinned file matches the buildless posture most
cleanly — over the multi-file `swagger-ui-dist` bundle — while still giving the
requested Swagger-style "try it out". It is a pinned asset under
`api/web/vendor/scalar/` with its MIT license, and nothing is fetched at
runtime; the explorer works offline.

**Exposure.** The explorer and the spec are part of the same unauthenticated
surface as the rest of `/api/v1` and `/mcp`; they add no new privilege, but the
try-it-out button makes the existing mutating surface (deploy, delete, cancel)
one click away. Both `/api/docs` and `/api/v1/openapi.json` are therefore
**gated behind a `--docs` flag and off by default**: an operator opts in
explicitly, and the "put auth in front before exposing publicly" guidance from
ADR-0016 applies unchanged. The vendored Scalar JS asset itself is served by the
existing file server regardless of the flag — it is public library code that
reveals nothing about the API — so only the interactive explorer shell and the
machine-readable description of the surface are gated.

### Consequences

- **Positive:** A standard, tool-consumable contract for the whole `/api/v1`
  surface, kept honest against the handlers by a test rather than by discipline.
  A browser explorer that tests endpoints against real engine state, same-origin,
  with zero client setup. No build step and no new Go or Node dependency in the
  serving path. Assets stay vendored and offline-capable. Third parties
  (generated clients, Postman/Bruno, contract tests) get the same spec for free.
- **Negative / trade-offs accepted:** Converting the flat `HandleFunc` list into
  a route table is a mechanical but non-trivial refactor of `Server.Handler`, and
  every route now carries a description payload to maintain (the drift test makes
  that maintenance mandatory, which is the point). Request/response schemas must
  be described in Go values by hand — richer than the current "no schema at all",
  but bounded and reviewable. The explorer widens how easily the unauthenticated
  mutating surface is exercised, so it is gated off by default behind `--docs`
  rather than always served like the read-mostly UI.
- **Follow-ups / risks to watch:** If finer control is wanted later, the `--docs`
  gate can grow an env-var equivalent or split spec-only from explorer-on. Keep
  the vendored renderer pinned and update it deliberately (ADR-0013 discipline).
  As Milestone 4 adds routes
  (durable deployment path, job-worker surface, queries), they land in the route
  table and appear in the spec automatically; the MCP tool set (ADR-0016) and the
  OpenAPI spec should be reviewed together so the two descriptions of the surface
  stay consistent.

## Pros and cons of the options

### Option 1 — struct-tag codegen (swaggo/swag)
- Good: annotations live next to handlers; rich schema inference from structs;
  familiar Swagger UI output.
- Bad: introduces a codegen CLI and a struct-tag DSL into the build, against the
  buildless/low-dependency posture (ADR-0010, ADR-0012); the generated
  `swagger.json` is a committed artifact that can go stale between regenerations;
  ties spec fidelity to the generator's Go-reflection quirks.

### Option 2 — hand-maintained openapi.yaml
- Good: full control of the spec; no generator; serve-and-render is trivial.
- Bad: the spec is maintained entirely separately from the routes, so it drifts
  the first time someone forgets to update it; nothing enforces that the served
  routes and the documented routes agree.

### Option 3 — runtime spec from an in-code route registry + vendored explorer (chosen)
- Good: one source of truth for registration and description; drift is a test
  failure, not a latent bug; no build step, no new dependency; same-origin
  try-it-out against the live engine; assets vendored and offline.
- Bad: up-front refactor of `Handler` into a table; schemas described by hand in
  Go; the mutating surface becomes easier to poke without new auth.

### Option 4 — spec only, no embedded UI
- Good: smallest change; no vendored UI asset; developers use their own tooling.
- Bad: does not deliver the requested "try it out right here" experience; every
  developer must wire up an external UI and point it at the server themselves.

## Links

- relates to ADR-0011 (single-binary distribution and web UI) — the HTTP API and
  embedded-asset model this extends
- relates to ADR-0012 (buildless, self-contained web UI app shell) — the
  no-build constraint on the explorer asset
- relates to ADR-0013 (vendor the bpmn-js modeler as a pinned asset) — the
  vendoring discipline the explorer asset follows
- relates to ADR-0016 (MCP server over the HTTP API) — the other description of
  this surface and the unauthenticated-exposure guidance restated here
- relates to ADR-0009 / ADR-0010 (hand-written codec, no heavy SDKs / no CGO) —
  the posture that rules out a codegen toolchain for the spec
