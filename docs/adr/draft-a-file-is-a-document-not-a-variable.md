# ADR-DRAFT: A file is a document, and a variable holds a reference to it

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-04
- **Deciders:** Atlas maintainers
- **Open question:** how a remote Worker Instance reads a document's bytes — over the
  HTTP API with its worker token, or over the job stream it already holds — has not been
  settled. Each changes a different surface (an HTTP route a worker token reaches, or the
  job protocol), and neither has been measured against a 35 MiB document on a slow link.
  Slices 1 to 4 of the plan do not need the answer; slice 5 does.
- **Question checked:** 2026-11

## Context and problem statement

A process that receives a file keeps it in a process variable today, because there is
nowhere else in Atlas to keep it.

The case that brought this up: the example process *CSV-Datensätze prüfen*
([ADR-0087](0087-in-process-csv-ingestion.md)) asks a person to upload a CSV file in a
user task. The Tasks app reads the picked file as text and completes the task with it as
the string variable `csvText` (`api/web/app.js`, the file-field branch of task
completion — hard-wired to that one name). The Operations view then had to learn to
shorten such values ([pblumer/atlas#1556](https://github.com/pblumer/atlas/pull/1556)):
it fixed what a person sees, not where the bytes live.

What a variable costs is already written down in the code and in two records:

- `limits.Variable` (1 MiB by default) says: *"A variable is a business record … Anything
  larger than this is a document, and a document does not belong in a token's scope,
  where every touch rewrites the whole of it into the log"*
  ([ADR-0294](0294-a-variable-is-a-record.md)).
- Every write of a variable is an event in the WAL, a live value in state, and a full
  copy in the per-step variable history (`cfVariableSnapshot`,
  [ADR-0048](0048-per-step-variable-snapshots.md)). A file therefore lands at least three
  times on disk, and again on every change. The event exporter
  ([ADR-0114](0114-opensearch-event-exporter.md)) ships it to OpenSearch as well.
- A binary file can only enter a variable as base64, a third larger, and only up to the
  variable budget.

Atlas already moves documents in some places, but always *between two outside systems*,
never into its own keeping:

- `limits.Document` (35 MiB) bounds a document a Worker carries without it becoming a
  variable — the mail Worker's `export-attachment` into the S3 Worker
  ([ADR-0478](0478-mail-documents-leave-the-mailbox.md)), through the `DocumentStore`
  interface in `connector/mail`.
- The S3 Worker ([ADR-0442](0442-s3-object-store-worker.md)) hands out presigned links so
  a 40 MB PDF reaches a person without passing through Atlas.
- The Paperless-ngx and SharePoint Workers reach document systems a customer already runs.
- The Tasks app sends a catalogue logo straight to the catalogue's own endpoint
  (`logoKatalog`) so it never becomes a variable.

All of them need an outside service. An installation that runs Atlas as what it says it is
— one binary, no database, no broker — has no place for a file a person uploads in a
form, so the file goes into a variable.

The question: **where does a file a process receives live, and what does the process hold
instead?**

## Decision drivers

- **The log and the state stay sized by business records, not by files.** Recovery time,
  WAL compaction, checkpoint size and the exporter must not grow with uploaded files.
- **The invariants hold** ([invariants](../architecture/invariants.md)): `applyToState`
  stays deterministic and free of I/O (I4); nothing is visible before it is durable (I2);
  the single writer stays the only writer of state (I3).
- **It works in a bare installation.** No object store, database or document system may
  be required for a form to accept a file.
- **Reading a document is no easier than reading the variable that names it.** The
  instance visibility rule ([ADR-0275](0275-instance-visibility.md)) applies, and a
  reference must not become a capability anyone holding the string can use.
- **What the instance owns, the instance's lifecycle governs.** History retention
  ([ADR-0115](0115-history-retention-hard-delete.md),
  [ADR-0144](0144-per-definition-history-ttl.md)) removes a document with the last
  instance that refers to it; a whole-instance snapshot
  ([ADR-0109](0109-full-instance-snapshot.md)) carries it.
- **Existing models keep running.** A process that reads `csvText` today still works.
- **Workers can read it**, in process and on a Worker Instance.

## Considered options

1. **Keep files in variables and raise the variable budget.** The strongest form of this
   option: it is the smallest change, everything a file touches is replayed, versioned
   and exported with the instance, and nothing new can go missing. It fails the first
   driver by construction — each touch rewrites the file into the WAL and the history —
   and the budget exists precisely because a 1 MiB record was already judged the limit
   (ADR-0294). Raising it moves the cost onto every installation, not only onto the
   processes that carry files.
2. **Keep files in variables as base64**, as the S3 Worker's `get-object` does. Option 1,
   a third larger, plus binary support. Same objection.
3. **Require an outside store** (S3, SharePoint, Paperless) and let the variable hold its
   key. The strongest form: Atlas keeps no bytes at all, the operator's existing store
   owns backup and retention, and presigned links already keep large files off Atlas. It
   fails the third driver: a form's file field would only work where an S3 Worker is
   configured, and the upload — a person in the Tasks app — has no Worker to run it. It
   stays the right answer for *archiving*, which is why option 4 keeps a path to it.
4. **A document store inside Atlas, in the data directory, and a reference in the
   variable.** Files are stored once, by content hash, outside the log; the process holds
   a small JSON reference. Moving documents on to S3, SharePoint or Paperless remains the
   Workers' job.
5. **BPMN data objects or data stores as the carrier.** A data object is variable-shaped
   by design ([ADR-0053](0053-first-class-data-objects.md)) and is written to the log the
   same way, so it inherits the budget. A data object *holding a reference* is fine and
   needs nothing from this record; a BPMN data store is a modelling concept, not storage.

## Decision outcome

Proposed: **option 4**, a document store in the data directory, with a reference in the
variable. Option 3 stays available as the way a process moves a document out of Atlas.

### The reference

A document is named in a variable by an ordinary JSON object:

```json
{
  "$document": "doc_7Q2…",
  "name": "mitarbeitende.csv",
  "contentType": "text/csv",
  "size": 1164,
  "sha256": "9f2c…"
}
```

- It is a JSON variable (`VarJSON`), not a new variable kind. The WAL format, the engine,
  the compiler and FEEL do not change; FEEL can read `name`, `size` and `contentType`
  like any context, which is enough for a gateway such as `size < 5000000`.
- `$document` is the marker every surface recognises: Operations and the replay show a
  document card with a download link instead of a value; the variable audit and the
  exporter carry the reference, never the bytes.
- The id is random, not the hash. Two people uploading the same bytes get two documents
  with their own authorization, while the store keeps the bytes once (see below).

### The store

- A new directory `documents/` under the data directory, registered in
  `api/storeregistry.go` as `classRuntime` — it is carried by the whole-instance snapshot
  (ADR-0109) and not by the design-time backup (ADR-0107), the same as the log.
- Bytes are stored once per SHA-256, written temp file → `fsync` → rename, the
  `api/sidecar` discipline. A small metadata record per document id holds name, detected
  and declared content type, size, hash, uploader, creation time, state and the
  instances it is bound to.
- The store is written by the API server, never by the processor. No event carries bytes,
  and `applyToState` never touches a document (I4).
- Each document is bounded by `limits.Document` (35 MiB), the budget Atlas already has
  for a person's document. Uploads are refused below the storage guard's reserve
  ([ADR-0481](0481-storage-guard.md)).

### Lifecycle: pending, bound, released

1. **Pending.** An upload creates a pending document owned by the uploader. Only the
   uploader may read it. A pending document that is never bound is removed after a TTL
   (proposed: 24 hours).
2. **Bound.** A document becomes bound to a **root process instance** at the boundary
   where a variable value enters the engine through the server: task completion,
   instance start, message publication, an operator's variable change, a job completion.
   The server sees each of these requests, finds `$document` references in the
   variables, checks that the caller may use each one (it owns it while pending, or may
   read an instance it is bound to), and records the binding **after** the command is
   durable (I2). A call activity's child is bound through its root instance; a copy of
   the reference inside one instance needs no binding.
3. **Released.** A document is deleted by an off-loop sweep once no instance it is bound
   to exists any more — after history retention purged the last one. The purge event
   itself does not delete files (I4); the sweep observes its result. Deleting the bytes
   waits until no document id refers to the hash.

### Who may read a document

- Pending: the uploader.
- Bound: whoever `instanceAccessFor` lets read a bound instance (ADR-0275). A person
  holding a user task reads it only if a field of that task's form holds the reference.
- A reference in hand is not enough. The id is unguessable, but the check is the instance,
  not the secret.
- A document a browser would run as a page (HTML, SVG, XML) is served with
  `Content-Disposition: attachment`, and the stored content type is the detected one, as
  ADR-0478 already does for exported attachments.

### Routes (to be declared in `api/openapi.go` and classified for MCP)

| Route | Purpose |
|---|---|
| `POST /api/v1/documents` | Upload one document (streamed request body, name and type in headers); answers the reference. |
| `GET /api/v1/documents/{id}` | Download, with the authorization above. |
| `GET /api/v1/documents/{id}/meta` | The reference and its state, without the bytes. |

For MCP, reading the metadata is a tool. Whether an agent may download bytes, and how
many, is decided in the slice that adds it; it is not proposed here.

### What changes elsewhere

- **Tasks app:** a form's file field uploads the file first and completes the task with
  the reference under the field's own key. The hard-wired `csvText` branch becomes the
  compatibility path for the existing example; the `logoKatalog` path is unchanged.
- **CSV import worker:** reads a `$document` reference as well as `csvText`, and parses
  the file as a stream instead of a string.
- **S3 and mail Workers:** gain the store as a source and a target — export a document
  to S3, put a mail attachment into the store instead of a variable — through the
  `DocumentStore` seam that already exists.
- **Operations, replay, variable audit:** show a document card for a reference.

### What this does not solve

The parsed result can still be large. `csv-import` writes every row into one variable
`rows`; a file of a few thousand lines reaches the 1 MiB variable budget whether the file
itself was a variable or a document. Processing large files needs reading in pages (a
cursor over the document, one multi-instance batch per page), which is a separate decision.

### Consequences

- **Positive:** the log, the state, the history and the export stay sized by records; a
  file is stored once, whatever the number of steps that pass its reference on.
- **Positive:** binary files become possible in forms and processes without base64.
- **Positive:** existing models are unaffected; the reference is plain JSON.
- **Negative:** Atlas keeps bytes outside the log for the first time for runtime data. A
  document is not replayed, so its durability is the store's own write discipline, and a
  restore must carry `documents/` with the log or references dangle. The whole-instance
  snapshot has to include it from the first slice.
- **Negative:** the binding is enforced at the server's boundaries, not by the engine.
  Every route through which a variable value enters must call it, and a route added
  later that forgets it produces a reference only its uploader can read — a safe failure,
  but a failure.
- **Negative:** a new data class in the data directory to size, monitor and back up.

## Links

- [ADR-0294](0294-a-variable-is-a-record.md) — the variable budget and why a document does
  not belong in a token's scope
- [ADR-0048](0048-per-step-variable-snapshots.md) — every change is kept in full
- [ADR-0087](0087-in-process-csv-ingestion.md) — the CSV upload as a user task; its listed
  follow-ups (file-field binding, large-file path) are what this record takes up
- [ADR-0442](0442-s3-object-store-worker.md), [ADR-0478](0478-mail-documents-leave-the-mailbox.md)
  — `limits.Document`, the `DocumentStore` seam, detected types and disposition
- [ADR-0275](0275-instance-visibility.md) — who may read an instance
- [ADR-0314](0314-portal-personal-data.md) — a reference, not the content
- [ADR-0107](0107-backup-and-restore.md), [ADR-0109](0109-full-instance-snapshot.md),
  [ADR-0115](0115-history-retention-hard-delete.md), [ADR-0481](0481-storage-guard.md)
- Plan: [docs/planning/documents-instead-of-variables.md](../planning/documents-instead-of-variables.md)
