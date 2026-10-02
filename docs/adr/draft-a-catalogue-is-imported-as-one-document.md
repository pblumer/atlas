# ADR-DRAFT: A catalogue is imported as one document

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

A shop reaches an installation in one of these ways today:

- **One record at a time.** The Console, the REST routes and their MCP tools write one
  catalogue (`POST /api/v1/catalogs`, `PATCH …/{id}`), one product
  (`POST /api/v1/catalog-products`) and one release (`POST …/{id}/releases`) at a time
  ([ADR-0376](0376-catalogue-maintenance-over-mcp.md)).
- **From an ArchiMate model.** `POST /api/v1/catalogs/{id}/import` derives product drafts and
  their composition and aggregation edges (`api/catalog/archimate.go`). It carries no approval,
  no process binding, no variant, no time limit and no audience. Everything arrives as a
  draft.
- **By restoring a backup.** That restores the whole instance, overwrites it, and is an
  administrator's act.

None of these delivers a shop as a thing that can be shipped. An example under `examples/`
can carry processes, decisions and forms, but no catalogue. A product manager who built a
catalogue on a test installation cannot hand it to production except product by product.
A sequence of single writes that fails halfway leaves a half-built shop: catalogues that
offer products that are not there, and products whose home is not yet written.

## Decision drivers

- **All or nothing.** Either the shop arrives whole or nothing changes, with every reason
  given at once.
- **The same authority as the single writes.** An import may not do what the importer could
  not do one record at a time: write a catalogue they do not maintain, take a product from
  another catalogue, or change who maintains a catalogue unless they own it.
- **Repeatable.** Importing the same document twice must not create the shop twice.
- **Checkable without a server.** An example's test must prove the document is valid and
  publishable with no running installation.
- **One surface for every caller.** HTTP, MCP, an example installer and a migration script
  all use the same import.

## Considered options

1. **A catalogue document and one import route.** `{catalogs, products, publish}` posted to
   `POST /api/v1/catalogs/import`, all or nothing.
2. **A client-side installer** that makes the single writes in order. It needs no new API.
3. **Extend the ArchiMate import** to carry what a product needs.
4. **Per-catalogue backup and restore.**

## Decision outcome

Chosen: **option 1.**

**The document** (`catalog.Document`, `api/catalog/document.go`):
- `catalogs` holds whole catalogue records: id, texts, rank, languages, items, groups,
  members, edges.
- `products` holds whole product records, each with its `homeCatalog`.
- `publish` asks for a release of every catalogue in the document.
- IDs are the document's own and must be URL words. Because they are stable, importing the
  document again finds its records and updates them.
- A theme, a logo and pictures are not part of a document. A theme is an administrator's to
  set (ADR-0316), and the other two are binaries with routes of their own.
- `Document.Check` validates the document on its own and is what an example's test runs:
  ids, duplicates, language tags, a missing home, a theme.

**The import** (`POST /api/v1/catalogs/import`, role `productmanager`, MCP
`atlas_import_catalog`) takes two visits to the run loop.

The first visit resolves the document against the store and checks every authority:
- A catalogue that exists must be one the importer maintains. Its member list may change only
  if they own it.
- A new catalogue is owned by the importer.
- A product's home must be a catalogue in the document, or one on the server that the
  importer maintains.
- Moving an existing product away needs the losing catalogue's agreement too.
- A catalogue may offer only products that are in the document or on the server.
- With `publish`, every catalogue's publish problems are computed against the store as the
  import will leave it, including whether its lifecycle processes are deployed.

The second visit **plans again** and only then writes:
- the catalogues, keeping owner, creation time and theme;
- the products, with their revisions moved on;
- the releases computed before the first write.

The second plan is what makes the import safe against a concurrent edit. Whether processes
are deployed is the one thing not asked again, as a single publish asks it on its own visit.

**Status codes.** A refused document writes nothing. The response lists every problem with
its subject (`catalog:<id>`, `product:<id>`):
- **400** for the document itself;
- **403** for a catalogue the importer does not maintain;
- **422** for what publishing would refuse, or a home that does not exist;
- **409** if the store moved between the two visits.

An id an outsider names that another catalogue already holds is refused **403**. The id is
the document's own, so any refusal says it is taken, and 403 says nothing more.

**The answer** is `{created, updated, releases}`.

### Why not the others

**Option 2** adds nothing to the API, which is its strongest point, and the Console already
makes these calls. But it is not atomic: a failure on the seventh write leaves six behind,
and only a person can tell which. It would also hold the shop's rules twice, once in the
server and once in JavaScript in the installer. And it serves only the browser it runs in:
an agent over MCP, a migration script or an example's Go test could not use it.

**Option 3** would make a standard notation the import format. But ArchiMate has no word for
an approval rule, a process binding, a variant, a time limit or an audience. Carrying them
would mean properties of Atlas's own inside an ArchiMate file. That file would be ArchiMate
in name only, and a product manager would author it in a modelling tool that cannot validate
any of it. The ArchiMate import stays what it is: where a catalogue's structure starts.

**Option 4** would move a shop as data, but a backup is a snapshot of an installation's
records, revisions and owners, not a description a person writes and reviews. Restoring one
catalogue from another installation would bring its owner ids, which name accounts that do
not exist here.

### Consequences

- **Positive:**
  - An example can ship a whole shop: `examples/` gains catalogue documents.
  - A product manager can move a catalogue between installations as a reviewable file.
  - A refused import changes nothing and names every problem.
  - Importing again is safe.
- **Negative / trade-offs accepted:**
  - Readable ids can collide. A second document, or another team's import, that names an id
    already taken is refused rather than merged.
  - There is no export yet, so a document is written by hand or assembled from the routes'
    answers.
  - Pictures, logos and a theme are set separately.
  - The write is all or nothing against refusals, not against the disk. A write error after
    the first record leaves the records before it, as any sequence of sidecar writes would,
    and answers 500.
- **Follow-ups:**
  - **Export**, e.g. `GET /api/v1/catalogs/{id}/document`, so a shop built in the Console
    becomes a document.
  - **Pictures** carried by reference, if examples need them.

## Links

- [ADR-0312](0312-portal-catalogue-order-inventory.md): catalogues, products and releases.
- [ADR-0315](0315-portal-roles-and-responsibilities.md): a product's home, and who maintains
  a catalogue.
- [ADR-0376](0376-catalogue-maintenance-over-mcp.md): the single writes over MCP.
- [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md): the lifecycle
  checks a publish runs.
