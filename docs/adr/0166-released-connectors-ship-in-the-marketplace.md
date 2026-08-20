# ADR-0166: A released connector ships in the marketplace

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas now has two catalogs of connectors, and nothing keeps them in step.

- The **compiled connector catalog** (ADR-0067) is the data-driven list of connector
  *kinds* the Modeler renders for authoring. It is compiled into the binary and grows
  every time a kind ships — REST (ADR-0067), REST/OAuth2 (ADR-0152), SCIM (ADR-0153),
  LDAP (ADR-0154), SOAP (ADR-0165), and the managed connectors before them. Adding a
  kind here is what makes it *authorable*.
- The **marketplace** (ADR-0081) is the discovery surface: a searchable gallery whose
  first slice is a curated, bundled catalog of element-template-plus-manifest packages
  (ADR-0027). It is what makes a connector *findable and installable* by name, with its
  provenance and compatibility shown. Adding a package here is what makes a kind
  *discoverable*.

These are populated by different acts. A kind reaches the compiled catalog because a
maintainer wired it into `NewBuilder` and the Modeler; it reaches the marketplace only
if someone *also* authors and drops in a package. Nothing links the two, so the default
outcome is drift: a connector ships, becomes usable through the built-in catalog, and is
**absent from the gallery Atlas built for exactly this** — the "extremely easy to
discover and install" surface that was ADR-0081's whole point. The gallery slowly stops
describing what the binary can actually do, and a connector's release is silently
incomplete in the one place a user goes looking.

The question this ADR answers: **is publishing a newly released connector to the
marketplace a mandatory part of releasing it, and how is that made impossible to forget
rather than a convention that quietly rots?**

## Decision drivers

- **The marketplace is the discovery contract (ADR-0081).** A connector a user cannot
  find in the gallery is, for the purpose of adoption, not released — it exists only for
  whoever already knows the kind name. Leaving publication optional defeats the surface's
  reason to exist.
- **Two catalogs with no link always diverge.** This repository's settled answer to "two
  sources of truth drift" is not a docstring but a test in the mandatory `go test ./...`
  sweep — the ADR-index guard (`docs/adr`) and the OpenAPI route-table drift test
  (ADR-0043) are the precedents. A rule with no check is the state we are already in.
- **It is safe to mandate for connectors specifically.** A connector / service-task
  package is *data* — property bindings the compiler already runs, carrying **no
  credentials** (ADR-0036/0041/0081). Publishing one writes catalog/template data over
  the ADR-0027/0067 path; it never reaches the WAL, the processor, or `applyToState`. The
  six invariants are untouched, so making publication compulsory adds a release step, not
  a risk.
- **The mandate must be cheap to satisfy, or it will be gamed.** If publishing means
  hand-writing a second description of a kind the catalog already describes, authors will
  write the thinnest possible package to pass the check. The rule should ride the metadata
  the compiled catalog already holds.
- **"Released" needs a precise, testable definition.** A rule that fires on a fuzzy event
  cannot be enforced. The trigger has to be a fact already in the tree.

## Considered options

1. **Convention only** — a line in `AGENTS.md` and the PR template asking the author to
   remember to publish. No enforcement.
2. **Mandate plus a build test** — state the rule, and add a test to the `go test ./...`
   sweep that fails when a released connector kind has no corresponding marketplace
   package. The ADR-index / OpenAPI-drift pattern, applied to the catalog gap.
3. **Generate the package from the catalog** — derive each connector's marketplace
   package from the compiled catalog's own kind metadata, so there is no separate publish
   act to forget and nothing to drift *from*.

## Decision outcome

Chosen: **mandate publication and enforce it with a build test (option 2), and generate
the bundled package from the catalog metadata wherever the kind is fully described there
(option 3 as the mechanism that makes the mandate cheap).**

**The rule.** A connector kind is not *released* until it is present in the marketplace
catalog. Concretely, a connector kind is "released" when it is (a) in the compiled
connector catalog (ADR-0067), authorable in the Modeler, and (b) backed by an ADR that
is `Accepted` (a `Proposed` kind is not yet released and is exempt until it is accepted).
Every released kind must have exactly one marketplace package (ADR-0081) whose manifest
`kind` is `connector`/`service-task`, naming the same catalog kind, with an `engineCompat`
that includes the current engine version. Shipping a connector and its gallery package is
one unit of work, not two.

**The enforcement.** A test in `docs/` or the marketplace package, inside the mandatory
`go test ./...` run, walks the compiled catalog, filters to released kinds by the ADR
status above, and asserts a matching marketplace package exists for each. It fails the
build — naming the kind and pointing at where to add the package — the moment a released
kind has no gallery entry. This is deliberately the same shape as the ADR-index guard
that keeps `README.md` honest and the ADR-0043 drift test that keeps routes and the
OpenAPI spec in step: a convention the repository refuses to let rot.

**The cheap path.** The compiled catalog already holds a kind's title, description,
icon, and property schema — the bulk of what an ADR-0081 element-template package carries.
The bundled package for a catalog-described connector is therefore *generated* from that
metadata at build time, so satisfying the mandate is the default rather than a second
authoring chore, and the two catalogs cannot describe the same kind differently because
one is derived from the other. A kind whose package needs hand-tuning may override the
generated one; the test checks presence and identity, not that it was generated.

**Scope: connector data, not code.** This mandate is about the data-only artifact kinds —
connectors and service tasks. It deliberately does **not** force **script tasks** into
the marketplace: those carry executable code, and ADR-0081's load-bearing trust split
(option i) keeps code-bearing artifacts behind mandatory human review, never
auto-published. Compelling code into a public gallery would run exactly counter to that.
Managed/server-registered connectors (clio, mail) are released kinds like any other and
are in scope; a package for them advertises the kind and its configuration shape, still
carrying no credential (ADR-0036/0041).

**Relationship to the marketplace's status.** ADR-0081 is `Proposed` and its first slice
is a curated *local* catalog, so this rule binds against that bundled catalog today and
rides ADR-0081 forward unchanged when the remote registry (its follow-up B) lands —
"published" then means "in the registry", the same rule against a wider surface. This ADR
stays `Proposed` until ADR-0081's catalog and the enforcing test are both in place; it is
the policy that turns "we have a gallery" into "the gallery is complete by construction".

### Consequences

- **Positive:** the marketplace gallery is complete by construction — every released
  connector is discoverable and installable there, which is the adoption promise ADR-0081
  made. The two catalogs can no longer drift silently; the gap fails the build with a
  message that says which kind is missing. Because the package is generated from catalog
  metadata, the mandate costs a release almost nothing and the two descriptions of a kind
  stay identical. No engine change and no new trust surface — connector packages are
  credential-free data over the existing ADR-0027/0067 write path (invariants untouched).
- **Negative / trade-offs accepted:** releasing a connector now has a hard gate it did not
  have — a kind cannot merge as `Accepted` without its gallery package, which is the
  intended cost. The build test needs a reliable read of "which ADR backs this kind and is
  it accepted"; until that link is explicit in the catalog it leans on a naming/registry
  convention that itself has to be maintained. Generating a good package from catalog
  metadata will not fit every kind, so some kinds keep a hand-authored override, which is a
  small second surface for those.
- **Follow-ups / risks to watch:** making the *catalog-kind → backing-ADR* link explicit
  in the catalog data so the test reads a fact rather than a convention; whether the same
  mandate should extend to DMN decision connectors and future kinds as they ship; how the
  rule reads against the remote registry (ADR-0081 follow-up B) once "publish" is a network
  act rather than a bundled file — the rule is the same, the check moves; and a de-listing
  path (ADR-0081) for a *deprecated* connector kind, so the mandate has a clean inverse when
  a kind is retired rather than leaving a package the binary no longer backs.

## Pros and cons of the options

### Option 1 — convention only
- Good: nothing to build; zero enforcement code.
- Bad: it is the status quo that produced the drift. A checklist item is forgotten under
  deadline, and the gallery falls behind the binary with no signal that it has.

### Option 2 — mandate plus a build test (chosen)
- Good: the repository's proven answer to drift (ADR-index guard, ADR-0043 drift test); the
  gap becomes a failing build, not a slow rot; the rule is precise and testable.
- Bad: adds a release gate and needs a dependable read of a kind's release status; that
  link must be maintained.

### Option 3 — generate the package from the catalog (adopted as the mechanism)
- Good: removes the separate publish act entirely; the two catalogs cannot disagree because
  one is derived; cheapest possible way to satisfy the mandate.
- Bad: catalog metadata does not capture everything a rich gallery package might want, so
  some kinds still need a hand-authored override; on its own it is a mechanism, not a policy,
  which is why it is paired with option 2's rule and test rather than replacing them.

## Links

- makes mandatory the publication side of [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)
  (the community marketplace and its discovery gallery); binds against its curated bundled
  catalog now and rides its remote-registry follow-up unchanged
- builds on [ADR-0067](0067-service-task-connector-catalog.md) (the compiled connector
  catalog whose kinds this rule tracks) and [ADR-0027](0027-element-templates.md) (the
  element-template package format published to the gallery)
- honors [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)'s data-vs-code
  trust split and [ADR-0036](0036-clio-connector.md) / [ADR-0041](0041-connector-management-and-secret-store.md)
  (no credential travels in a published artifact); deliberately excludes the code-bearing
  script tasks of [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)
- follows the "guard a convention with a test in the mandatory sweep" posture of
  [ADR-0018](0018-test-driven-development.md), the ADR-index guard (`docs/adr`), and the
  OpenAPI route-table drift test of [ADR-0043](0043-openapi-spec-and-embedded-api-explorer.md)
- in scope for the connectors initiative kinds — SCIM (ADR-0153), LDAP (ADR-0154),
  SOAP ([ADR-0165](0165-soap-connector.md)) — each of which must ship its gallery package
  when accepted
- installs as deploy-time template data; does not touch the six engine invariants
  (`../architecture/invariants.md`)
