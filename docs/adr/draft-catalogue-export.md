# ADR-DRAFT: A catalogue is exported as the document it is imported as

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-12
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0436](0436-a-catalogue-is-imported-as-one-document.md) made a whole shop importable as
one document and named the export as its follow-up: a catalogue built in the Console could
reach another installation only as a document written by hand or assembled from the routes'
answers.

Two needs are behind the export, and they are not the same:

- **A whole catalogue** moves from a test installation to production, with every product it
  maintains or offers.
- **Chosen products** move into *another* catalogue — on another installation, or beside the
  catalogue they came from — without bringing that catalogue along.

Both may need the processes the products are bound to. A product binds its provisioning,
deprovisioning, lifecycle and (custom) approval process by id; the process lives in the
deployment registry, not in the catalogue. A catalogue moved without them arrives bound to
ids the target may not have, and publishing there refuses it.

## Decision drivers

- **One format.** What an export answers is what the import takes. A second format would be a
  second thing to keep level with the first.
- **The same authority.** Exporting is reading the whole record — members, approval rules,
  bindings — so it is a maintainer's, not a reader's. Deploying a process is a modeler's, and
  an import that carries processes may not deploy what its importer could not deploy alone.
- **The same deploy door.** A process an import deploys passes every check
  `POST /api/v1/deployments` makes: DMN resolution, the application it files under, the
  mailbox and message-name claims, the personal-data listeners.
- **Repeatable.** Importing the same export twice changes nothing twice, processes included.

## Considered options

1. **Export the import's document; extend the import with `?into=` and `processes`.**
2. **A separate archive format** (a zip of the document and the BPMN files).
3. **Leave processes to the application bundle** ([ADR-0129](0129-remote-deployment-targets.md))
   and export the catalogue alone.

## Decision outcome

Chosen: **option 1.**

**Export.**
- `GET /api/v1/catalogs/{id}/document` answers the catalogue with every product it maintains
  or offers, as a `catalog.Document`. Owner, revisions and times are left out — the import
  assigns its own — and so are theme, logo and pictures, which a document does not carry.
  Only a maintainer exports: a catalogue the caller cannot see is 404, one they only view 403.
- `GET /api/v1/catalog-products/document?ids=a,b` answers the products named and the edges of
  their home catalogues that join two of them, and no catalogue. Each must be a product whose
  home the caller maintains; any other is 404, as the product list leaves it out.
- `?processes=true` on either adds `processes`: `{id, xml}` for every process the products are
  bound to, as the newest version was deployed (the bytes as they arrived, so re-importing on
  the same server finds them unchanged). Atlas's own approval models and the fulfilment
  orchestration are left out — every installation has them. A process not deployed here is
  named under `missingProcesses` rather than silently dropped.

**Import** (`POST /api/v1/catalogs/import`, ADR-0436) gains two things:
- `?into=<catalogue>` imports a document of **products alone** into a catalogue the importer
  maintains. Every product is offered there and the document's top-level `edges` are added to
  it. A product new to this server is homed there; one the server already has **keeps its
  home**, because a product is referenced by catalogues rather than owned by one, and offering
  it in a second catalogue must not take it from the first. A document with catalogues cannot
  be imported into one, and top-level edges are refused without `into`.
- `processes` are checked with everything else before anything is written: the importer needs
  the modeler role as well as productmanager (403 otherwise), each model must compile and must
  deploy the process it is filed under. They are then deployed **before** the catalogue,
  because publishing asks whether bound processes are deployed. Each goes through
  `deployBody`, the body of `POST /api/v1/deployments` extracted for the purpose, as the
  importer. A process whose newest deployed version is the document's model is left
  unchanged. The answer names `deployed` and `unchanged`.

MCP: `atlas_export_catalog`, `atlas_export_catalog_products`, and `atlas_import_catalog` with
`into`, `edges` and `processes`. The Console's catalogue page exports the catalogue or the
ticked products, optionally with processes, and imports products into the catalogue shown; the
catalogue list imports a whole document.

### Why not the others

**Option 2** would keep BPMN as files a modeller can open, which is its strongest point. But it
is a second format beside the one ADR-0436 made the contract, every caller — MCP, a script, an
example's test — would have to pack and unpack it, and the import would still have to read the
document inside it. A BPMN model is text and travels in JSON without loss.

**Option 3** would leave deployment to the mechanism built for moving processes between
servers, with releases and versions. But an application bundle is organised by application,
not by product, and a product manager moving five products would have to find out which
applications hold their processes and move all of those. The catalogue is where the binding
is; the export follows it.

### Consequences

- **Positive:**
  - A catalogue built in the Console moves to another installation as a reviewable file.
  - Products move into another catalogue, with the structure between them.
  - A product's processes travel with it, deployed under the same checks as any deploy.
  - Importing an export again changes nothing twice.
- **Negative / trade-offs accepted:**
  - **Deploying is not undone.** The processes deploy before the catalogue is written; a
    catalogue refused after that (a publish problem, a concurrent edit) leaves them deployed.
    The refusal names them. Everything that can be checked without deploying is checked
    first, so this is limited to publish problems that depend on the deployed models and to a
    store that moved in between.
  - A product offered by a catalogue but maintained by another keeps that home in a catalogue
    export. On an installation without that catalogue, the import refuses it; exporting the
    products and importing them `into` the target catalogue is the way across.
  - Forms (`configForm`), decisions a process calls and the Workers it needs are not carried.
    A form is named by id like a process, and is a follow-up if moving catalogues shows it is
    needed; decisions and Workers are the application bundle's.
  - Only the newest version of a process travels, which is the one a new order runs on.
- **Follow-ups:**
  - Carry configuration forms the same way, if needed.

## Links

- [ADR-0436](0436-a-catalogue-is-imported-as-one-document.md): the document and the import this
  completes.
- [ADR-0129](0129-remote-deployment-targets.md): publishing an application to another server.
- [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md): the lifecycle
  checks a publish runs against deployed processes.
