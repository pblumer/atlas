# ADR-DRAFT: A file is a document, and a variable holds a reference to it

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-11-04
- **Deciders:** Atlas maintainers
- **Open question:** how a remote Worker Instance reads a document's bytes — over the
  HTTP API with its worker token, or over the job stream it already holds — has not been
  settled. Each changes a different surface (an HTTP route a worker token reaches, or the
  job protocol), and neither has been measured against a 35 MiB document on a slow link.
  Slices 1 to 4 of the plan do not need the answer; slice 5 does.
- **Question checked:** 2026-11
- **Decided (2026-11-05, product owner):** the store is internal by default with S3 as a
  selectable backend; `limits.Document` is the size limit; unbound uploads expire after
  24 hours, configurable; documents enter the whole-instance snapshot only when a
  snapshot asks for them (per snapshot, not per process); `csvText` as under
  *Compatibility* below.

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

Chosen: **option 4**, a document store inside Atlas with a reference in the variable,
whose bytes live in the data directory by default or in an S3 bucket when the installation
chooses it. Option 3 stays available as the way a process moves a document out of Atlas.

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

- **Metadata is always local.** A small record per document id in a new directory
  `documents/` under the data directory holds name, detected and declared content type,
  size, hash, uploader, creation time, state, the instances it is bound to, and where its
  bytes are. Written temp file → `fsync` → rename, the `api/sidecar` discipline.
- **Bytes have two backends**, chosen per installation:
  - `local` (the default): `documents/blobs/` under the data directory, one file per
    SHA-256, the same write discipline. A bare installation needs nothing else.
  - `s3`: a bucket reached through an S3 Worker the installation already defines
    (ADR-0442) — its name, never its credentials, in the server's configuration. The
    server resolves it in its own S3 registry, as the mail Worker's export does in process
    (ADR-0478). The object key is the hash. A download may then answer with a short-lived
    presigned link instead of streaming the bytes through Atlas.
  - Switching backend applies to new documents; a document keeps the backend it was
    written to, which its metadata names. Moving existing documents is a separate,
    explicit operation, not a side effect of a configuration change.
- **Backup is opt-in.** `documents/` is registered in `api/storeregistry.go` as runtime
  data, but outside the default whole-instance snapshot (ADR-0109): a snapshot carries the
  documents only when it is asked to, because documents can make it many times larger and
  not every use case needs them back. The design-time backup (ADR-0107) never carries them.
  A snapshot without documents restores references whose bytes are absent; every surface
  that resolves a reference then says *document not available* rather than failing
  silently, and a Worker raises an incident naming the variable. With the `s3` backend, the
  bucket's own versioning and backup are what protect the bytes.
- The store is written by the API server, never by the processor. No event carries bytes,
  and `applyToState` never touches a document (I4).
- Each document is bounded by `limits.Document` (35 MiB), the budget Atlas already has
  for a person's document. Uploads are refused below the storage guard's reserve
  ([ADR-0481](0481-storage-guard.md)).

### Lifecycle: pending, bound, released

1. **Pending.** An upload creates a pending document owned by the uploader. Only the
   uploader may read it. A pending document that is never bound is removed after a TTL:
   24 hours by default, set by a server option.
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
  the reference under the field's own key. The `logoKatalog` path is unchanged.
- **CSV import worker:** its source variable may hold a CSV text, as today, or a
  `$document` reference; it parses the file as a stream instead of a string.
- **S3 and mail Workers:** gain the store as a source and a target — export a document
  to S3, put a mail attachment into the store instead of a variable — through the
  `DocumentStore` seam that already exists.
- **Operations, replay, variable audit:** show a document card for a reference.

### Compatibility: `csvText`

Today the Tasks app writes every picked file, as text, into the variable `csvText`,
whatever the field is called. The example's upload form already names its file field
`csvText`, so the rule above — the reference goes under the field's own key — puts the
reference exactly where the CSV import already looks. Nothing has to be phased out:

- `csvText` stays the CSV import's default source variable and accepts **both** a text
  (API callers and scripted starts that pass the file inline) and a reference. Accepting
  a text costs one type check; refusing it would break callers for no gain, and the
  variable budget already keeps such a text under 1 MiB.
- What ends is the **hard-wiring**: a file field named anything else no longer lands in
  `csvText`. A model that relied on that — a file field under another key feeding a CSV
  import that reads `csvText` — stops working at the version that ships slice 3. None of
  the shipped examples does; installations' own models are not known. The release note
  says so, and the CSV import's incident names the variable it found empty.

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
- **Negative:** a new data class in the data directory to size, monitor and back up —
  and, because the snapshot carries it only on request, a restore can bring back
  references without their bytes. The design makes that visible, not impossible.
- **Negative:** two byte backends to test; the `s3` backend's durability is the bucket's,
  not Atlas's.

## As built (slices 1–4, 2026-11-05)

- **Store:** `api/docstore` — metadata records under `documents/meta`, the `local` backend
  under `documents/blobs` (one file per SHA-256), the `s3` backend (`docstore.Bucket`)
  through `connector/s3.Documenter`, which gained `GetDocument` and `DeleteDocument`
  bounded by `limits.Document`. A bucket is not listed; the store keeps a journal of the
  hashes it wrote there (`documents/written.json`) so the sweep can delete what no record
  names. `documents/` is `classRuntime`, `onDemand` and `optIn` in `api/storeregistry.go`;
  `GET /api/v1/backup/full?documents=true` carries it, and a restore accepts it whenever
  an archive does.
- **Routes:** `POST /api/v1/documents`, `GET /api/v1/documents/{id}`,
  `GET /api/v1/documents/{id}/meta` (`api/documents.go`). A download is served with
  `nosniff`, `Content-Security-Policy: sandbox` and, for a type a browser would run, as an
  attachment. A missing blob answers 410. No presigned link is handed out: every
  download passes the access check.
- **Binding:** `checkDocumentRefs` runs before the command at instance start (the
  `startInstanceGuarded` funnel), task completion, job completion, the operator variable
  change and the trigger route; `bindDocuments` records the root instance once `drive()`
  returned. Messages, public links and wizards, order actions and tool-call arguments
  refuse references (`refuseDocumentRefs`). `TestEveryVariableEntryChecksDocumentReferences`
  reads the package's source and fails for a function that parses variables without one
  of the two.
- **Sweep:** every 15 minutes, off the run loop, through a read view; an instance counts
  as existing while it is live or in history.
- **Configuration:** `--documents-backend` (`local`|`s3`), `--documents-s3-worker`,
  `--documents-s3-bucket`, `--documents-s3-prefix`, `--document-pending-ttl` and their
  `ATLAS_*` variables.
- **Console:** a file field uploads and completes with the reference under its own key;
  Operations, the single-instance view and the replay show a reference as a download link;
  the Settings page offers the snapshot with documents.
- **CSV import:** the source variable may hold text or a reference; the in-process worker
  reads the document off the run loop. The lease payload for a Worker Instance is resolved
  on the run loop, where reading a document is not allowed, so a Worker Instance's CSV job
  fails naming the document until slice 5.
- **Not built yet:** slice 5 (Worker Instances reading documents, S3 and mail Workers
  moving documents), slice 6 (MCP tools — the three routes are listed as omitted for now,
  personal-data encipherment of documents, the handbook). A public start form or answer
  link cannot carry a file yet: its visitor is nobody the store knows.

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
