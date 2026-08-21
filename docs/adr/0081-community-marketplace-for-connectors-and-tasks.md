# ADR-0081: A community marketplace for connectors, service tasks, and script tasks

- **Status:** Proposed
- **Date:** 2026-07-29
- **Deciders:** Atlas maintainers

> **Terminology note (2026-08-21).** The area this record calls the *marketplace*
> is now called the **Repository** throughout Atlas: the Modeler navigation entry,
> the routes under `/api/v1/repository/`, and the `<data>/repository` directory.
> Only the name changed — every decision below still holds. This record is left in
> its original wording as the dated account of the decision.

## Context and problem statement

Atlas can now *package* reusable building blocks, but it cannot *share* them
beyond a single server. Three mechanisms already exist and each stops at the
edge of the local install:

- **Element templates** (ADR-0027, *Proposed*): a connector author ships a
  bpmn.io `element-templates` JSON document; the server serves a catalog from a
  directory under the data dir. This is the packaging *format*, but its catalog
  is populated by hand-dropping files — ADR-0027 explicitly leaves "how a catalog
  is populated (bundled defaults vs. user-dropped files)" as an open follow-up.
- **The service-task connector catalog** (ADR-0067, *Accepted*): a data-driven
  list of connector *kinds* in the modeler, so "adding a kind is a data entry".
  But the list is compiled into the binary; there is no contribution path.
- **Polyglot script tasks** (ADR-0047, *Accepted*): PowerShell / Python / JS
  tasks run through the job path. A script lives inside one process model; there
  is **no** notion of a script task as a shareable, reusable artifact at all.

Separately, **sharing scopes** (ADR-0071, *Accepted*) added `private`/`shared`
access boundaries — but deliberately *only* design-time, *only* project-internal,
*only* between identified users on **one** server under `--auth`. It explicitly
lists "org/public visibility" as an unshipped follow-up. So Atlas has
*authoring-collaboration* but no *distribution*.

The user's goal is broader than any of these: make **community sharing** of
connectors, service tasks, and script tasks **extremely easy and genuinely
popular** — build a thing once, publish it, and let anyone discover and install
it in one action. That is a distribution and discovery concern that none of the
existing ADRs decide.

The question this ADR answers: **should Atlas have a marketplace for reusable
task/connector artifacts, what is the one shareable unit, where do artifacts
come from, and how do we keep untrusted shared content safe — without touching
the six engine invariants?**

## Decision drivers

- **"Extremely easy" is the point.** The request is explicitly about adoption and
  ease, not a completionist feature. The MVP must make *publish → discover →
  install* a few clicks, or it will not be popular. Discovery UX is a first-class
  driver, not a follow-up.
- **Reuse the packaging we already have.** ADR-0027's element-templates JSON is
  an established, interoperable format the vendored toolkit understands. A
  marketplace should distribute *that*, not invent a rival package schema.
- **Design-time only; do not touch the invariants.** Like templates (ADR-0027),
  projects (ADR-0034), users (ADR-0044), and sharing scopes (ADR-0071), a
  marketplace artifact is deploy-time/config data. Installing one only ever
  writes a catalog entry / template the compiler already accepts (ADR-0027's
  "sugar over properties, never a way around the gate"). It must never reach the
  WAL, the processor, or `applyToState`. The six invariants stay untouched.
- **Trust is the headline risk, and it is asymmetric across the three kinds.**
  A connector template is *data* (properties bound to extensions the engine
  already runs). A **script task carries executable code** — ADR-0047 calls
  arbitrary interpreter execution "the largest new attack surface Atlas would
  gain". Distributing runnable code from strangers multiplies that surface. The
  marketplace must treat a shared script fundamentally more carefully than a
  shared connector template.
- **Secrets never travel in a shared artifact.** ADR-0036/0067 already forbid
  credentials in a model (a URL is fine, a token is not), and ADR-0041/0069 keep
  secrets in the server-side vault. A published artifact is the most-shared thing
  Atlas has; the "no secret in the artifact" rule is absolute here.
- **Smallest honest slice, but don't box in the trajectory.** Ship one coherent
  vertical (publish/discover/install for connector templates from a curated
  source), while leaving the expensive-to-change shapes open: a real remote
  registry, script-task distribution, ratings/popularity, and signing.
- **One shareable unit, not three.** A marketplace with separate stories for
  "connectors" vs "service tasks" vs "script tasks" is three things to learn.
  There should be **one** package concept that can *carry* any of the three.

## Considered options

For **the one shareable unit (the "package")**:

1. **The element-template JSON is the package (extended with a manifest).** A
   marketplace artifact is an element-template document (ADR-0027) plus a small
   manifest (id, semantic version, author, description, kind, engine-version
   compatibility, checksum). A connector *is* already a template; a **script
   task** is expressed as a template that carries the `atlas:` script extension
   (ADR-0047's language marker + source) as its bound properties. One format,
   three uses.
2. **A new Atlas-specific "package" bundle format** (a zip/JSON envelope wrapping
   arbitrary artifact types) designed for the marketplace from scratch.
3. **Three separate distribution stories**, one per task kind.

For **where artifacts come from (the source)**:

A. **A local, curated, bundled catalog only** — ship a set of vetted templates in
   the binary/data dir; "install" = enable one. No network. (Closest to ADR-0027
   as written.)
B. **A single Atlas-hosted remote registry** the server fetches from over HTTPS,
   with the local catalog (ADR-0027) as the install target/cache.
C. **Federated/self-hostable registries** — a registry is a URL serving a
   signed index; an operator configures one or more (including an internal one).

For **the trust model on installed artifacts**:

- **i.** *Data-only artifacts install directly; code-bearing artifacts are gated.*
  A connector template (pure property bindings the compiler already accepts)
  installs like any ADR-0027 template. A **script-carrying** artifact is
  quarantined: it is imported as an editable draft the user must read and
  explicitly deploy — never auto-enabled — and (later) requires the script worker
  to be enabled at all (ADR-0047 opt-out CLI). Provenance (author, checksum, and
  later signature) is shown at install time.
- **ii.** *Treat all artifacts identically* — install any package the same way.
- **iii.** *Curated-only* — nothing installs that a maintainer has not vetted
  (this is option A's trust model, listed here for the enforcement axis).

For **discovery / "make it popular"**:

- **x.** A searchable in-Modeler gallery (name, description, icon, kind filter),
  reusing ADR-0067's catalog rendering, with a thin popularity signal
  (install count) once a remote source exists — deliberately minimal but present.
- **y.** No dedicated discovery surface; browse a flat list (status quo of a
  file catalog).

## Decision outcome

Chosen: **the element-template-plus-manifest package (option 1), sourced first
from a curated local/bundled catalog and shaped from day one to fetch from a
remote registry (A now, on a path to B/C), with the data-vs-code trust split
(i) and a searchable in-Modeler gallery (x).**

- **One package, three uses.** A marketplace artifact is an ADR-0027
  element-template document plus a **manifest** (`id`, `version` [semver],
  `author`, `title`, `description`, `kind` ∈ {`connector`, `service-task`,
  `script-task`}, `engineCompat`, `checksum`, and later `signature`). A connector
  and a service task are template property-bindings the compiler already runs
  (ADR-0027/0067); a **script task** is the same envelope carrying the ADR-0047
  `atlas:` language marker + script source as its bound properties. Nothing new
  in the compiler or engine — installing an artifact writes a catalog
  entry/template through the ADR-0067/0027 path, which produces ordinary
  executable BPMN. **The six invariants are untouched.**

- **Source: curated-local now, remote-ready by construction.** The first slice
  ships a small **curated, bundled catalog** (option A) — the safe, no-network
  MVP that resolves ADR-0027's open "how is a catalog populated" question with a
  concrete first answer. But the install path, manifest, and catalog store are
  designed so that a **single Atlas-hosted registry (B)** and eventually
  **federated/self-hosted registries (C)** are additive: a registry is just
  another source of the same manifested packages, fetched over HTTPS into the
  same local catalog that ADR-0027 already serves. No reshaping to go remote.

- **Trust split by artifact kind (option i) — the load-bearing decision.**
  - A **connector / service-task template** is *data*: property bindings to
    extensions the engine already runs, with **no credentials** (ADR-0036/0041/
    0067). It installs like any ADR-0027 template, showing author + checksum.
  - A **script-task** carries *executable code* (ADR-0047's largest-attack-surface
    warning). It is **never auto-installed as runnable**: it imports as an
    **editable draft** (ADR-0021) the user must open, read, and explicitly deploy;
    it runs only where the relevant script worker is enabled (ADR-0047's opt-out
    CLI), and the recommended posture remains the external, isolated worker. The
    marketplace *surfaces* provenance and makes review unavoidable; it does not
    sandbox — sandboxing/signature verification are named follow-ups.
  - **Secrets never travel.** A published artifact is validated to carry no
    credential material; auth stays a server-side named reference (ADR-0036/0067)
    resolved from the vault (ADR-0069/0070) on the installing server.

- **Discovery: a searchable in-Modeler gallery (option x).** Reuse ADR-0067's
  data-driven catalog rendering for a gallery filtered by `kind`, searchable by
  title/description, showing icon + author + compatibility. A thin **popularity
  signal** (install count / "featured") is specified now but only lights up once
  a remote source (B) exists — the "make it popular" driver gets a real, if
  minimal, home rather than being deferred wholesale.

Option 2 is rejected: inventing a bundle format throws away ADR-0027's interop
and existing template ecosystem for no near-term gain. Option 3 is rejected: it
triples the surface users must learn and the code must maintain. Enforcement
option ii is rejected: it would let a stranger's PowerShell auto-enable, exactly
the surface ADR-0047 warns about. Discovery option y fails the actual request
("popular").

### Consequences

- **Positive:** community sharing arrives by *distributing* the package format
  Atlas already has (ADR-0027), so a connector author's existing template is
  already publishable; one unit covers all three task kinds; the engine and its
  six invariants are untouched (install is deploy-time template data); the
  curated-local MVP ships with no network and no new trust surface, while the
  manifest/store are shaped so a remote registry is additive; the data-vs-code
  split lets safe connector templates be one-click while dangerous code stays
  behind mandatory review.
- **Negative / trade-offs accepted:** the MVP's "marketplace" is a curated local
  catalog — genuine community publishing (upload, moderation, a public registry)
  is a later slice, so "extremely easy to *publish*" is only partially delivered
  at first (install is easy immediately; publish-to-the-world is not); a manifest
  layered onto ADR-0027 templates means a compatibility/versioning surface to
  maintain ("template updated, instances already applied" — ADR-0027's own open
  problem — now also spans installs); distributing script tasks raises the trust
  stakes even with the draft-quarantine, and we accept that the marketplace makes
  review *unavoidable* rather than making execution *safe*; a popularity signal
  invites gaming and must not be trusted as a security signal.
- **Follow-ups / risks to watch:** the **remote registry** (B) and its
  publish/upload/moderation flow; **package signing and signature verification**
  (provenance beyond a checksum); **script-task sandboxing** / the external gRPC
  worker (ADR-0047) as a precondition for lowering script-install friction;
  **version upgrade of already-applied artifacts** (shared with ADR-0027);
  federated/self-hosted registries (C) for internal/enterprise catalogs; how
  marketplace visibility interacts with sharing scopes (ADR-0071) when a shared
  server installs an artifact; ratings/reviews and abuse/moderation once
  community upload exists; a de-listing / vulnerability-response path for a
  published artifact later found harmful.

## Pros and cons of the options

### The shareable unit

**Option 1 — element-template + manifest (chosen).**
- Good: reuses ADR-0027's interoperable format and existing templates; one unit
  spans connector, service task, and script task; install stays pure template
  data over the ADR-0067/0027 write path (no engine change).
- Bad: the template schema was not designed to carry a script body, so the
  script-task encoding is a convention layered on top; a manifest adds a
  versioning/compatibility surface.

**Option 2 — a new Atlas package bundle format.**
- Good: purpose-built; can wrap anything.
- Bad: discards ADR-0027 interop and the existing template ecosystem; a second
  package concept to define, validate, and maintain; more to learn.

**Option 3 — three separate distribution stories.**
- Good: each tuned to its kind.
- Bad: triples the surface for users and maintainers; contradicts "one thing to
  share"; guarantees inconsistency between the three.

### The source

**Option A — curated local/bundled only (chosen as the first slice).**
- Good: no network, no new trust surface, ships immediately; concretely answers
  ADR-0027's open "how is a catalog populated".
- Bad: not really "community" yet — no external publishing.

**Option B — single Atlas-hosted remote registry.**
- Good: real distribution and discovery; a natural home for popularity.
- Bad: hosting, moderation, availability, and a much larger trust surface;
  premature before the local install path is proven.

**Option C — federated/self-hostable registries.**
- Good: internal/enterprise catalogs; no central chokepoint.
- Bad: heaviest; index format, discovery across registries, and trust config for
  each — far beyond today's need.

### The trust model

**Option i — data installs, code is gated (chosen).**
- Good: matches the real risk asymmetry (ADR-0047); keeps connector templates
  one-click while forcing review of executable scripts; shows provenance.
- Bad: script installs stay deliberately higher-friction (a feature, not a bug).

**Option ii — treat all artifacts identically.**
- Good: simplest UX.
- Bad: would let a stranger's script auto-enable — exactly the surface ADR-0047
  warns against. Not viable.

**Option iii — curated-only.**
- Good: safest.
- Bad: is option A's trust model; cannot express community-contributed content,
  so it fails the request once a registry exists.

### Discovery

**Option x — searchable in-Modeler gallery + thin popularity signal (chosen).**
- Good: reuses ADR-0067's catalog rendering; gives "make it popular" a real home;
  minimal but present.
- Bad: a popularity signal is gameable and must never be read as a trust signal.

**Option y — flat list, no discovery surface.**
- Good: nothing to build.
- Bad: fails the "extremely easy and popular" goal outright.

## Links

- builds on [ADR-0027](0027-element-templates.md) (element-templates as the
  package format; this ADR answers its open "how is a catalog populated" and
  "version upgrade of applied templates" follow-ups)
- builds on [ADR-0067](0067-service-task-connector-catalog.md) (the data-driven
  catalog and its rendering, reused for the gallery)
- concerns [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md) (script
  tasks as the code-bearing, trust-gated artifact kind; its opt-out worker and
  external-worker isolation are the safety preconditions)
- relates to [ADR-0071](0071-sharing-scopes.md) (design-time sharing *within* a
  server; this ADR is distribution *across* servers — the org/public-visibility
  axis ADR-0071 left open, but for reusable artifacts rather than projects)
- honors the credential-handling boundary of
  [ADR-0036](0036-clio-connector.md) / [ADR-0041](0041-connector-management-and-secret-store.md)
  / [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) (no secret in a
  shared artifact)
- installs as deploy-time template data over the ADR-0025 property write path;
  does not touch the six invariants (`../architecture/invariants.md`)
