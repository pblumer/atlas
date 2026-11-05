# Documents instead of variables — delivery plan

**Decision record:** [ADR-draft-a-file-is-a-document-not-a-variable](../adr/draft-a-file-is-a-document-not-a-variable.md)
(Proposed). **Status of this plan:** not started; nothing below is built.

## Outcome

A file a process receives — a CSV upload in a form, a PDF from a mailbox — is stored once
in Atlas's own document store. The process variable holds a small JSON reference
(`{"$document": "doc_…", "name", "contentType", "size", "sha256"}`). The log, the state,
the per-step variable history and the event export carry the reference, never the bytes.

The example *CSV-Datensätze prüfen* is the first process to work this way.

## Ground rules for every slice

- `applyToState` never reads or writes a document (I4). No event carries document bytes.
- A binding is recorded only after the command that carries the reference is durable (I2).
- The store is written by the API server off the run loop; a scan over documents or
  instances runs through `readOffLoop` (I3, ADR-0239).
- Every new route is declared in `api/openapi.go`, classified in
  `mcp/tool_registry_drift_test.go`, and every message a person reads goes through the
  locale catalogue (de first, then en).
- `CHANGELOG.md` per user-facing slice; the ADR's *As built* note when a slice lands.

## Slices

### S0 — Decide

- Decisions 1–4 below are taken; decision 5 is proposed and awaits confirmation.
- Exit: ADR status `Accepted`, `Implementation: Not started`.

### S1 — Store and routes

- `documents/` in the data directory for metadata, registered in
  `api/storeregistry.go` as runtime data outside the default whole-instance snapshot;
  the snapshot carries it only when asked to.
- Byte backend `local` (default): content-addressed blobs (SHA-256) under
  `documents/blobs/`; metadata per random document id; temp → `fsync` → rename.
- Byte backend `s3`: a server option naming an existing S3 Worker and bucket; object key
  is the hash; download may answer with a presigned link. A document keeps the backend it
  was written to.
- Server option for the pending TTL, default 24 hours.
- `POST /api/v1/documents` (streamed, `limits.Document`), `GET /api/v1/documents/{id}`,
  `GET /api/v1/documents/{id}/meta`. Pending documents readable by the uploader only.
- Content-type detection; HTML, SVG and XML served as attachment (as ADR-0478).
- Upload refused below the storage guard's reserve (ADR-0481).
- Pending TTL sweep.
- **Tests:** upload/download round trip at the size limit and one byte over; a second
  user gets 404 for a pending document; identical bytes from two users share storage and
  not authorization; a crash between temp write and rename leaves no visible document;
  a snapshot carries `documents/` only when asked to; a restore without them shows
  *document not available* and does not fail; the same suite passes against the `s3`
  backend (against a local S3-compatible test server).

### S2 — Binding, authorization and release

- One function that finds `$document` references in a variable set and binds them to a
  root instance, called by every route through which a variable value enters: instance
  start, task completion, message publication, variable change, job completion (in
  process and remote). A test enumerates those routes so a new one cannot skip it.
- Reading a bound document follows `instanceAccessFor` (ADR-0275), including the
  task-holder rule (the field must be on the task's form).
- Release sweep: delete documents whose bound instances no longer exist; delete bytes when
  no document id refers to the hash.
- **Tests:** a reference the caller may not use is refused at task completion, before the
  command is submitted; restart after binding keeps the binding; history retention purge
  followed by the sweep removes the document; a document bound to two instances survives
  the purge of one; the sweep is idempotent.

### S3 — Forms, Operations and replay

- Tasks app: a file field uploads first and completes the task with the reference under
  the field's own key. The `csvText` branch stays for models that still read it; the
  `logoKatalog` branch is unchanged.
- Operations overview, single-instance view, value window and replay: a reference renders
  as a document card (name, type, size, download).
- **Tests (e2e):** upload in a task form, reference in the instance, download from
  Operations; a user without access gets no link.

### S4 — CSV import reads documents

- `connector/csvimport` accepts a `$document` reference beside `csvText` and parses the
  file as a stream.
- The example `pruefe-datensaetze` uses the reference; its handbook card says so.
- **Tests:** the same file yields the same `rows` from `csvText` and from a document; a
  missing or unreadable document raises an incident naming the variable.

### S5 — Workers move documents

- Settle the open question in the ADR: how a remote Worker Instance reads document bytes
  (HTTP with its worker token, or the job stream). Measure with a 35 MiB document.
- S3 Worker: export a document into a bucket, import an object as a document.
- Mail Worker: store an attachment as a document instead of a variable.
- **Tests:** in-process and remote round trips; a Worker without access to the instance
  cannot read the document.

### S6 — Remaining surfaces

- MCP: a metadata tool; decide whether and how much an agent may download.
- Personal data: a document declared personal is enciphered under the subject's key
  (ADR-0314).
- Handbook (de/en), key-features review.

## Decisions (2026-11-05)

1. **Store:** both backends — internal (`local`) is the default, S3 selectable per
   installation. *Taken.*
2. **Size limit:** reuse `limits.Document` (35 MiB). *Taken.*
3. **Pending TTL:** 24 hours, configurable. *Taken.*
4. **Backup:** documents are in the whole-instance snapshot only when asked for. *Taken.*
   Assumption to confirm: "asked for" is an option of the snapshot (per installation or
   per snapshot request), not a per-process setting.
5. **`csvText`:** *proposed, awaiting confirmation* — keep it as the CSV import's source
   variable, accepting a text or a document reference, with no end date; end only the
   Tasks app's hard-wiring of every file into `csvText`. See the ADR's *Compatibility*
   section for why nothing needs phasing out.

## Out of scope, named so it is not forgotten

- **Large parsed results.** `rows` is still one variable under the 1 MiB budget. A CSV
  file of a few thousand lines fails there whether or not the file is a document. Reading
  a document in pages (cursor, one multi-instance batch per page) needs its own decision.
- Versioning a document, editing it in place, full-text search.
- Virus scanning beyond the inspection ADR-0478 already defines for exported attachments.
