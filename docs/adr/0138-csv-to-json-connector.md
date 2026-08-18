# ADR-0138: A first-class "CSV to JSON" connector kind with model-authored layout

- **Status:** Accepted
- **Date:** 2026-08-11
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0087](0087-in-process-csv-ingestion.md) made CSV ingestion a step of the
process: a **"CSV einlesen" service task** (reserved job type `io.atlas.csv-import`,
index 11) reads two variables up its scope chain — `csvText` (the raw file) and
`columnConfig` (the column layout) — parses the text with `parseCSVRows`, and writes
`rows` (a JSON array) + `rowCount`. The column layout is supplied by a **separate
preceding script task** (`spaltenkonfig`) that writes `columnConfig` as a FEEL context
literal.

That shape works but has two rough edges surfaced while scaling the example
(`pruefe-datensaetze.bpmn`) to larger files:

1. **The layout lives in a side task, not on the task that uses it.** Authors must
   remember to place *and keep in sync* a `columnConfig` script before every CSV
   import. The task that says "CSV einlesen" carries none of its own configuration —
   its inputs arrive by variable-name convention (`csvText`, `columnConfig`), which is
   invisible in the model and easy to get wrong.
2. **It is not a reusable, discoverable connector.** ADR-0067 established the
   service-task connector catalog and ADR-0081 the marketplace; REST and mail are
   first-class connector *kinds* whose config is authored on the task itself (method,
   URL, from, to, …) and compiled into an interned connector detail read by the
   worker. CSV import never became one — it stayed a bare `zeebe:taskDefinition` type
   plus a convention.

The question: **should CSV-to-JSON become a first-class connector kind — its source,
delimiter, header handling, column layout, and result variable authored on the task
and compiled like REST/mail — while keeping the ADR-0087 variable-convention path
working for already-deployed models?**

## Decision drivers

- **Config belongs on the task (ADR-0067).** Like REST's method/URL and mail's
  from/to, a CSV import's *source variable, delimiter, header handling, columns, and
  result variable* are per-task authored data, interned into the compiled process
  (invariant I5), not a neighbouring script's variable.
- **"Like a function" is the mental model the user has, and it is the right one.** A
  connector kind is a reusable, typed operation: the element template is its typed
  parameter form (dropdowns for enumerated params), the in-process worker its body,
  the service task a call site, the result variable its return. CSV-to-JSON should be
  exactly that.
- **A selection is better than free text where the domain is closed.** Delimiter and
  header-presence are small closed sets; element templates render them as `Dropdown`
  choices (as REST does for method/auth), removing a class of typos.
- **Do not break deployed models.** ADR-0087's `csvText`/`columnConfig` convention is
  in the field (the v4 example). The worker must keep serving it.
- **Reuse the parser; do not fork it.** `parseCSVRows` already handles delimiter,
  header/no-header, per-column type coercion, BOM, ragged rows (ADR-0084). The
  connector must feed it, not reimplement it.
- **Honor the invariants.** Parsing stays a post-fsync worker side effect off the
  single writer, never in `applyToState` (I1/I2/I4). The authored layout is deploy-time
  interned data (I5). Installing the marketplace template only writes catalog/template
  data (ADR-0081) — no engine reach.

## Considered options

**For where the layout is authored:**

1. **A first-class `atlas:csvConnector` detail** compiled onto the task (source,
   delimiter, hasHeader, columns, resultVariable), read by the worker via the
   process lookup — mirroring `atlas:restConnector` / the mail connector. (Chosen.)
2. **A thin marketplace template over the existing job type** that only pins
   `taskDefinition` type `io.atlas.csv-import`, leaving `columnConfig` in a script
   task. Rejected as the end state: it makes the task *discoverable* but not
   *self-configuring* — the side-task rough edge remains.
3. **Keep the variable convention only** (ADR-0087 as-is). Rejected: it is the status
   quo the user asked to improve.

**For the column layout when a header row is present:**

A. **Derive columns from the header row** when the author supplies no explicit column
   list — every header cell becomes a string field. (Chosen.)
B. **Always require an explicit column list.** Rejected as the default: for the common
   "just turn this CSV into JSON" case, retyping the header names is friction.

**For backward compatibility:**

- **Both paths, connector wins** (chosen): the worker prefers the compiled connector
  detail; with none, it falls back to the `csvText`/`columnConfig` scope variables
  (ADR-0087). vs. **new path only**, which would strand deployed models.

## Decision outcome

Chosen: **option 1 (a first-class `atlas:csvConnector` detail), with header-derived
columns (A) and both compatibility paths (connector detail preferred, variable
convention as fallback).**

- **A new connector kind, wired like the others.** A moddle type `atlas:csvConnector`
  is preserved by the modeler; the compiler parses it off a service task into an
  interned `CsvConnectorDetail{Source, Delimiter, HasHeader, Columns, ResultVariable}`
  and assigns the existing reserved `io.atlas.csv-import` job type. The worker obtains
  the detail from the compiled process by the same process-lookup mechanism the DMN,
  mail, and REST workers use — not from ioMapping, not from a convention variable.
- **The worker builds a `csvConfig` from the detail and calls `parseCSVRows`.** Source
  text is read from the authored source variable (default `csvText`) up the scope
  chain; delimiter, header flag, and columns come from the detail. When the detail
  has a header and no explicit columns, `parseCSVRows` derives the columns from the
  header row (see below). The result is written to the authored result variable
  (default `rows`), plus `rowCount`.
- **Header-derived columns.** `parseCSVRows` gains one mode: with `hasHeader` true and
  an empty column list, it synthesizes one string column per distinct, non-empty
  header cell (first occurrence wins), so a bare "CSV to JSON" needs no column
  authoring. An explicit column list still wins and still selects/renames/types
  columns exactly as before; a headerless file still requires indexed columns.
- **Backward compatibility.** With no `atlas:csvConnector` detail, the worker behaves
  exactly as ADR-0087: read `csvText` + `columnConfig`, write `rows` + `rowCount`. The
  v4 example keeps running unchanged until it is migrated to the connector.
- **Marketplace entry.** A curated catalog package `community.csv-to-json`
  (kind `service-task`, ADR-0081) ships the element template: **Source** (String,
  FEEL), **Delimiter** (Dropdown: comma/semicolon/tab/pipe), **Header** (Dropdown:
  header row / no header), **Columns** (String, optional), **Result variable**
  (String). Installing it writes template data only; the worker that runs it is the
  in-process one already compiled in.

Option 2 is kept as the *migration story* (a deployed model can adopt the connector
incrementally), not the destination. Option B and new-path-only are rejected as above.

### Consequences

- **Positive:** a CSV import is authored end-to-end on one task, with dropdowns for the
  closed-set fields; the separate `columnConfig` script task disappears; the layout is
  visible and versioned in the model (I5); adding it followed the ADR-0067 connector
  recipe (moddle type + compiler branch + worker read) with no new job type and no
  engine-invariant change; deployed ADR-0087 models keep working via the fallback.
- **Negative / trade-offs accepted:** a second way to configure CSV import now exists
  (connector detail and the variable convention), a deliberate overlap for
  compatibility, to be retired once models migrate; header-derived columns are all
  typed `string` (explicit columns remain the way to coerce number/integer/boolean);
  the worker gains a small branch to choose detail-vs-convention.
- **Follow-ups:** migrate `pruefe-datensaetze.bpmn` to the connector and drop its
  `spaltenkonfig` script; author the connector in the Modeler's Implement panel
  (ADR-0067 catalog) rather than hand-XML; a per-column type/rename UI in the template
  once header-derivation covers the simple case; large-file streaming (ADR-0087's open
  item) is unchanged.

## Links

- builds on [ADR-0087](0087-in-process-csv-ingestion.md) (in-process CSV ingestion;
  this ADR promotes its `io.atlas.csv-import` task to a first-class connector and keeps
  its variable convention as the compatibility fallback)
- follows [ADR-0067](0067-service-task-connector-catalog.md) (the connector-kind
  recipe: moddle type + compiler branch + one worker) and its authored-endpoint shape
- distributes via [ADR-0081](0081-community-marketplace-for-connectors-and-tasks.md)
  (the marketplace package is an element template; install writes template data only)
- reuses the `parseCSVRows` layout/coercion of [ADR-0084](0084-csv-batch-validation.md)
- honors the six invariants (`../architecture/invariants.md`): parsing is a post-fsync
  worker side effect (I1/I2/I4), the authored layout is interned deploy-time data (I5)
