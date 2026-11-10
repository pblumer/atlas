# How much does a file of tens of thousands of records cost to validate?

Evidence for one design question, not a performance baseline.

[ADR-0548](../../docs/adr/0548-bulk-validation-evaluates-a-decision-per-row-in-one-job.md)
proposed validating a large file in **one job** that evaluates a DMN decision per row,
instead of one multi-instance iteration per row as
[ADR-0084](../../docs/adr/0084-csv-batch-validation.md) does. It rested on arithmetic and
named the gap as its open question: what one decision evaluation costs, and what one
iteration of the ADR-0084 shape costs in events, bytes and time. It also set a threshold:
if a 50,000-row run in one job takes more than about a minute, build chunked
multi-instance instead. This is the measurement.

- Raw capture: [`measurement-95bce7d.txt`](measurement-95bce7d.txt)
- Benchmarks: [`../bulkvalidation_test.go`](../bulkvalidation_test.go)
- Machine: shared cloud VM, Intel Xeon @ 2.10 GHz, 4 cores, Go 1.26.0, virtual ext4 disk.
  Read the [caveats](README.md#what-a-baseline-is--and-is-not). The absolute values are
  this machine's; the **shapes** are what this document is for. Repeated runs varied by
  5 to 10 %.

## The answer

**One job per file, as proposed.** 50,000 rows take 2.3–2.6 s through the core of the
proposed `validate-rows` operation, well inside the one-minute threshold. The ADR-0084
process cannot run that file at all (the rows exceed the variable budget), and at the
sizes it can run, its cost grows with the square of the row count.

The ADR's own estimate of the ADR-0084 cost was wrong in one respect: it predicted about
125 GB *written to the log* for 50,000 rows. The log grows almost linearly. The square
shows in time and allocation on the run loop instead, which is worse for everybody else
on the server, because the run loop is the partition's only writer.

## What was measured

| Benchmark | One op is | What it stands for |
|---|---|---|
| `BulkValidationDecisionPerRow` | one decision evaluation | the unit both designs pay once per row |
| `BulkValidationStreamedFile` | one whole CSV file | the core of `validate-rows`: parse a row, evaluate, write the result row |
| `BulkValidationMultiInstance` | one instance over *n* rows | the ADR-0084 process, through the real server and its in-process DMN worker, durable WAL |

Two decisions: the example `RowValid` from `examples/pruefe-datensaetze.dmn` (one rule,
FEEL in the outputs), and `QualityRow`, a 22-rule FIRST table with an e-mail pattern,
twenty groups and a licence per group, closer to a real quality check. The synthetic file
fails about 5 % of its rows.

The multi-instance process is ADR-0084's subprocess without the upload and parse steps
in front: the rows arrive as a start variable, and every row is valid, so no row parks on
a correction task. The ADR-0084 process is therefore measured at its cheapest.

## One decision

| Decision | Time per row | Allocated per row | Trace |
|---|---:|---:|---:|
| `RowValid` (example) | 12–13 µs | 7.4 KB, 83 allocations | 420 B |
| `QualityRow` (22 rules) | 46–56 µs | 39 KB, 238 allocations | 4.6 KB |

- **Typing the model does not change this.** After the measurement, both decisions were
  given typed input data and a structured result type (`itemDefinition`). Re-measured,
  one evaluation allocates one object more and takes the same time within the spread.
- **Asking for the trace costs nothing extra.** `dmn.Registry.Evaluate` calls
  `EvaluateTraced` and discards the trace, so "plain" and "traced" are the same work.
  An evaluation that does not build a trace would be cheaper; how much is not measured.
- **temis scans every rule of a FIRST table.** The trace of a row that matched rule 3
  lists all 22 as evaluated. Rule count, not position of the match, sets the cost.

## One file, one job

| Rows | Source | Result document | Time | Per row | Heap in use |
|---:|---:|---:|---:|---:|---:|
| 10,000 | 0.3 MB | 0.4 MB | 0.45–0.54 s | 45–53 µs | 8 MB |
| 50,000 | 1.5 MB | 2.1 MB | 2.1–2.6 s | 43–52 µs | 18 MB |
| 200,000 | 6.1 MB | 8.7 MB | 8.2–9.3 s | 41–46 µs | 53 MB |

- **Linear, and the decision is almost all of it.** Per row it is the evaluation's 50 µs
  plus CSV reading and writing.
- **The heap figure includes the result buffered in memory.** In the benchmark the
  result is written to a `bytes.Buffer`; written to a temp file, as the operation would,
  it stays out of the heap.
- **Where the threshold lies.** One minute at about 50 µs a row is about 1.2 million rows.
  `limits.Document` (35 MiB) stops a CSV of 30 bytes a row at about the same count. A rule
  several times heavier than `QualityRow` reaches the threshold first. That is the case to
  watch, not the row count.

## The ADR-0084 process

| Rows | Start to completion | Per row | Events | Evaluation records | Log written | Heap allocated |
|---:|---:|---:|---:|---:|---:|---:|
| 1,000 | 2.9–3.4 s | 2.9–3.4 ms | 22,012 | 1,000 | 4.5 MB | 1.4 GB |
| 2,000 | 9.2 s | 4.6 ms | 44,012 | 2,000 | 9.7 MB | 5.5 GB |
| 5,000 | 47 s | 9.5 ms | 110,012 | 5,000 | 30 MB | 35 GB |
| 10,000 | 184 s | 18.4 ms | 220,012 | 10,000 | 80 MB | 142 GB |

The 2,000- to 10,000-row runs ran once each (`-count=1`); 1,000 rows ran four times.

- **Time is quadratic.** It fits 1.15 ms × n + 1.72 µs × n². Fitted on 1,000 and 10,000
  rows, the model predicts 2,000 and 5,000 rows to within 0.4 % and 2.7 %. Doubling the file
  roughly quadruples the time.
- **Allocation is quadratic, almost purely.** About 1.4 KB × n². Each step of 2× multiplies
  it by 4.
- **The log is nearly linear.** About 4.1 KB a row plus 0.4 B × n². An iteration's event
  carries its own element, not the collection.
- **22 events and one evaluation record per row, exactly**, plus 12 for the instance.
- **The state directory's growth is not a reliable number here**: Pebble buffers in its
  memtable and compacts, so its size on disk lags what was written. It is in the raw file
  and not interpreted.

### Where the time goes

CPU profile of the 2,000-row run (13.2 s of samples):

| Share | Call chain |
|---:|---|
| 70 % | `api.(*Server).loop`: the run loop, the partition's single writer |
| 57 % | `engine.finishMultiInstanceIteration`, which sets the iteration's output element |
| 50 % | `engine.setListElement` → `setVariableElement` in `applyToState` |
| 27 % | `expr.ParseJSON` of the stored collection |
| 15 % | `encoding/json.Marshal` of it again |

Finishing an iteration sets one element of the output collection by parsing the whole
stored collection and serialising it again. That is O(n) per iteration and O(n²) per
instance. It runs in `applyToState`, so a fix changes how state is stored and must replay
identically (invariant 4). That is its own decision, named in ADR-0548 as a follow-up.

### 50,000 rows

The ADR-0084 process **cannot start** with 50,000 rows: the example's rows are about
64 bytes of JSON each, 3.2 MB in all, and `limits.Variable` (1 MiB) refuses the start
variable. Extrapolating the fits anyway, for scale and not as a measurement:

| | Projected at 50,000 rows |
|---|---:|
| Start to completion | about 73 min, most of it holding the run loop |
| Log written | about 1.2 GB |
| Heap allocated | about 3.5 TB |
| Events | about 1.1 million |

## What this does not show

- **One machine, one disk.** The VM's fsync latency was not characterised. The
  multi-instance figures include it; the file figures do not touch the disk.
- **The job around the file is not measured.** Reading the source from the document
  store and writing the result back adds a 1.5 MB read and a 2 MB write, small next to
  2.5 s but not zero, and the S3 backend adds the network.
- **Concurrency is not measured.** That an ADR-0084 run holds the run loop for minutes
  follows from the profile, but how much it delays other instances was not run.
- **Real rules vary.** `QualityRow` is a guess at a realistic check. A table with
  hundreds of rules, or FEEL that calls a decision service, costs more per row.

## Reproducing

```bash
# Decision and file benchmarks, with repetitions:
go test -run=^$ -bench='BulkValidation(DecisionPerRow|StreamedFile)' -benchmem -benchtime=3x -count=3 ./benchmarks/

# The full series, including the multi-instance runs (about five minutes here):
ATLAS_BENCH_BULK=full go test -run=^$ -bench=BulkValidation -benchmem -benchtime=1x -timeout=120m ./benchmarks/

# One multi-instance size, profiled:
ATLAS_BENCH_ROWS=2000 go test -run=^$ -bench=BulkValidationMultiInstance -benchtime=1x \
  -cpuprofile=mi.cpu -o bench.test ./benchmarks/
go tool pprof -top -cum bench.test mi.cpu
```

Without `ATLAS_BENCH_BULK=full` each file and multi-instance benchmark runs one size
only, because CI smoke-runs every benchmark in the package once.
