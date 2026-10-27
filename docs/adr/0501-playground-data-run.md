# ADR-0501: A data run checks a whole file against a model in throwaway sandboxes, and hands the file back

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-27
- **Deciders:** Patrick Blumer
- **Open question:** Two figures this record's defaults rest on are not measured:
  - how fast a sandbox runs a model with no stubs, no pools and decisions evaluated in-process;
    ADR-0215's 2 ms per case was measured with stubs and pools;
  - how much memory the spreadsheet library's streaming reader and writer hold for a sheet of
    70 000 rows.

  The chunk size and the parallelism below are chosen on those two gaps.
- **Question checked:** 2026-10

## Context and problem statement

Quality work on operational data often starts from an export rather than from a system: a
server list, an account list, a licence list, passed around as a spreadsheet. A data-quality
check is then a row-by-row question. Is the hostname well formed? Does the owner's department
exist? Is the end-of-life date in the past while the server is still marked productive? The
answer belongs back in the file: as a verdict beside each row, or as the corrected value in
the cell. The deciders ran exactly this on another engine, one process instance per row,
started locally from a file of about **70 000 rows** of server data. Files of that order are
the normal case here, and larger ones are possible.

Atlas has two places a file can go today, and neither carries this:

- **The production path** (ADR-0084, ADR-0087, ADR-0139) parses the file into one variable,
  `rows`, of one instance, and checks it in a multi-instance loop (`api/csvupload.go`). That is
  the right path for checks that need a system or a person, with an audit trail, but not for a
  file of this size:
  - A variable is a record, not a document. Its budget is 1 MiB (`limits/limits.go`,
    ADR-0294), and a server export of a few dozen columns passes that at a few thousand rows.
  - Every run leaves its instances in durable history until retention removes them (ADR-0144).
- **The Playground's CSV batch** (ADR-0215) is closer in spirit: one case per row, the real
  engine, nothing durable, nothing that can leave the process. It falls short in four ways:
  - It stops at **50 000 cases** (`maxCasesPerRun`, `api/playground/batch.go`). The bound is
    real: the sandbox keeps every finished case, because the report, the per-case replay and
    the paged results read them, so its footprint grows with the file.
  - It reads CSV only, with every value as text.
  - Its results are a table of its own (inputs, end event, outputs), not the file handed back.
  - **It does not evaluate decisions.** A business rule task creates a job with the reserved
    DMN job type, and in production the in-process DMN worker answers it (`dmn.Handler`). A
    sandbox registers no worker, so the job is answered by a stub or parks. Measured on this
    branch with a one-task model: with no stubs the case stays on the business rule task; with
    the default stubs it completes and the result variable is never written. That contradicts
    `playground/doc.go` and ADR-0215, which both say DMN behaves in a sandbox as it does in
    production. For a data-quality model, whose rules are mostly decision tables, it is the
    whole difference.

The question: **how does Atlas run a model over every row of a file of 70 000 rows or more, with
production semantics including decisions, so that nothing durable is left and nothing outside
is reached, and hand the same file back with each row's verdict and corrections?**

## Decision drivers

- **Every row comes back exactly once, in its place.** A quality report that silently loses or
  reorders rows is worse than none. A row whose case failed is still a row, and says why.
- **Size does not change the design.** Memory and disk are bounded by a slice of the file, not
  by the file. The limit is time and an upload budget, never a row count.
- **Production semantics, decisions included.** The real compiler and processor, FEEL and DMN.
  There is no second evaluator to drift (ADR-0215).
- **Nothing durable, nothing reached.** No version, no instance in Operations, no record in the
  engine's log, no network. A check that needs a system or a person is a process, and runs on
  the production path.
- **The run answers nothing it does not compute.** A stub's answer in a quality verdict would
  be an invented fact stated as a checked one.
- **Reproducible.** The same file, model and reference date give the same file back, whatever
  the slicing.
- **Local.** It runs in the single binary, offline, and from the command line.
- **The data stays the uploader's.** The file may hold personal data. It lives in a temporary
  directory owned by the principal who uploaded it, for as long as the result is wanted.

## Considered options

1. **Raise the Playground's ceiling.**
2. **Production engine, one instance per row**, through a new file start that creates an
   instance per row.
3. **Production engine as ADR-0084 has it**, with the budgets raised.
4. **A data run**: the file streamed through a sequence of throwaway sandboxes, one slice each,
   with each row's result written as its case finishes.
5. **Batch decision evaluation**: evaluate a decision per row, with no process.

## Decision outcome

Chosen: **option 4, a data run**. The prerequisite is that **the sandbox evaluates decisions**,
which is also a fix to the Playground as it stands.

### The prerequisite: a sandbox evaluates decisions

- `playground.Options` gains the decision binding a sandbox evaluates with. The API passes one
  built over the server's DMN registry. A business rule task's job is then answered the way
  production answers it: `dmn.Handler` over the sandbox's own store, its own compiled
  processes and the server's registry. The decision-evaluation record (ADR-0066) lands in the
  sandbox's store, where a case's view can show it.
- Decisions resolve as production resolves them:
  - for the latest and fixed-version bindings, against the decisions deployed on this server;
  - for a deployment-bound decision on a deployed source, against the model deployed with
    that version.
- A **draft's** deployment-bound decision has no deployed snapshot. It resolves to the latest
  deployed decision of that id, and the run says so.
- A decision that cannot be found raises an incident naming it, as in production. It never
  passes quietly.
- A decision bound to a remote temis Worker (ADR-0050) is evaluated locally when the registry
  holds its model, and is otherwise an incident. **A sandbox dials nothing.** That is a
  structural property and stays one.
- **Stubs and decisions.** The `default` and `human` stubs no longer answer business rule
  tasks. A stub an author names for the element itself (`byElement`) still wins, because it is
  an explicit statement.
- **This changes existing Playground runs.** A model with a business rule task now produces the
  decision's result where it produced none. That is the fix, and the behaviour the
  documentation already claims. The changelog says it in those words, because a scenario's
  baseline (ADR-0215) will show the difference.

### The data run

**Where it lives.** In the `playground` package and `api/playground`. It is a third mode in the
Playground panel beside Step and Batch, with the meaning "apply" rather than "test". There is
also a command, `atlas playground data-run`.

**The routes**

- `POST /api/v1/playground/data-runs`: multipart, a `file` (CSV or XLSX) and a `config`. It
  answers 202 with the run's id. The `config` holds:
  - the model source, in the shape the session's open request already has;
  - the sheet (XLSX) or the delimiter (CSV);
  - an optional map from column header to variable name;
  - `resultColumns`;
  - `referenceTime`.
- `GET /api/v1/playground/data-runs/{id}`: progress and summary. Rows read, rows finished, how
  many completed, how many ended in an incident, how many did not finish, how many cells were
  corrected, and the state of the run.
- `GET /api/v1/playground/data-runs/{id}/result`: the file handed back, 409 until the run has
  finished.
- `DELETE /api/v1/playground/data-runs/{id}`: cancels and deletes.

**Slices.** The file is read as a stream and cut into slices of `K` rows, 5 000 by default. Each
slice runs in a sandbox of its own:

1. open the sandbox, with no stubs and with the run's decision binding;
2. start every row as a case;
3. run to rest within the slice's budget;
4. read each case's result and write its output rows;
5. close the sandbox, which deletes everything it wrote.

What a run holds at any moment is bounded by `K`, not by the file. The sandbox's own
reasons to keep every case (report, replay, paging) end with its slice. Up to `P` slices, 2 by
default, run at once. Each is its own sandbox with its own single writer (I3). Their output is
written in slice order, so the file comes back in input order however the slices interleave.

**Rows are independent.** A row's result may not depend on which slice it landed in. So the run
refuses, before reading the file, a model in which a case could wait for another case: a
message catch event, a receive task, a signal catch, an event-based gateway.

**What the run refuses.** The model may hold only what the engine computes:

- allowed: gateways, FEEL script tasks, business rule tasks, timers (on the run's virtual
  clock), embedded subprocesses, and call activities whose callee qualifies too;
- refused: anything that creates a job other than a decision, meaning a service or connector
  task, a send task, a script in another language (ADR-0047), or a user task.

The refusal lists the elements and says why: *this check needs a system or a person; run it as
a process*. That boundary is the one the deciders drew, and it keeps every verdict computed.

**From a row to a case.** Column headers name the variables, as in the Playground's CSV path
today, unless `config` maps a header to another name.

- A header that cannot be a variable name, or one that starts with `atlas.`, is refused at the
  start.
- XLSX cells carry their types: number, boolean, text, and dates as RFC 3339 dates or instants.
- CSV cells are text, except that a column the process declares as a start variable (ADR-0455)
  is coerced to its declared type with the CSV import's own coercion (`coerceCell`,
  `connector/csvimport`).
- A cell that does not coerce stays text. A dirty value reaches the rules dirty, which is the
  point of checking it.
- An empty cell leaves the variable out, so a declared default applies as it would on any
  start.

**What comes back.** The same format, holding every input row once, in input order:

- **The original columns.** A cell keeps its input value unless the case left a different
  final value in the variable of that column. Then the cell holds the corrected value. That is
  how a model corrects data: an output mapping or a script task that writes the column's
  variable.
- **`atlas.state`**: `completed`, `incident` or `unfinished`.
- **`atlas.end`**: the end event reached, or the element the case stopped on.
- **`atlas.message`**: an incident's message.
- **`atlas.corrected`**: one entry `column: old → new` per corrected cell. A correction never
  erases what was there.
- **`resultColumns`**: the variables the request names, in that order. They are named, not
  discovered, because the header is written before the first case runs. A union of every
  variable any case created would need the whole file run before the first line could be
  written.

For XLSX the workbook holds the checked sheet, written with typed cells and never a formula. A
formula cell in the input is read as its cached value and handed back as that value; Atlas
does not recompute a workbook. The workbook also holds a sheet `atlas` with the summary:

- the model and its version or draft revision;
- the reference time;
- the counts by state and by end event;
- the corrections per column.

For CSV, the summary is in the status response.

**Reproducible.** Every slice's sandbox starts its virtual clock at `referenceTime`, which
defaults to the moment of upload and is written into the summary. A check like "the end-of-life
date is in the past" therefore reads one date for the whole file, and the same one again when
the run is repeated with that reference time. No stubs means no draws. The output does not
depend on `K` or `P`.

**Bounds**

- There is no row ceiling.
- A new budget `DataRun` bounds the upload, 256 MiB by default and set through `ATLAS_LIMIT_*`
  like every budget. The existing `DataUpload` of 16 MiB is sized for a Playground CSV and is
  too small for 70 000 rows of a wide export.
- A wall-clock budget bounds a run, one hour by default. Each slice carries the per-case budget
  the batch run already uses.
- One data run per principal and two per server may be in flight. A data run holds none of the
  eight session places.

**Ownership and lifetime**

- Only the principal who started a run may read or delete it, as with a session.
- The input, the slices' sandboxes and the output live in a temporary directory. It is removed
  on `DELETE`, an hour after the run finished, and on shutdown.
- Nothing reaches a durable store or the engine's log.
- Personal values in the file stay as readable as they were uploaded, and only for that hour.
  The sandbox holds no key and enciphers nothing (ADR-0314), exactly as the Playground's CSV
  path does today.

**Who may start one.** Whoever may open a Playground session on the same source.

**XLSX** is read and written with a pure-Go library (excelize, BSD-3-Clause), which keeps
ADR-0010's no-cgo rule. It is used through its streaming row reader and stream writer. The
input is the named sheet or the first one, and the first row is the header.
`THIRD_PARTY_NOTICES.md` gains the entry.

**The command.** `atlas playground data-run --file servers.xlsx --out checked.xlsx` names its
model with `--process` or `--draft`. It uploads, prints progress, and downloads the result.

- It exits 0 when every row completed, whatever end each reached. A failed check is a business
  result, not a failure of the run.
- It exits 3 when any row ended in an incident or did not finish, the code `atlas playground`
  already uses for a run that did not hold up.
- It exits 1 when the run could not be done at all.

**Not over MCP, in this record.** A file of tens of megabytes is not a tool argument, and the
command is the automation path. A tool that reads a run's status could follow without new
decisions.

### Consequences

- **Positive:**
  - A file of any length is checked with production semantics, decisions included, in
    minutes, and comes back as the file it was, plus verdicts and corrections.
  - Nothing durable is left, nothing outside is reached, and no verdict rests on an invented
    answer.
  - The Playground's batch and trial runs gain decision evaluation, which they were documented
    to have and did not.
- **Negative / trade-offs accepted:**
  - **Existing Playground results change** for models with a business rule task, as above.
  - **Real checks against systems are not served here.** On the production path they still
    meet the variable budget for a file this size. One instance per row from a file (option 2)
    is the follow-up that path needs, and it is not decided here.
  - **A new dependency.** Spreadsheets bring a third-party library, a file format with its own
    edge cases, and a budget sixteen times the current upload ceiling.
  - **A model that a person or a system completes cannot run as a data run as it stands.** It
    has to be written for it. Expected for rule checks, and the refusal says which elements
    are in the way.
  - **The per-case replay is not kept.** Once its slice closes, a case's path is gone. A row
    worth examining is examined by running it alone in a session, with the same model and
    reference time.
- **Follow-ups / risks to watch:**
  - Instances per row on the production path, for checks that need a system.
  - A corrections-only output: the rows with a corrected cell, shaped for import into the
    system of record. A corrected copy of an export fixes the copy; the system the export
    came from still holds the error.
  - A fully local mode of the command that compiles the model and reads decisions from files
    without a server.
  - The two figures in the open question, which decide whether `K` and `P` are right.

### What the implementation starts with (ADR-0018)

**Decisions in the sandbox**

- A business rule task in a sandbox writes the decision's result variable, and the default stub
  no longer answers it.
- A `byElement` stub on that task still does.
- A decision that is not deployed raises an incident naming it.
- A decision bound to a remote temis Worker whose model is not in the registry raises an
  incident, and nothing is dialled.

**The data run**

- Every input row appears in the output exactly once, in input order, for any `K` and `P`.
- A row whose case raised an incident carries `incident` and the message.
- A correction keeps the old value in `atlas.corrected`.
- A model with a service task, a user task or a message catch is refused before the file is
  read, and the elements are named.
- An XLSX date cell reaches the case as a date. A declared number column in CSV is a number,
  and a non-numeric cell in it stays text.
- The same file with the same reference time gives byte-identical output twice, and the same
  output with `K` = 7 as with `K` = 5 000.
- A run of 70 000 rows completes. A file over the `DataRun` budget is refused.
- A second run by the same principal while one is in flight is refused.
- After `DELETE`, or the hour, nothing of the run is left on disk.
- No input header beginning with `atlas.` is accepted, and no output formula is written.

**The command**

- It exits 0, 3 and 1 in the three cases above.

## Pros and cons of the options

### 1 — Raise the Playground's ceiling

- Good: one number to change.
- Bad: the footprint grows with every case the sandbox keeps, so a higher number only moves
  the wall, and the session TTL and the paging are sized for the old one. It also leaves the
  file handed back, spreadsheets and decisions unsolved.

### 2 — Production engine, one instance per row

- Good: exactly the pattern the deciders know. Every row is durable, auditable and able to
  call systems and wait for people.
- Bad: 70 000 instances in the log and in history for each run. Operations is crowded with
  throwaway work, and side effects are real, which is what a quick check of an export must
  not risk. Right for checks that need systems, and named as that follow-up.

### 3 — Production engine as ADR-0084, budgets raised

- Good: no new code path.
- Bad: it puts a document into a variable, which ADR-0294 argues against on every write. Each
  touch of `rows` rewrites the whole file into the log. A budget large enough for 70 000 rows
  is no budget for a record.

### 4 — A data run (chosen)

- Good: bounded by a slice, not by the file. Production semantics, nothing durable, nothing
  reached, the file handed back, reproducible, and local.
- Bad: a third Playground mode, a new budget and a new dependency. No replay of a single row
  after the fact, and the model must be written to compute everything it needs.

### 5 — Batch decision evaluation

- Good: the cheapest by far. No instance, no token, no log, and for a pure table of rules it
  answers the same question.
- Bad: it stops at one decision. A check that derives a value, routes on it and then corrects
  a field is a process, and authors draw it as one. It would also be a second surface for the
  same task, and the data run already evaluates decisions at the price of a sandbox per slice.

## Links

- extends [ADR-0215](0215-modeler-playground.md) (the sandbox, its batch and its CSV path),
  and corrects its claim that a sandbox evaluates decisions
- serves the user story of [ADR-0084](0084-csv-batch-validation.md) at a size its path cannot
  take, without replacing that path for checks that need systems or people
- respects [ADR-0294](0294-a-variable-is-a-record.md) (a variable is a record) and
  [ADR-0010](0010-go-and-no-cgo.md) (no cgo)
- relates to ADR-0500, whose trial depends on the same
  decision evaluation
