# ADR-0087: In-process CSV ingestion — upload in a user task, parse in the process

- **Status:** Accepted
- **Date:** 2026-07-30
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0084](0084-csv-batch-validation.md) delivered CSV batch validation by
**ingesting the file at the edge**: a `POST /instances-from-csv` endpoint parsed
the upload and *started* a process instance already seeded with `rows`, and the web
UI grew a dedicated **Datenprüfung** screen to drive that endpoint.

Reviewing it against the original story — *"as a Quality Manager I want a process in
which, after it starts, a user task lets me upload the CSV, and then the machine
runs"* — the ingestion sat in the wrong place. The upload was a **side-channel** (a
separate screen and endpoint) rather than a **step of the process**, and the column
layout lived in that screen's form rather than in the model. The instance was born
mid-flight (already holding `rows`) instead of starting empty and *asking* for the
file. The question: **can the upload and the parsing be steps of the process, so the
data is handled entirely in the model — the file arriving through an ordinary
user-task form?**

## Decision drivers

- **The upload is process work.** Starting the instance, collecting the file, and
  parsing it should be modeled steps, not an out-of-band screen — so the flow is
  visible, versioned, and recoverable like any other process.
- **The column layout is process data.** "Which columns, which delimiter" belongs in
  the model (a task), not in a UI form or an HTTP request.
- **Reuse, don't fork.** The existing CSV parser and the whole validation/correction
  machinery (ADR-0084/0086) must be reused unchanged.
- **Self-completing.** Once the file is provided, the pipeline must run to the first
  human touch-point with no external worker — Atlas's in-process-worker property.

## Considered options

1. **In-process ingestion** — the process starts, parks at a **"CSV hochladen" user
   task**; completing it yields a `csvText` variable; a **script task** sets the
   column layout (`columnConfig`); a reserved **CSV-import service task** parses
   `csvText` against `columnConfig` into `rows`; then the existing multi-instance
   validation/correction runs. (Chosen.)
2. **Keep the edge endpoint as the only path** (ADR-0084 as-is). Rejected: the upload
   stays a side-channel, the layout stays out of the model.
3. **Client-side parse in the upload task** — the browser parses the file and submits
   `rows`. Rejected: the parsing (and the layout) leave the process — "the data is
   handled in the process" is the point.

## Decision outcome

Chosen option: **Option 1.** The example process `pruefe-datensaetze.bpmn` becomes:

```
start
 → userTask   "CSV hochladen"      (form csv-upload-form → csvText)
 → scriptTask "Spaltenkonfiguration" (FEEL sets columnConfig — the layout lives here)
 → serviceTask "CSV einlesen"       (io.atlas.csv-import: csvText × columnConfig → rows, rowCount)
 → subProcess  (multi-instance validation + correction, unchanged)
 → end
```

- **The upload is a user task.** form-js's filepicker holds the picked file
  client-side (as object URLs), not its bytes, so the Tasks app reads the selected
  file **as text** on completion and submits it as the `csvText` variable — no
  side-channel. This makes the upload an ordinary task step; the instance starts
  empty and asks for the file.
- **The layout is a script task.** A FEEL context literal
  (`columnConfig = {columns: […], delimiter: ","}`) writes the predefined layout as a
  process variable — the config is *in the process*, editable in the model.
- **The parsing is a reserved service-task worker.** `CsvImportJobType`
  (`io.atlas.csv-import`, reserved index 11) is served by one in-process worker across
  every process, like the DMN/mail/clio/rest workers. It reads `csvText` and
  `columnConfig` up the task's scope chain and writes `rows` + `rowCount` with the
  **same parser** the endpoint uses (`parseCSVRows`). A raw FEEL script task cannot do
  this — FEEL does not parse delimited text — which is why the parsing is a service
  task while the *layout* is the script task the story called for.

The `POST /instances-from-csv` endpoint from ADR-0084 **remains** as a programmatic
bulk-import API, but it is **no longer the primary, UI-facing path**; the dedicated
Datenprüfung screen is removed. This ADR supersedes ADR-0084's "ingestion endpoint +
dedicated screen" as the default UX, not its architecture (rows → multi-instance →
DMN → correction is unchanged).

### Consequences

- **Positive:** the upload, the layout, and the parsing are all modeled steps —
  visible, versioned, recoverable; the layout lives in the process; no new endpoint
  or bespoke screen; the parser and the entire validation/correction machinery are
  reused verbatim; the instance starts empty and asks for the file, exactly as the
  story described.
- **Negative / trade-offs accepted:** a new reserved job type (one worker) is added;
  two ways to ingest now exist (the in-process task and the retained endpoint), which
  is a small conceptual overlap kept deliberately so an automated/bulk importer still
  has an API.
- **Follow-ups:** author the CSV-import task and its `columnConfig` in the Modeler's
  Implement panel (today it is authored as a `zeebe:taskDefinition` type + a script
  task); a first-class filepicker→variable binding in the Tasks app instead of the
  read-file-as-text convention; a large-file streaming path (the file is read whole
  into a variable).

## Links

- supersedes the ingestion-UX of ADR-0084 (CSV batch validation); reuses its parser
  and pipeline
- builds on ADR-0028 (user tasks & forms), ADR-0086 (scope-chain gateway conditions)
- follows the reserved in-process connector-worker pattern of ADR-0067 (service-task
  connector catalog), ADR-0079 (mail)
