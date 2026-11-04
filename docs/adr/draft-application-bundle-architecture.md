# ADR-DRAFT: What an application bundle carries of the architecture, and where the capability map lives

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-04
- **Deciders:** Atlas maintainers
- **Open question:** Whether several Atlas servers of one organisation must share one
  capability map (option C), or whether one map per server, kept consistent by hand or by
  export, is enough. The answer depends on how many servers an organisation runs for one
  business architecture (DEV/INT/PROD, one per unit, a central one), which nobody has
  collected yet.
- **Question checked:** 2026-11

## Context and problem statement

PR #1559 gives the M365 example its data and strategy layers so that a release's
architecture sketch (ADR-0471, ADR-0495) shows more than processes and workers: an
information model (ADR-0230/0259), data objects and stores in every process, eleven
capabilities and two value streams (ADR-0305). Getting these onto a server needs an extra
script (`architektur/einspielen.py`) between import and publish, because:

1. **An application bundle carries none of it.** The source tree (`atlas.json`, ADR-0134,
   ADR-0467, format 3) and therefore an atlas hub package (ADR-0499) carry processes,
   forms, decisions, decision models and optionally constants. A hub import through the
   Console brings processes, forms and decisions only; the sketch of the release that
   follows has no data layer and no strategy layer.
2. **Capabilities and value streams are server-wide records.** The register of ADR-0305 is
   one flat map per server, keyed by a portable key. An application's processes realize
   capabilities (`realizations[].applicationKey/processId`), but nothing records that a
   capability came with, or belongs to, an application. Two applications that both bring
   `m365-konten` collide; removing an application leaves its capabilities behind.
3. **The sketch shows writes only.** A data input association writes a variable
   (ADR-0059), so reads are not drawn in the example.

The information model is already filed per application (`applicationId` on the infomodel
records); only its transport in the bundle is missing. The question this record answers is
the other one: **where does the capability map live — in the application, on the server,
or above the server — and what, accordingly, does a bundle carry?**

Who reads capabilities today, and would notice a change of their scope:

- **Structured catalogues (ADR-0524):** a Marktleistung's category and product group are
  capability keys, resolved against the server's register at publish.
- **Panorama (ADR-0308)** binds the register; coverage, measurement, confirmation
  (ADR-0304, ADR-0309) and the business-architecture gaps read it as one map.
- **The release architecture sketch (ADR-0471/0495)** picks the capabilities a release's
  processes realize, matched by the application's key.
- **MCP tools** (`atlas_*capabilit*`, value streams, `atlas_business_architecture_gaps`,
  `atlas_capability_coverage`) address capabilities by key, server-wide.

## Decision drivers

- **No extra script.** Whatever a bundle needs for a complete sketch must travel in the
  bundle and be applied by every import path (hub import in the Console and through
  `…/import`, source-tree upload, `atlas import`), and come back out on export.
- **The method's shape (ADR-0305).** A capability describes what the *organisation* can
  do, independent of how it is implemented. Applications *realize* capabilities; they do
  not define the organisation.
- **One heading, one meaning.** ADR-0524 made catalogue headings capability keys so that
  "Identität", "Konto" and "Account" stop being three headings for one thing. Whatever is
  decided must not bring that back per application.
- **Imports never replace work silently (ADR-0134, ADR-0467).** An import that creates or
  changes records outside the application it installs has to show it in the plan and be
  decided, like an artifact another application holds.
- **Design-time only.** Capabilities, value streams and the information model are sidecar
  records; nothing here reaches the event log or recovery (I2, I4). Whatever option is
  chosen must stay that way.
- **Removing an application leaves a consistent map.** Deleting or purging an application
  (ADR-0034) should not leave realizations pointing at nothing without saying so.

## Considered options

1. **A — Capabilities per application, the server condenses.** Each capability and value
   stream is owned by one application and travels in its bundle. The server shows a
   condensed overview across applications (capability landscape grouped by application,
   or merged by key).
2. **B — One capability map per server (enterprise view); bundles bring realizations,
   their information model and proposals.** The register stays server-wide. A bundle
   carries its information model, the realizations of its processes and products
   (process/product → capability key), and *proposed* capabilities and value streams for
   keys it needs. On import, a key the server knows is linked; a key it does not know
   appears in the plan as a gap with the bundle's proposal, which an architect adopts
   (files it in the map) or maps onto an existing key — never created silently.
3. **C — As B, but the map itself is a versioned, signed hub package (reference model)
   that several servers share.** The capability map and value streams are published like
   an application bundle (ADR-0499: signed, pinned by digest, versioned), installed on
   every server of the organisation, and updated opt-in (as ADR-0506 proposes for
   catalogue bundles). Application bundles declare which reference model and version their
   realizations are written against.

Common to all three: the **information model travels in the bundle** (format 4 of the
source tree, `architecture/information-model.json`, planned and decided as an artifact like
a form), because it already belongs to the application.

## Decision outcome

Proposed: **B**, built so that **C** can follow without changing the bundle format.

- B keeps the register what ADR-0305 made it — one flat, portable map of the organisation —
  so catalogue headings (ADR-0524), Panorama (ADR-0308), coverage and gaps keep one meaning.
- B removes the extra script: everything an application contributes to the sketch (its
  information model, its realizations) travels in its bundle and is applied on import; what
  it would add to the organisation's map arrives as a decided proposal, not as a side
  effect.
- The ownership problem of A does not arise, because a bundle never owns a capability: two
  applications that both need `m365-konten` both *realize* it.
- C is the right answer once an organisation runs several servers on one map. B's bundle
  format (realizations against keys, proposals for missing keys) is exactly what C needs;
  C adds a package kind for the map and a reference ("written against map X version Y"),
  and can be decided when the open question above has an answer.

### What B means concretely

- **Bundle format 4** adds to `atlas.json`:
  - `informationModel`: path of the application's information model (the import format of
    `POST /api/v1/infomodel/import`).
  - `architecture`: path of a file with `realizations` (process id or product id →
    capability key, optionally value-stream stage) and `proposals` (capabilities and value
    streams in the shape of ADR-0305, used only for keys the server does not know).
- **Import plan** gains refs `infomodel:<application key>`, `realization:<process>→<key>`
  and `capability:<key>` / `valuestream:<key>` for proposals. A proposal for an unknown key
  is `Undecided` until the caller adopts it (filed in the map with the bundle's content) or
  maps it onto an existing key; a proposal for a known key is shown as a difference and
  never overwrites the map's record. Non-architects (no right to write capabilities) get
  the realizations and the information model; proposals stay open as gaps in
  `atlas_business_architecture_gaps`.
- **Realizations move to where they belong.** Today they live inside the capability record
  (`realizations[]`). A realization brought by a bundle is owned by the application: it is
  written, replaced and removed with the application. The capability record keeps the
  realizations written by hand; the read side unions both. (The storage form — a field
  with an owner on the capability record, or a separate sidecar keyed by application — is
  an implementation choice; the separate sidecar is preferred because deleting an
  application then touches no capability record.)
- **Export** writes the application's information model, its realizations and, as
  proposals, the capabilities and value streams its realizations name, so a tree installs
  on an empty server with a complete sketch after an architect adopts the proposals.
- **The sketch** (ADR-0471/0495) is unchanged in what it draws; it now finds the data layer
  and the strategy layer for any installed bundle.
- **Reads in the sketch** (gap 3) are not this record's: drawing reads needs a data input
  association that does not write a variable (ADR-0059). It is named here so it is not
  lost.

### Consequences

- **Positive:** no extra script; one map with one meaning; catalogue headings, Panorama,
  coverage and gaps untouched; deleting an application removes exactly its realizations
  and model; the step to C is additive.
- **Negative / trade-offs accepted:** a bundle installed by somebody who may not write
  capabilities lands with open gaps instead of a complete strategy layer until an architect
  decides the proposals; the import plan grows a new kind of decision (adopt or map a
  proposal).
- **Follow-ups / risks to watch:** the storage move of realizations needs a migration that
  reads existing `realizations[]` unchanged (design-time sidecar, no WAL impact); the
  Console needs an import dialog section for proposals; the MCP tools need the proposal
  decisions; C stays open until the open question is answered.

## Pros and cons of the options

### A — Capabilities per application, the server condenses

- Good: a bundle is self-contained; installing it gives a complete sketch without anybody
  else deciding anything.
- Good: removing an application removes its capabilities.
- Bad: it contradicts ADR-0305's premise that a capability belongs to the organisation, not
  to an implementation; two applications realizing the same capability each bring their own
  copy, and the "condensed" server view has to merge copies that may differ in name, owner,
  inputs and SLAs.
- Bad: ADR-0524's catalogue headings and ADR-0308's Panorama binding need one key with one
  meaning. With app-owned capabilities they need either app-qualified keys
  (`beispiel-m365-dienste/m365-konten`) — which reopens the "three headings for one thing"
  problem — or a second, server-wide layer, which is option B with extra steps.
- Bad: largest change: ownership on every capability, a migration of existing records into
  some application or into a "server" pseudo-application, and new rules for every reader
  listed above.

### B — Server map; bundles bring realizations, information model and proposals

- Good: keeps ADR-0305/0308/0524 as they are; one heading, one meaning.
- Good: the bundle is complete for what the application *is* (model, realizations); what it
  would *add to the organisation* is visible and decided, in line with ADR-0134/0467.
- Good: realizations become application-owned, which fixes the clean-up on delete.
- Bad: a complete strategy layer on a fresh server needs an architect to adopt proposals
  once.
- Bad: a new decision kind in the import plan and its UI.

### C — As B, with the map as a shared, signed hub package

- Good: one map across all servers of an organisation (DEV/INT/PROD, several units), with
  versions, signatures and opt-in updates already known from the hub (ADR-0499, ADR-0506).
- Good: applications can state the map version they were written against, so a gap is
  "your server runs map 1.2, this bundle needs 1.3" rather than a list of unknown keys.
- Bad: a second package kind and its lifecycle (install, update, conflicting local edits to
  a shared map) before anybody has asked for more than one server per map.
- Bad: local edits to the map need a rule (fork, overlay, or forbidden), which is a
  decision of its own.

## Links

- relates to ADR-0305 (capabilities and value streams), ADR-0308 (Panorama binds the
  register), ADR-0304, ADR-0309
- relates to ADR-0524 (catalogue headings are capability keys)
- relates to ADR-0134, ADR-0467 (source tree, application package export), ADR-0499 (atlas
  hub), ADR-0506 (hub catalogue bundles and updates)
- relates to ADR-0230, ADR-0259 (information model), ADR-0058, ADR-0059 (data associations)
- relates to ADR-0471, ADR-0495 (release architecture sketch)
- relates to ADR-0420 (one graph model, several projections) for C's cross-server view
- context: PR #1559 (M365 example architecture, `architektur/einspielen.py`)
