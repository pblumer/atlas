# ADR-0084: CSV batch validation — upload a file, validate every row against business rules, correct the failures

- **Status:** Proposed
- **Date:** 2026-07-29
- **Deciders:** Atlas maintainers

## Context and problem statement

A quality-management user story: *"As a Quality Manager I want to check records
against business rules — is the email address well-formed, is the record in the
correct group, does it hold the correct licence — and correct the ones that are
wrong."* The records arrive as a CSV with a **predefined column layout**, uploaded
through a form; each record is then checked **individually** against the rules,
and a failing record must be routed back for correction.

Atlas already has almost every ingredient (see the [ROADMAP](../../ROADMAP.md)):

- **Forms** (form-js) and a **start-form → instance** flow that seeds submitted
  data as start variables ([ADR-0028](0028-forms-and-the-tasks-app.md),
  [ADR-0029](0029-public-process-start-links.md)).
- **`VarJSON`** variables that hold an array of row objects and bind back into
  FEEL as a list ([ADR-0037](0037-structured-json-variables.md)).
- **Multi-instance activities** that fan an activity out over a FEEL input
  collection, one iteration per element, collecting an output collection
  ([ADR-0077](0077-multi-instance-activities.md)).
- **Business rule tasks** that evaluate a **DMN decision** (embedded temis) over
  an instance's variables and write the result back
  ([ADR-0014](0014-dmn-business-rule-tasks-via-temis.md),
  [ADR-0039](0039-dmn-io-variable-mappings.md)).
- The **Tasks app** and **user tasks** for the human-correction step
  ([ADR-0028](0028-forms-and-the-tasks-app.md),
  [ADR-0045](0045-user-task-assignment-bound-to-identity.md)).

The one thing that does **not** exist anywhere in the repo is a way to get a CSV
**into** the engine as a collection of typed rows. Every start path today takes a
JSON `{"variables": …}` body; nothing ingests a file, and there is no CSV parser.

The question this ADR answers: **how does a CSV of records become a durable,
per-row validation run against business rules, with the failures surfaced for
correction — without inventing a new engine subsystem or breaking an invariant?**

## Decision drivers

- **Reuse the engine, don't sidestep it.** Validation should be a real process
  instance so every row's decision is durable and auditable
  ([ADR-0066](0066-decision-evaluation-records.md)) — not a throwaway synchronous
  computation.
- **Rules are business content, not code.** "Correct group", "correct licence"
  change without a redeploy of Atlas; a DMN decision table is the right home.
- **No new hot-path work, no new value type.** Rows are `VarJSON`; iteration is
  multi-instance; validation is a business rule task — all existing machinery.
- **The CSV parser is a deploy-/side-effect-time concern**, never on the
  processor batch cycle (invariant 1, invariant 5).
- **Predefined column layout.** The upload must be checked against a declared set
  of columns so a mis-shaped file is caught at ingestion, not mid-process.

## Considered options

1. **Engine-native pipeline** — a server-side upload endpoint parses the CSV into
   a `VarJSON` array and starts a process instance whose model is a
   multi-instance business rule task over the rows; failing rows become user
   tasks in the Tasks app. (Chosen.)
2. **Synchronous validation endpoint** — the server parses the CSV and evaluates
   a decision per row in-line, returning a report, with no process instance.
3. **Client-side ingestion** — parse the CSV in the browser and post the rows to
   the existing JSON start path.

## Decision outcome

Chosen option: **Option 1, the engine-native pipeline**, delivered as a sequence
of vertical slices so each one *runs*:

**Slice 1 — CSV ingestion (this ADR's first implementation).** A new endpoint
`POST /api/v1/processes/{key}/instances-from-csv` accepts `multipart/form-data`
with two parts: the CSV `file` and a JSON `config` describing the **predefined
column layout**. The server parses the CSV against that layout into a list of row
objects and starts an instance of the named definition, seeding the standard
start variables:

- `rows` — a `VarJSON` array of `{columnName: value}` objects,
- `rowCount` — the number of parsed rows,
- `fileName` — the uploaded file's name (metadata for the audit trail).

It reuses the exact single-writer start path (`CreateInstance` + `Drive`) that
`handleCreateInstance` and the public-form start already use — no engine change,
no new value type. The column layout is a request-scoped JSON document:

```json
{
  "hasHeader": true,
  "delimiter": ";",
  "columns": [
    {"name": "email",   "header": "E-Mail",  "type": "string"},
    {"name": "group",   "header": "Gruppe",  "type": "string"},
    {"name": "license", "header": "Lizenz",  "type": "string"}
  ]
}
```

Each `column` maps one CSV column (by `header` when `hasHeader`, else by
`index`) into a row field `name`, coercing to `type` (`string` | `number` |
`integer` | `boolean`). Coercion is **lenient**: a cell that will not coerce is
kept as its raw string so dirty data flows through to be *validated and
corrected* rather than rejected at the door — that is the whole point of the
feature. Structural mismatch (a configured `header` absent from the file's header
row) is a `400`, because the file does not match the declared layout.

**Slice 2 — the validation process + decision (landed).** The example decision
`examples/pruefe-datensaetze.dmn` (`RowValid`: `emailOk · groupOk · licenseOk ·
valid`) and process `examples/pruefe-datensaetze.bpmn` validate each row over
`inputCollection="=rows"`, assembling an `outputCollection` of per-row verdicts.
Two facts shaped the implementation:

- **A business rule task cannot be multi-instanced directly** — the compiler wires
  multi-instance only onto service/script/user tasks, call activities, and
  subprocesses (ADR-0077). So the row check is a business rule task **wrapped in an
  embedded subprocess** that is multi-instanced (ADR-0074 + ADR-0077); the
  subprocess's `outputElement="=verdict"` collects each iteration's decision result.
- **The DMN worker had to learn the scope chain.** It read input mappings against
  the process root only, so a per-iteration `inputElement` (`row`) resolved to null.
  It now resolves up the element's full scope chain (nearest scope wins), mirroring
  the script worker — the ADR-0068 follow-up the ROADMAP called out. This is an
  off-processor read whose result still freezes into the job completion (I6), so
  replay is unaffected.

**Slice 3 — the correction loop (landed).** Inside the per-row iteration, an
exclusive gateway branches on `=verdict.valid`: a valid row ends the iteration; an
invalid one parks on a **user task** (`korrigiere`, bound to the `row-correction-form`)
that the Quality Manager claims in the Tasks app. The task's output mapping
reshapes the submitted fields back into `row` (`={email:email, group:group,
license:license}`), and a **same-scope back-edge** re-enters the business rule task —
re-validating until the row is valid. The whole instance completes only when every
row passes; `verdicts` collects each row's final (valid) verdict.

This required making **gateway conditions scope-aware**
([ADR-0086](0086-gateway-conditions-resolve-over-scope-chain.md)): the in-subprocess
gateway reads the per-iteration `verdict`, which lives in the iteration scope, not
the process root. A business rule task cannot be multi-instanced directly, and the
back-edge stays within the subprocess scope (its join is an exclusive gateway, which
fires per arrival).

**Slice 4 — the upload UI + form pre-fill.** The web app grows a **Datenprüfung**
screen: pick a deployed process, choose a CSV, give the column layout, and upload
it to the Slice-1 endpoint (`multipart/form-data`) — the first file upload in the
buildless UI (ADR-0012). The **pre-fill gap is closed** by giving the correction
task **input mappings** (`row.email → email`, …) that surface its per-row fields
flat in the task's **own element-instance scope**, matching the form's field keys;
its output mapping still reshapes the edits back into `row`. The task list now
carries each task's `elementInstanceKey`, so the Tasks app pre-fills a bound form by
reading that scope's variables (`GET /api/v1/instances/{elementInstanceKey}/variables`,
the existing endpoint) instead of the process root. Later: promoting the column
layout to a reusable, project-scoped **artifact**
([ADR-0034](0034-projects-and-artifacts.md)).

### Consequences

- **Positive:** every row's validation is a durable, replayable decision
  evaluation; the rules live in a DMN table a business user maintains; nothing
  touches the processor hot path or adds a value type; the ingestion endpoint is
  a thin, pure, fully-testable transform in front of the established start path.
- **Negative / trade-offs accepted:** the full user story spans several slices —
  the first push delivers ingestion only, and is honest that the process,
  decision, correction loop, and UI follow. The column layout travels with the
  request (not yet a reusable artifact). A very large CSV is bounded by an upload
  cap and materialises as one `VarJSON` variable; streaming/partitioned ingestion
  of huge files is out of scope here.
- **Follow-ups / risks to watch:** promote the column layout to an artifact;
  per-row parse annotations for the correction UI; a corrected-CSV export;
  fan-out cost of very wide multi-instance validation (Slice 2 will measure it).

## Pros and cons of the options

### Option 1 — engine-native pipeline
- Good: durable + auditable per-row decisions; rules as data; reuses forms,
  `VarJSON`, multi-instance, business rule tasks, and the Tasks app wholesale;
  no engine or hot-path change.
- Bad: the complete story is multi-slice; the first deliverable is plumbing that
  only becomes visibly valuable once Slice 2's process exists.

### Option 2 — synchronous validation endpoint
- Good: a single small endpoint returns a report immediately; least code.
- Bad: sidesteps the engine — no durability, no decision-evaluation audit
  trail, no correction workflow; re-implements orchestration Atlas already owns.
  Rejected as off-strategy for a workflow engine.

### Option 3 — client-side ingestion
- Good: no new server endpoint; reuses the JSON start path unchanged.
- Bad: a browser-parsed CSV is unvalidated and unbounded before it reaches the
  server; the predefined-layout check and type coercion would live in JS with no
  server enforcement. Rejected for robustness; the server owns ingestion.

## Links

- builds on ADR-0028 (forms & Tasks app), ADR-0037 (`VarJSON`),
  ADR-0077 (multi-instance), ADR-0014/0039 (business rule tasks),
  ADR-0066 (decision-evaluation records)
- relates to ADR-0034 (projects & artifacts — future home of the column layout)
