# ADR-DRAFT: The Repository becomes the local view of atlas hub and installs nothing of its own

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-10
- **Deciders:** pblumer (maintainer; approval pending)
- **Open question:** how the Modeler's task panel offers a Worker Type installed from
  atlas hub — from the element templates its manifest carries (which needs the applier
  of ADR-0212) or from the manifest's JSON Schemas (which ADR-0535 § 4 names for
  configuration forms, not for the task panel). Stage 3 below cannot be designed until
  that is settled.
- **Question checked:** 2026-11

## Context and problem statement

Atlas has three places that answer "which Worker Types can a process on this server
use", and they do not agree.

1. **The binary.** Every Worker Type is compiled in (ADR-0535, Context). What the
   Modeler offers on a service task is `SERVICE_TASK_KINDS` in `api/web/editor.js`;
   that list, and nothing else, decides what a modeller can choose.
2. **The Repository** (ADR-0081). A curated catalogue of 17 element-template packages,
   embedded with `go:embed repository_catalog/*.json`. Until this record's stage 0 the
   Console offered *Install* and *Remove* on every package. An install wrote a record
   into `<data>/repository`, and the only reader of that store was the gallery itself,
   to draw an "Installed" pill (`api/repositorystore.go`, ADR-0212). Install and Remove
   therefore changed nothing a modeller could see. A script-task install answered
   "imported for review", but no review step exists; it saved the same record.
3. **atlas hub** (ADR-0499). Signed, operator-pinned registries of application
   Blueprints, browsed from the Console's project list and imported through ADR-0467's
   existing importer. The v1 index accepts application packages only. ADR-0535 § 6
   (Accepted, Partial) adds a `worker-type` release kind whose manifest carries the
   Worker Type's element templates; ADR-0536 (Proposed) defines its curation tiers.

Measured on 2026-11-10, the catalogue and the binary diverge in both directions:

- 15 packages describe a kind the binary ships (the release registry in
  `api/releasedkinds.go` names them);
- one package, `community.slack-message`, describes a Slack service task for which no
  Worker Type, compiler branch or moddle type exists;
- one package, `community.powershell-ad-lookup`, is a script sample, not a kind;
- 16 released connector kinds have no package at all (`packagesOwed` in
  `api/releasedkinds.go`), among them Active Directory, Entra ID, SOAP, the SQL kinds
  and Discord.

The records disagree about what is meant to become the single channel. ADR-0208 § 5
says Worker Type packages reuse the Repository's package model. ADR-0535 makes atlas
hub the distribution channel for Worker Types and cites neither ADR-0081 nor ADR-0212.
ADR-0167 ties the definition of a *released* kind to a Repository package, and the
release registry enforces that with a ratchet. ADR-0212 decided to build an applier
but left open, as its own option 4, that the Repository might honestly be a
documentation gallery instead; it is Proposed and nothing of the applier exists.

The question this record answers: **what is the Repository for, now that atlas hub
exists and is meant to distribute Worker Types?**

## Decision drivers

- **The Console must not offer an action that has no effect** (ADR-0212's own driver).
- **One source of truth for what a server offers.** Two hand-maintained lists that
  describe the same Worker Types drift; this one already has, in both directions.
- **No second installer.** ADR-0499 imports through ADR-0467's existing path; a
  Worker Type from atlas hub is installed through ADR-0535's verified path. A local
  page must not grow a third.
- **Offline and air-gapped installations keep working.** A server with no registry
  must still be able to show what it ships. ADR-0535 already requires organisation
  Worker Types to work without a public registry.
- **Deployments never change meaning (I6).** Nothing here may alter a deployed model
  or a persisted job-type index.
- **The descriptions are worth keeping.** The package texts are the only prose, outside
  the ADRs, that explains a Worker Type's operations to a modeller in one place.
- **Public API churn is called out.** Atlas is pre-1.0 and the HTTP surface may
  change in any release (CHANGELOG), but a removal is still announced before it lands.

## Considered options

1. **Keep the Repository as a catalogue only** (the state after stage 0): no install,
   the bundled JSON stays the source of the descriptions, atlas hub stays separate.
2. **Build the ADR-0212 applier**, so that an install puts a pre-filled task variant
   into the Modeler.
3. **Remove the Repository** — page, routes, store and catalogue — and leave discovery
   to the Modeler's kind picker and to atlas hub.
4. **Make the Repository the local view of atlas hub:** one Modeler page that lists
   the Worker Types this server offers, taking their descriptions from their
   manifests, and later browses the pinned atlas hub registries for Worker Type
   releases. It installs nothing itself and hands every installation to the path the
   release kind already has.

## Decision outcome

Chosen option: **4**, in stages, because it is the only option that leaves one source
of truth for what a server offers (the Worker Type manifest, ADR-0535/0546), keeps the
descriptions and the offline case, and adds no installer.

### Stage 0 — landed with this record (ADR-0212's immediate fix)

- The Console's Repository page installs nothing. *Install*, *Review & install* and
  *Remove* are gone, and so is the "Installed" pill.
- Each package says what it is: **Built in** when the release registry names it as a
  shipped kind's package, **Not built in** when no kind stands behind it, **Script
  sample** for a script task. The list response carries this as `builtIn`.
- *Show template* displays the element template a package carries, so a script
  sample's script can be read.
- The routes under `/api/v1/repository/` are unchanged.

### Stage 1 — retire the install half

- `POST /api/v1/repository/packages/{id}/install`, `GET /api/v1/repository/installed`
  and `DELETE /api/v1/repository/installed/{id}` are marked `deprecated` in the OpenAPI
  document, with a changelog entry under *Deprecated*, and removed one release later
  under *Removed*. The read routes stay until stage 2.
- Records already in `<data>/repository` are left on disk and ignored: nothing ever
  read them, so there is nothing to migrate. Backup and restore keep treating the
  directory as an opaque sidecar until it is removed.
- `community.slack-message` is removed from the catalogue, because it describes a
  Worker Type Atlas does not have.

### Stage 2 — the manifest becomes the source of the descriptions

- A built-in Worker Type's manifest (ADR-0546) gains the description and the
  operations' prose the package carries today. The page lists built-in Worker Types
  from their manifests, not from `repository_catalog/`.
- ADR-0167's mandate is restated against the manifest: a released connector kind owes
  a manifest with a description, and the `packagesOwed` ratchet folds into the existing
  `kindsWithoutManifest` ratchet. The bundled JSON catalogue is deleted once every
  packaged kind's description lives in its manifest.
- The script sample either moves into the Modeler's script-task examples or is
  dropped; a code-bearing artifact does not return to a gallery (ADR-0167 already
  exempts script tasks).

### Stage 3 — atlas hub Worker Types

- When atlas hub carries the `worker-type` release kind (ADR-0535 § 6), the page lists
  the releases of the operator-pinned registries beside the built-in Worker Types and
  starts installation through ADR-0535's verified path. It is a view; the trust
  decisions stay where ADR-0535 and ADR-0536 put them.
- Blueprint browsing stays in the Console's project list, where ADR-0467's import runs;
  the page links to it rather than duplicating it.
- This stage waits for the open question above.

### What this record changes in others, on acceptance

- **ADR-0212:** resolved by its option 4. The applier is not built; whether a template
  applier is needed at all is the open question of stage 3.
- **ADR-0081:** superseded in its install half (the template store and the install
  routes). Its trust split and its no-secrets rule carry over to atlas hub unchanged.
- **ADR-0167:** amended in stage 2; the mandate moves from the package to the manifest.
- **ADR-0208 § 5:** superseded by ADR-0535 § 6 for distribution; the Repository is not
  a Worker Type registry.

### Consequences

- **Positive:** the page stops promising an effect it does not have; one description
  per Worker Type, kept beside the code that implements it; the offline case is served
  by the manifests the binary carries; no new installer and no new write path.
- **Negative / trade-offs accepted:** three deprecated routes and their removal are a
  visible API change; stage 2 moves prose into 31 manifests that do not exist yet
  (`kindsWithoutManifest`), so the descriptions stay in the JSON catalogue for some
  time; until stage 3 the page shows only what the binary ships, so it overlaps with
  the Modeler's kind picker.
- **Follow-ups / risks to watch:** the open question above; whether a Worker Type page
  belongs in the Modeler (where a kind is chosen) or the Console (where a Worker is
  configured) is assumed, not measured; external callers of the install routes are not
  known — none exists in this tree and no MCP tool calls them, but an operator's own
  script could.

### Invariants and scale-out readiness

Everything here is design-time data served from the binary or from a sidecar store; it
never reaches the log, the processor or `applyToState`. I1–I6 are untouched. The page
holds no partition state and no host-local singleton beyond the existing sidecar
store, which stage 1 retires; in a multi-node deployment the list is the same on every
node because it comes from the binary and, in stage 3, from the pinned registries.

## Pros and cons of the options

### Option 1 — catalogue only

- Good: no further work; honest after stage 0.
- Bad: keeps two hand-maintained lists of Worker Types that already disagree; leaves
  ADR-0167 tied to a gallery nobody installs from; atlas hub and the Repository remain
  two pages for one idea.

### Option 2 — build the ADR-0212 applier

- Good: an install would finally do what the word says; pre-filled variants of
  built-in kinds become possible.
- Bad: every packaged kind already has a hand-written panel with per-operation field
  visibility a flat template cannot express, so the applier serves only modest
  variants; it adds an Atlas-specific manifest field to 15 packages; it builds a
  distribution path ADR-0535 has since placed in atlas hub.

### Option 3 — remove the Repository

- Good: the least code; no overlap with atlas hub.
- Bad: loses the only consolidated descriptions; breaks ADR-0167's release check with
  nothing in its place; leaves no local place where atlas hub Worker Types will show.

### Option 4 — the local view of atlas hub (chosen)

- Good: one source of truth (the manifest); offline works; no new installer; a natural
  home for stage 3.
- Bad: staged over several releases; stage 3 depends on an unsettled question and on
  ADR-0535 § 6 being built.

## Links

- [ADR-0027](0027-element-templates.md) — element templates
- [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md) — the Repository
- [ADR-0167](0167-released-connectors-ship-in-the-marketplace.md) — the release mandate
- [ADR-0208](0208-worker-type-packages.md) — Worker Type packages
- [ADR-0212](0212-element-template-applier.md) — the applier and the honesty fix
- [ADR-0467](0467-application-package-export.md) — application package import
- [ADR-0499](0499-blueprint-registry-and-distribution.md) — atlas hub
- [ADR-0535](0535-worker-types-as-distributed-artifacts.md) — Worker Types as distributed artifacts
- [ADR-0536](0536-worker-sdk-licence-and-hub-curation.md) — atlas hub curation tiers
- [ADR-0546](0546-new-built-in-worker-types-ship-a-manifest.md) — built-in Worker Type manifests
