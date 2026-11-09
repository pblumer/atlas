# ADR-DRAFT: An atlas hub release shows its architecture sketch and its process models before it is installed

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-09
- **Deciders:** pblumer (maintainer; asked on 2026-11-06 for the application's architecture sketch in atlas hub, and on 2026-11-09 for it to be built)

## Context and problem statement

The atlas hub preview ([ADR-0499](0499-blueprint-registry-and-distribution.md)) tells an
operator what a release is: its identity, digest, licence, declared needs, catalogues,
products and file list. It does not tell them what the application *does* — how it meets
its environment, which workers it needs, which messages start it, what its processes
look like. Somebody deciding whether to install a Blueprint has to install it to find out.

Atlas already draws that picture for releases: every release carries an architecture
snapshot taken when it is recorded ([ADR-0471](0471-release-architecture-sketch.md)),
drawn in layers ([ADR-0495](0495-layered-release-architecture.md)). Two facts keep that
picture out of the hub as it is:

1. **The snapshot is taken on an installation.** `architectureSnapshot` reads the
   compiled processes of deployed definitions, and its physical part, strategy layer,
   catalogue products and coupling peers read that installation's registers. A hub
   release has been deployed nowhere; no installation has read it.
2. **The snapshot shows the boundary, not the flow.** It answers which connections exist
   and who opens them — the question operations, network and security reviewers act on.
   It names the processes (as Application and Business Processes) but not their control
   flow. "What does it do" is answered by the BPMN models themselves.

The question this record answers: what does the hub preview show of an application's
architecture and behaviour, derived from what, where, and how is it kept from being
mistaken for a release's sketch?

## Decision drivers

- **Derived, never drawn** (ADR-0471): nobody maintains a picture that drifts from the
  package.
- **True about what it is.** A preview must not claim facts of an installation it never
  read — a worker's endpoint, "no other application here", a release number nobody
  minted, a moment nobody took a snapshot at.
- **Bound to what was reviewed.** What the preview shows must be the bytes whose digest
  the operator saw, as the import is (ADR-0499 amendment d).
- **No new trust in publisher content.** A rendering the publisher supplies is active
  content before review; the server should draw only what it parsed itself.
- **No change to the registry's signed index.** A new index field is refused by every
  server that predates it (ADR-0499 amendment 2026-11-02).
- **The engine is untouched.** Nothing here enters the WAL, `applyToState`, a processor
  or the run loop.

## Considered options

Where the sketch comes from:

1. **A — Built at `atlas hub build`, referenced from the signed index.**
2. **B — Built at `atlas hub build`, carried inside the package.**
3. **C — Derived by the installing server from the verified package, on request.**

Whether the BPMN flow belongs in the sketch:

1. **F1 — Draw the process flow into the sketch.**
2. **F2 — Keep the sketch as it is and show the models beside it.**

## Decision outcome

Chosen: **C and F2.**

### 1. The server derives the sketch from the package's process models

`GET /api/v1/blueprints/{id}/releases/{version}/architecture` (JSON) and `…/architecture.svg`
fetch the release from the operator-pinned registry, refuse it unless `?digest=` equals
the digest the preview reported, inspect it as the preview does, read it as a source tree
(ADR-0467) and compile each process file with `compiler.ParseAll`, exactly as the import's
needs check does. The facts are collected by `logicalSketch` — the same pure function a
release's snapshot now uses for its model-bound part — so a release and a package draw the
same models the same way:

- the processes, with a file that does not compile recorded as unread;
- the messages they receive, and the workers, job types, decisions and calls they name;
- the data objects, stores and personal-data declarations, with classes checked against
  the information models the package carries (ADR-0528);
- the processes as work and the roles their user tasks and lanes name;
- the messages and signals they throw and catch.

It is **not** taken: no physical part, no strategy layer, no catalogue products, no
applications at the other end of a name and no target application for a call. Each of
those is an installation's fact. The snapshot is marked `packaged`, which the renderings
read: a name with nobody at its other end reads "whatever the installation runs", a call
out of the package reads "not in this package", the bands say "this package", and the
not-shown list names the capability register, the catalogues and the coupled applications.
`?layers=` works as on a release's route; the technology layer is refused as not recorded.

The SVG's header names the Blueprint, its version and digest instead of a release
(`Meta.Blueprint`); the footer says the sketch was derived before any installation read
the package and that a release published after installing records its own sketch, which
can differ. A release's rendering is byte for byte unchanged.

### 2. The models are shown beside the sketch, not inside it

`GET …/diagrams` answers the BPMN files the package's manifest names, under the same
digest rule. The Console's hub preview shows the sketch (application, business and data
layers) as an image under the package's metadata, and on request loads the models and
draws the chosen one read-only in the viewer the Tasks app uses (`mountDiagramPreview`).
The models are loaded only when asked for, because every read fetches the package again.

### 3. Surfaces

- **HTTP:** the three routes above, role modeler, as the preview.
- **MCP:** `atlas_hub_release_architecture` proxies the JSON route. The SVG is omitted as
  a rendering for people; `diagrams` is omitted because MCP never moves raw package
  content (ADR-0499 amendment d), and the tool answers what the models fix as data.
- **Console:** the hub preview dialog.

### Consequences

- **Positive:**
  - An operator sees what an application does — its boundary and its processes — before
    installing it, without anybody having drawn anything.
  - The sketch is the server's own rendering of bytes it verified; no publisher-supplied
    image is shown, and the signed index is unchanged, so no older server is affected.
  - One pure collection function serves releases and packages, so the two cannot drift.
- **Negative / trade-offs accepted:**
  - Each read downloads and inspects the package again (bounded by ADR-0499's 50 MiB cap
    and the server's bundle budget); a preview with diagrams fetches it three times.
  - The sketch depends on the installing server's renderer and mapping version, not the
    publisher's: two servers of different versions may draw the same package differently.
    The stamp names the mapping version.
  - The preview sketch and the sketch of the release published after installing differ
    by design; the footer says so.
  - The package's BPMN is parsed by bpmn-js in the browser before the import's own review.
    bpmn-js draws labels as text; the models reach the page as JSON data, never as markup.
- **Follow-ups / risks to watch:**
  - Cache a derived sketch per digest if repeated downloads become a load.
  - A link from a box in the sketch to the element it names in the diagram (the snapshot's
    `usedBy` already carries the element ids).
  - Comparing the preview sketch with the first release's sketch after installing.
  - A static rendering on the public hub site for readers without atlas (option A or B),
    which needs its own decision on index compatibility and publisher-supplied content.

## Pros and cons of the options

### A — Built at `atlas hub build`, referenced from the index
- Good: visible to readers without an atlas installation; computed once.
- Bad: a new index field is refused by every server that predates it; the image is the
  publisher's, not the reader's server's.

### B — Built at `atlas hub build`, inside the package
- Good: no index change; covered by the package digest.
- Bad: publisher-supplied SVG is active content before review; the reader still has to
  download the package; only atlas-built registries would carry it.

### C — Derived by the installing server (chosen)
- Good: no schema change; the server draws only what it parsed and verified; works for
  every registry and every package already published.
- Bad: repeated downloads; invisible without an atlas installation.

### F1 — The flow inside the sketch
- Good: one picture.
- Bad: ArchiMate has no faithful place for gateways, events and loops; the fixed,
  deterministic layout of ADR-0471 §3 cannot hold a flow; a lossy flow would be read as
  complete.

### F2 — The models beside the sketch (chosen)
- Good: each picture answers its own question; the models are shown exactly as authored.
- Bad: two views to look at instead of one.

## Links

- [ADR-0471](0471-release-architecture-sketch.md), [ADR-0495](0495-layered-release-architecture.md) — the release sketch this derives from
- [ADR-0499](0499-blueprint-registry-and-distribution.md) — atlas hub, the preview and the digest-bound import
- [ADR-0467](0467-application-package-export.md), [ADR-0528](0528-application-bundle-architecture.md) — the source tree read here
- [Architecture invariants](../architecture/invariants.md)
