# ADR-0548: Bulk validation evaluates a decision per row inside one job, not one token per row

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-11-09
- **Deciders:** Atlas maintainers

## Context and problem statement

A quality manager wants to check a file of records against business rules — is the
address well-formed, is the record in the right group, does it hold the right licence —
and correct the ones that fail. [ADR-0084](0084-csv-batch-validation.md) built that
story with what the engine already had: parse the CSV into a `rows` variable, run a
multi-instance embedded subprocess over it, evaluate a DMN decision per row in a
business rule task, and park every failing row on a correction user task. That design
is right for the files it was built for: a few hundred rows that a person will look at
one by one.

The case that prompts this record is a file of **50,000 records**. The ADR-0084 shape
does not carry it. The reasons are structural, not tuning:

1. **The rows cannot be one variable.** A variable is a record and is bounded by
   `limits.Variable`, 1 MiB by default ([ADR-0294](0294-a-variable-is-a-record.md)).
   50,000 rows of even 100 bytes each are about 5 MB of JSON. Since
   [ADR-0543](0543-a-file-is-a-document-not-a-variable.md), the *file* is a document and
   no longer needs to fit, but the parsed `rows` the CSV import writes still do
   (ADR-0543 lists "parsed CSV results larger than one variable" as out of scope).
2. **The output collection makes every iteration cost more than the one before.** A
   multi-instance activity's `outputCollection` is rebuilt whole once per iteration: on
   each, `applyToState` parses the stored collection and serialises it again to set one
   element (`engine.setListElement`, measured in *Measured* below). The work per file
   therefore grows with the square of the row count. It is done on the run loop, the one
   writer of the partition, so every other instance on it waits. `limits.Collection`
   (16 MiB) bounds the peak in memory and, as its comment in `limits/limits.go` says,
   deliberately not this cost. An earlier version of this record estimated 125 GB
   *written to the log* for 50,000 rows; Slice 0 showed that wrong. The event carries
   the one element, so the log grows almost linearly (about 4 KB a row); the square
   shows in time and allocation instead.
3. **Every row is several steps of engine state.** Each iteration activates a subprocess,
   a business rule task, a job, a gateway and an end event, and each of those is an event
   in the log, a record in the state and a step in the per-step history. With
   [ADR-0066](0066-decision-evaluation-records.md), each evaluation also retains its
   inputs, outputs and temis trace. 50,000 rows are therefore hundreds of thousands of
   events and 50,000 retained traces, all written through the single writer of one
   partition ([ADR-0005](0005-group-commit-and-fsync-strategy.md)). Slice 0 counted 22
   events and one evaluation record per row.
4. **The iteration budget admits it, so nothing stops it early.** `limits.Iterations` is
   100,000 ([ADR-0276](0276-iteration-budget.md)), and `limits.TokenSteps`
   ([ADR-0272](0272-execution-budget.md)) is a rate budget per token that a wide fan-out is
   built not to trip. A 50,000-row run is admitted and then costs what points 2 and 3 say.
5. **Correction does not scale as a person's work either.** If 3 % of rows fail, that is
   1,500 user tasks. No quality manager works 1,500 tasks one form at a time. What they do
   with that many is filter, sort and fix a file.

The question this record answers: **how does Atlas check tens of thousands of records
against business rules that a business user maintains as DMN, with a durable and
auditable result, without one token per row?**

## Decision drivers

- **Rules stay business content.** The decision is a deployed DMN model, bound and
  versioned like a business rule task's ([ADR-0063](0063-dmn-decision-binding.md),
  [ADR-0398](0398-a-business-rule-task-can-call-a-decision-service.md)). No second rule
  language.
- **The engine records the run, not every row.** The log, state and history should
  grow with the number of *decisions a person takes*, not with the number of rows in a
  file.
- **The result must still be auditable.** For every row it must remain answerable which
  verdict it got, from which decision version, on which input file.
- **No invariant is bent.** Evaluation runs off the run loop. The engine sees one job
  completion whose variables are references and counts, so replay stays deterministic.
- **The small case stays as simple as it is.** ADR-0084's per-row correction loop is the
  right tool for a few hundred rows and must keep working unchanged.

## Considered options

1. **Keep ADR-0084's shape and raise the limits.** Make the CSV import read pages of a
   document, fix the quadratic collection, and let multi-instance carry 50,000 iterations.
2. **Chunked multi-instance.** Split the document into pages of, say, 1,000 rows; run a
   multi-instance activity over the *pages*; each iteration is one job that evaluates its
   page's rows in memory and writes a partial result.
3. **One bulk-validation job.** One task reads the whole document, evaluates the decision
   per row in memory, writes a result document with a verdict per row, and returns only
   counts, a reference and a bounded sample of failures. Correction then branches on the
   size of the failure set.
4. **Leave it to an external data-quality tool.** Atlas orchestrates, and a dedicated tool
   (an ETL or data-quality product) validates.

## Decision outcome

Chosen option: **Option 3, one bulk-validation job**, with the correction path chosen by
the number of failures and with Option 2 kept as the planned extension if Slice 0 shows a
single job to be too long. Slice 0 did not: see *Measured*.

### Measured (Slice 0)

`benchmarks/bulkvalidation_test.go`, written up with the raw output in
[`benchmarks/results/measurement-95bce7d.md`](../../benchmarks/results/measurement-95bce7d.md).
One shared cloud VM (Xeon 2.10 GHz, 4 cores, Go 1.26.0); the absolute numbers are that
machine's, the shapes are the finding. Repetitions varied by 5 to 10 %.

| | 1,000 rows | 10,000 rows | 50,000 rows |
|---|---:|---:|---:|
| ADR-0084 multi-instance, start to completion | 2.9–3.4 s | 184 s | cannot start; projected about 70 min |
| ADR-0084: log written | 4.5 MB | 80 MB | projected about 1.2 GB |
| ADR-0084: events / evaluation records | 22,012 / 1,000 | 220,012 / 10,000 | — |
| ADR-0084: heap allocated | 1.4 GB | 142 GB | projected about 3.5 TB |
| One job, streamed file (22-rule table) | — | 0.5 s | 2.3–2.6 s |

- **The multi-instance cost is quadratic, and the profile says where.** Time fits
  1.15 ms × n + 1.72 µs × n² to within 3 % at 2,000 and 5,000 rows. At 2,000 rows, 70 %
  of CPU time is the run loop, and 57 % is one call chain: finishing an iteration sets
  one element of the output collection by parsing and re-serialising all of it
  (`finishMultiInstanceIteration` → `setListElement` → `expr.ParseJSON` and
  `encoding/json.Marshal`). The 50,000-row figures are an extrapolation of that fit, not a
  run: 50,000 rows of the example are about 3.2 MB of JSON and are refused as a start
  variable by `limits.Variable` before anything runs.
- **One decision costs 12 µs for the example rule and 52 µs for a 22-rule FIRST table
  with an e-mail pattern.** Asking for the trace costs nothing extra, because
  `dmn.Registry.Evaluate` computes it and discards it. temis also scans every rule of a
  FIRST table rather than stopping at the first match.
- **One job per file is far inside the lease.** 50,000 rows through the core of
  `validate-rows` (parse, evaluate, write the result row) take 2.3–2.6 s and 18 MB of heap,
  200,000 rows 8–9 s. The one-minute threshold of *Long runs* is reached near 1.2 million
  rows of this rule, which is about where `limits.Document` (35 MiB of CSV at 30 bytes a
  row) stops the file anyway. A rule several times heavier than the 22-rule table would
  reach the threshold first; that, not the row count, is what to watch.

### What the job does

A new operation of the CSV Worker Type ([ADR-0087](0087-in-process-csv-ingestion.md),
[ADR-0139](0139-csv-to-json-connector.md)) — working name `validate-rows` — takes:

- `source`: a variable holding a document reference (ADR-0543), the uploaded file;
- the column layout, as the CSV import already declares it;
- a decision reference with the same binding a business rule task has (`decisionRef`,
  deployment, version or latest, decision or decision service);
- an input mapping per row: how a row's columns become the decision's input context. It
  is the same FEEL mapping a business rule task's inputs use, evaluated with `row` bound
  to the parsed row;
- which decision output says *valid*, and which outputs are the *reasons*.

It then:

1. **Resolves the decision once**, at the start of the job, to one deployment key, and
   evaluates every row against that key. A latest-bound decision that is redeployed
   mid-run does not split one file across two rule versions.
2. **Streams the rows.** It parses one row, evaluates it with `dmn.Registry.Evaluate`
   (the call the DMN worker already makes off the processor goroutine), writes one
   result row and drops the input. Memory holds the current row and the output being
   written, not 50,000 maps. It requests no trace per row (see *Audit*).
3. **Writes a result document**: the original columns plus `_row` (the line number in
   the source), `_valid`, the reason columns, and `_error` for a row the decision could
   not evaluate (a FEEL error, a missing input). The result is CSV, so the person who
   uploaded a CSV can open it in the tool they used to make it. It is stored through the
   document store and bound to the instance (ADR-0543).
4. **Completes the job** with one variable — the summary — and nothing row-shaped
   beyond a bounded sample:

   ```json
   {
     "total": 50000, "valid": 48512, "invalid": 1476, "errors": 12,
     "decision": {"id": "RowValid", "deploymentKey": 2251799813685321, "version": 4},
     "source": {"$document": "doc_…", "sha256": "…"},
     "result": {"$document": "doc_…", "name": "pruefung-ergebnis.csv", "size": 6123456},
     "failures": [{"_row": 17, "email": "…", "reasons": ["emailOk"]}]
   }
   ```

   `failures` holds at most `failureSample` rows (default 200) and is cut to stay within
   `limits.Variable`. The full set is the result document.

### Correction, chosen by size

The process model decides, with an ordinary exclusive gateway on `summary.invalid`:

- **Few failures** (at most a threshold the model states, for instance 200): a
  multi-instance user task over `summary.failures` — ADR-0084's correction loop,
  unchanged, but over the failures instead of every row. Each corrected row is
  re-checked by a business rule task against the *same* deployment key the summary names.
- **Many failures:** one user task offers the result document as a download, filtered
  to the failing rows, and accepts a corrected file as an upload. The corrected file is
  validated again by the same operation, and the loop ends when `invalid` is 0 or a
  person accepts the remainder with a reason.

Atlas does not pick the threshold: it is a property of the people doing the work, not of
the engine, so it lives in the model. `failureSample` is a property of the task for the
same reason. Its real ceiling is `limits.Variable`: the failures must fit one variable,
which at about 100 bytes a row is in the order of ten thousand.

### Writing the corrections back

Token-driven correction ends with corrected *rows*, but the person needs a corrected
*file*, the one they will load into the system the records came from. A second operation
of the same Worker Type, working name `apply-corrections`, closes that loop. It takes:

- the source document;
- the corrected rows, each carrying the `_row` it replaces. This is the multi-instance
  output collection of the correction loop, bounded by the same `failureSample`;
- optionally, a list of `_row` numbers to drop (a record a person rejected rather than
  corrected).

It streams the source, replaces or drops the named rows, writes a new document and binds
it to the instance. The source document is never changed: a document is immutable
(ADR-0543), and keeping both is what makes the correction reviewable. The operation
refuses a `_row` that is not in the source and a row whose columns differ from the
source's layout, so a correction cannot add or reshape records unnoticed. The model may
then run `validate-rows` once more over the new document as the final check.

The whole flow for a quality manager is then:

1. pick the file in a form (a document, ADR-0543);
2. `validate-rows` checks every row and writes the result document;
3. the failures, if few enough, become correction tasks, one token each;
4. `apply-corrections` writes the corrected file, and a last `validate-rows` confirms it.

### Audit

- The result document is the per-row record: every row, its verdict, its reasons, and
  the decision deployment key and source SHA-256 in the summary that references it.
  With content addressing, the same source file and the same deployment key produce the
  same result, which makes a re-run checkable.
- The run is retained as **one** decision evaluation record in the sense of ADR-0066:
  the summary as output, the binding and the source reference as input, and no trace.
  A trace per row is exactly the volume this record exists to avoid. A person who wants
  to know why row 17 failed reads its reason columns, or tries that row in the decision
  editor ([ADR-0326](0326-trying-a-decision-before-it-runs.md)).
- Per-row traces on request (for example for the first *n* failures) are a follow-up,
  not part of this decision.

### Where it runs

In process first. The decision models are held by the server's `dmn.Registry`. A Worker
Instance cannot evaluate a deployed decision today, because nothing ships the model to
it, and building that is a separate decision ([ADR-0203](0203-worker-execution-model.md)
governs where a Worker Type may run). The document is read through the job's own
instance, as every in-process document read is (ADR-0543), so a model cannot validate a
file that its instance was not given.

### Long runs

A single job that runs for minutes must not look dead, and today nothing lets it say it
is alive. The in-process runner holds a job for a fixed lease, `job.DefaultLease`, five
minutes ([ADR-0007](0007-job-worker-protocol.md)), and nothing extends it. A handler
that outlives the lease does not fail: the job is offered again, a second run starts,
and the first run's completion is refused by its stale lease epoch. The result is
correct, because the same source file and deployment key produce the same result, but
the work is done twice. The operation therefore:

- honours cancellation of the instance between rows;
- repeats from the first row after a server restart, because it keeps no progress;
- has to finish well inside the lease.

That is acceptable only while a whole run is short. The threshold is about a minute, a
fifth of the lease, leaving room for a slower server: **a run that would take longer
needs Option 2**, pages as iterations, each well inside the lease, so a restart resumes
at a page rather than at row one. Slice 0 measured 2.3–2.6 s for 50,000 rows, so
Option 2 is not needed for the files this record is about. Extending a lease from
inside a handler would be a change to ADR-0007's model and is not proposed here.

### Scale-out readiness

Assessed against [`docs/architecture/scale-out-readiness.md`](../architecture/scale-out-readiness.md):
**requires follow-up**, with the boundaries below kept now.

- **State owner:** the instance and its one job are partition-owned. The run itself
  keeps no durable state of its own; the result document is in the document store and
  the summary is an ordinary job completion. Nothing new is shared mutable state.
- **Routing:** the job is leased like any other job of its instance. The decision is
  resolved by deployment key, which is design-time metadata every node loads, not a
  host-local choice.
- **Failure and retry:** a restart or an elapsed lease repeats the run. Fencing by lease
  epoch refuses a stale completion (ADR-0007), and the same source and deployment key
  give the same result document, so a duplicate run is wasteful but not wrong.
- **Remaining dependency:** with the document store's local backend, the result document
  lives in one node's data directory (ADR-0543), and evaluation needs the decision model
  on the node that runs the job. A multi-node deployment needs the S3 backend for
  documents and, if validation is to run outside the server, decision models for Worker
  Instances. Both are named follow-ups, not assumptions of this record.

### As built (Slice 1)

Slice 1 is built, with one change to where the operation lives. This record proposed
`validate-rows` as an operation of the CSV Worker Type. It is a **business rule task over
a file** instead: an ordinary business rule task carrying `<atlas:rowsFrom>`.

```xml
<bpmn:businessRuleTask id="check">
  <bpmn:extensionElements>
    <zeebe:calledDecision decisionId="QualityRow" resultVariable="pruefung"/>
    <atlas:rowsFrom source="antrag" delimiter=";" columns="email,group,license"
                    validOutput="valid" failureSample="200"/>
    <zeebe:ioMapping>
      <zeebe:input source="=lower case(row.email)" target="email"/>
    </zeebe:ioMapping>
  </bpmn:extensionElements>
</bpmn:businessRuleTask>
```

Why the change: everything that makes a decision *bound* — the three bindings, the
deploy gate that refuses a model whose decision does not exist, the pin of `latest` at
deploy, the version pin, the impact analysis of a decision reference, the decision
picker and input mapping in the Modeler — is written against the business rule task.
A CSV operation would have had to re-implement all of it or widen every one of those
places to a second element type. The file layout, by contrast, is four attributes.
Putting the small thing on the large thing reuses the large thing whole; the CSV Worker
Type keeps its own job of turning a file into rows.

What it does, as built:

- **Compiler** (`compiler/scope_compile.go`, `decisionRows`): reads `<atlas:rowsFrom>`
  and refuses at deploy what a run would otherwise meet on its first record — no
  source, a source that is not a variable name, a delimiter that is not one
  character, a headerless file without columns, a column listed twice, a sample
  outside 0–10,000, a missing `resultVariable`, and a temis worker on the same task
  (one network call per record is what this exists to avoid).
- **Reading** (`connector/csvimport/stream.go`, `EachRow`): one record at a time, with
  the same column resolution as `ParseRows` (extracted, not copied), the record's line
  in the file, and its cells as written.
- **Evaluating** (`dmn/rows.go`): in the in-process DMN worker. Inputs are the input
  mappings with the record bound as `row`, or, without mappings, the record's fields by
  name; static inputs are the base either way. The first record's answer pins the
  decision deployment for the rest of the file. A record the decision fails on, or
  whose verdict is not a boolean, is counted as an error and written with its message;
  it does not fail the run.
- **Result document**: the file's own columns, then `_row`, `_valid`, one `_<output>`
  column per other decision output, and `_error`, in the file's delimiter, stored as
  `<name>.result.csv` and bound to the instance with the job's completion. It is written
  as the records are checked, not held as rows.
- **Summary** (the result variable): `total`, `valid`, `invalid`, `errors`, `decision`
  (`id`, `deploymentKey`), `source`, `result`, `failures` (each with `_row` and the
  reasons) and `truncated`. The sample is cut to `failureSample`, then further to the
  variable budget.
- **Audit**: one decision evaluation record for the run, inputs `{source, records}`,
  outputs the counts and the result reference, no trace.
- **Modeler**: the business rule task's panel has *Over a file* with the fields above;
  the moddle descriptor knows `atlas:RowsFrom`, so the Modeler keeps it on save.

Not yet:

- **Cancellation between records.** The in-process job handler has no context to
  cancel; an instance cancelled mid-run has its completion refused, and the run itself
  finishes. With the measured 2.5 s for 50,000 records that is acceptable; it is not
  for a run near the lease.
- **Formats other than CSV.** The CSV Worker Type's fixed-width and attribute-value
  formats are not read here.
- **S2 and S3**, below.

### Slices

- **S0 — measure (done).** A benchmark of temis evaluating a realistic quality rule per
  row, of one streamed file through the core of `validate-rows`, and of the ADR-0084
  process at 1,000 to 10,000 rows: events, log and state bytes, evaluation records,
  allocation, wall-clock time. Results under *Measured*; they confirm Option 3.
- **S1 — the operation (done, as a business rule task over a file; see *As built*).**
  In process: streaming evaluation, result document, summary, one evaluation record.
  Modeler panel and handbook.
- **S2 — the correction paths.** `apply-corrections`; an example model with both
  branches, from upload to corrected file; the file correction task with a filtered
  download and re-upload.
- **S3 — the Datenprüfung screen.** Show the summary and the result document instead of
  1,500 open tasks.

### Consequences

- **Positive:**
  - The log, state and history grow by one job and one summary per file, regardless of
    its size.
  - The quadratic collection and the 1 MiB `rows` variable are never involved.
  - Rules stay DMN, maintained and versioned as before.
  - One file is checked against one decision version, recorded in the result.
  - The small case keeps ADR-0084's loop, now pointed at the failures only.
- **Negative / trade-offs accepted:**
  - Per-row decisions are no longer engine records. A row's verdict lives in a document,
    not in Operations' timeline, and has no trace.
  - A server restart mid-run repeats the whole run, and a run that outlives the
    in-process lease is done twice (until Option 2 exists).
  - Evaluation is in process only. A Worker Instance cannot run it.
  - The result is only as reliable as the document store; a lost result document leaves
    a summary that names bytes that are gone (ADR-0543 answers 410 for those).
- **Follow-ups / risks to watch:**
  - The quadratic re-serialisation of `outputCollection` remains a defect for every other
    wide multi-instance loop, and Slice 0 located it (`engine.setListElement`). It runs
    in `applyToState`, so a fix changes how state is stored and must replay identically
    (invariant 4); it needs its own record and is not addressed here.
  - `dmn.Registry.Evaluate` builds the trace it throws away. An evaluation without one
    would be cheaper for `validate-rows`; how much is not measured.
  - Decision models for Worker Instances, if validation has to run outside the server.
  - Per-row traces on request.
  - Formats other than CSV (XLSX is what most quality managers actually receive).

## Pros and cons of the options

### Option 1 — keep ADR-0084's shape and raise the limits

- Good, in its strongest form: every row is a first-class engine record with its own
  trace, retry and timeline. Operations can show any row's decision without opening a
  file. No new operation, no second way to validate. Fixing the quadratic collection is
  owed anyway, and once fixed, the remaining cost is linear.
- Bad: linear is still hundreds of thousands of events and 50,000 retained traces for
  one file, through one partition's single writer, competing with every other instance
  on it. The per-row records buy auditability that a result document provides at a
  fraction of the cost. 1,500 correction tasks are not a workable user interface. This
  option scales the engine's bookkeeping with the data instead of with the decisions.

### Option 2 — chunked multi-instance

- Good, in its strongest form: progress is durable per page, so a crash at row 40,000
  loses one page, not the run. Pages can run in parallel. The engine records 50 jobs
  rather than 50,000 and stays far from every budget. This is the "seitenweises Lesen"
  that reading a document in pages suggests.
- Bad: it needs page-wise document reads, a way to merge partial results into one
  result document, and a collection of page summaries. That is more moving parts than
  Option 3, for a benefit — restart cost — that matters only if a single run is long.
  Whether it is long is exactly what S0 measures, so it stays the planned extension
  rather than the first step.

### Option 3 — one bulk-validation job (chosen)

- Good: the smallest engine footprint; reuses the document store, the CSV parser, the
  DMN registry and the existing binding; the result is a file the user can work with.
- Bad: as listed under *Consequences*: no per-row engine records, whole-run restart,
  in process only.

### Option 4 — an external data-quality tool

- Good, in its strongest form: purpose-built tools profile, deduplicate and validate at
  millions of rows, with rule libraries Atlas will never have. Atlas would only start the
  run and wait for the answer, as it does with any external system.
- Bad: the rules leave DMN and the business user's hands for a second rule language and
  a second tool to operate, license and secure. For checks that are already a DMN table
  (well-formed, in the right group, holding the right licence), that is a large cost for
  no gain. It remains the right answer for profiling or matching across files, which is
  not this decision's question.

## Links

- extends [ADR-0084](0084-csv-batch-validation.md) (CSV batch validation) for large files;
  its correction loop is reused for few failures
- builds on [ADR-0543](0543-a-file-is-a-document-not-a-variable.md) (documents),
  [ADR-0087](0087-in-process-csv-ingestion.md) and [ADR-0139](0139-csv-to-json-connector.md)
  (CSV Worker Type), [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md),
  [ADR-0039](0039-dmn-io-variable-mappings.md), [ADR-0063](0063-dmn-decision-binding.md)
  and [ADR-0398](0398-a-business-rule-task-can-call-a-decision-service.md) (decisions)
- relates to [ADR-0066](0066-decision-evaluation-records.md) (evaluation records),
  [ADR-0077](0077-multi-instance-activities.md) and [ADR-0074](0074-embedded-subprocesses.md)
  (multi-instance), [ADR-0007](0007-job-worker-protocol.md) (leases), [ADR-0276](0276-iteration-budget.md),
  [ADR-0272](0272-execution-budget.md), [ADR-0291](0291-one-place-for-budgets.md) and
  [ADR-0294](0294-a-variable-is-a-record.md) (budgets),
  [ADR-0086](0086-gateway-conditions-resolve-over-scope-chain.md),
  [ADR-0203](0203-worker-execution-model.md),
  [ADR-0326](0326-trying-a-decision-before-it-runs.md)
