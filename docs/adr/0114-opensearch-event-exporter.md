# ADR-0114: OpenSearch event exporter — a WAL-tailing sink, off the hot path

- **Status:** Accepted
- **Date:** 2026-08-12
- **Deciders:** Atlas engine team

## Context and problem statement

The instance TTL (ADR-0085) bounds the *active* set: a parked instance self-cleans by
terminating into history. History itself still grows unbounded, and the natural
companion — an age-based **hard delete** of finished instances — is only safe if the
data is preserved somewhere durable and searchable *first*. Operators asked for exactly
that: "delete the data corpses, but make sure the data can still be exported
(OpenSearch)."

So before a retention/hard-delete layer can land, Atlas needs a way to **stream its
event history out to OpenSearch**, where it becomes a searchable, long-lived archive
independent of the engine's own storage. This is the "WAL→sink event mirror" already
named as a follow-up on the ROADMAP for the clio connector.

The question this ADR answers: **how does Atlas export its event stream to an external
index without violating the engine's invariants** — no allocation on the processor hot
path (I1), never expose an event that is not durably fsynced (I2, durable-before-
visible), and never touch the single writer's state from another goroutine in a way that
breaks the single-writer model (I3)?

## Decision drivers

- **Zero hot-path cost (I1).** The exporter must not add per-command — or even
  per-batch — allocation or work to the processor batch cycle. When it is disabled
  (the default), the engine must be byte-for-byte unchanged.
- **Durable before visible (I2).** An event pushed to OpenSearch must already be on
  disk. Exporting a record that a crash later rolls back would publish a phantom to an
  external system.
- **Decoupled from the single writer (I3).** The exporter is I/O-bound (network to
  OpenSearch) and must never block or share mutable state with the processor goroutine.
- **Resumable and idempotent.** A restart, or an OpenSearch outage, must not lose or
  duplicate events. Delivery is at-least-once; OpenSearch de-duplicates by document id.
- **Reuse, don't invent.** Atlas already has a durable, ordered, append-only event
  log (the WAL, ADR-0005) and a durable position watermark in the state store. The
  exporter should consume those, not grow a new event-distribution mechanism inside the
  processor.

## Considered options

1. **A post-commit push hook inside the processor (rejected).** Add a callback in
   `processBatch`, after `tx.Commit()`, that hands the committed `[]eventRecord` to the
   exporter. Direct, but the batch buffer is reused every cycle, so the hook must *copy*
   the records before returning — per-batch allocation on the hot path (I1) — and it
   couples the exporter to engine internals (`eventRecord`, the batch lifecycle). It
   also runs on the writer goroutine, so any back-pressure from a slow/handoff path
   stalls the single writer.
2. **A WAL-tailing exporter, fully off the processor (chosen).** An independent
   goroutine tails the durable log directory — the same files recovery and the
   whole-instance snapshot (ADR-0109) already read concurrently with the running writer
   — decodes each record, and bulk-indexes it into OpenSearch. It tracks its own resume
   position and bounds itself to the durable watermark. The processor is untouched.
3. **Export from the state store's history indexes (rejected).** Re-read the
   materialized history column families (the API's timeline path) and push those. But
   that only captures the value *types* the history indexes retain, loses the ordered
   event stream, and would re-implement a second read model. The WAL already *is* the
   complete, ordered, append-only truth.

## Decision outcome

Chosen: **option 2 — a WAL-tailing exporter.**

- **A new `opensearch` package** holds a `Client` (interface + an HTTP client over
  OpenSearch's `_bulk` API) and an `Exporter`. It mirrors the clio integration's shape
  (ADR-0036): a small interface for testability, server-registered endpoint and
  credentials, opt-in, default-off. The endpoint/credentials/index live in server
  config (env), never in a model — an exporter is an operator/ops concern, not
  something authored per process, so there is **no Modeler surface** for it.
- **A new `wal.Tailer`** reads durable frames forward from a cursor, across segment
  rolls, independent of the writing `*Log`. It is safe from another goroutine because
  it opens segment files read-only and stops cleanly at a torn/incomplete tail — exactly
  the discipline `Replay` and the live snapshot already rely on. Unlike `Replay` (one
  shot, from genesis), a `Tailer` resumes from a `Cursor` and can stop mid-stream
  *without consuming* the stopping frame, so a caller can bound reading by an external
  limit and re-read from the same point next time.
- **Durability bound = the state store's `LastAppliedPosition`.** The processor sets
  that watermark in the *same* committed transaction as the state mutations, which
  happens only *after* the WAL batch is fsynced. So every record with
  `Position ≤ LastAppliedPosition` is guaranteed durable. The exporter reads the
  watermark on each tick (a concurrent Pebble read, safe) and exports only records at or
  below it — never a written-but-not-yet-fsynced frame. This is how the exporter honors
  durable-before-visible (I2) while reading the log off-loop.
- **Resume by position, cursor in memory.** The exporter persists a single high-water
  mark (the highest exported `Position`) to a small sidecar file
  (`<data-dir>/exporter/opensearch.pos`, atomic temp+rename, matching the ADR-0019
  sidecar discipline). The byte-offset cursor is memory-only: on boot the exporter
  re-scans the log from genesis, *skipping* (not re-exporting) every record at or below
  the persisted mark — an I/O-only catch-up that happens once per start — then tails
  incrementally.
- **At-least-once, idempotent.** Each record becomes one OpenSearch document with
  `_id = Position` (globally unique, monotonic). A retry after a crash or a bulk failure
  re-indexes the same `_id`, which OpenSearch treats as an overwrite, not a duplicate.
  The high-water mark advances (and is persisted) only after a bulk succeeds, so a
  failed/again-tried batch is re-read from the unchanged cursor. A per-tick `maxBatch`
  cap bounds memory and request size so a large backlog (first boot on a big WAL, or a
  long OpenSearch outage) drains over several ticks instead of one giant request.
- **Lifecycle like the other background pollers.** The server starts the exporter as a
  ticker goroutine registered on the run-loop `WaitGroup` and selecting on `quit`
  (the `timerScheduler`/`collabReaper`/`inboundBridge` pattern), so `Close` stops it
  cleanly. It does **not** go through the run loop's `do()` — it touches neither the
  processor nor its uncommitted state, only the durable WAL files and a concurrent-safe
  Pebble read.

### Consequences

- **Positive:** the processor is untouched and pays nothing when the exporter is off;
  durable-before-visible holds by construction (the watermark bound); recovery/outage
  safe (resume + idempotent ids); reuses the WAL and the existing durable watermark
  rather than growing a new distribution path; unblocks a future age-based hard-delete
  retention layer (OpenSearch holds the archive, so purging engine storage is safe).
- **Negative / trade-offs accepted:** delivery is at-least-once, so OpenSearch may
  briefly hold a document for an event the engine has (the exporter lags the writer by
  up to one poll interval); the boot catch-up re-reads already-exported segments from
  disk (I/O, no re-export) because the byte cursor is not persisted — acceptable and far
  simpler than persisting a fragile segment/offset pair; the document body is the record
  value marshalled generically (header fields + the value struct as JSON), not a curated
  per-type schema — good enough for search/archival, and a richer mapping can layer on;
  the exporter is single-node/single-partition, matching the current engine.
- **Follow-ups / risks to watch:** the companion **history retention / hard delete**
  (the other half of the operator ask) is a separate ADR that gates a purge on
  "exported past this position"; WAL segment retention/compaction can then be bounded by
  the min exported position (nothing deletes log data out from under the exporter
  today, but nothing compacts it either); a curated per-value-type document mapping and
  an index-per-day/rollover policy are natural refinements; multi-partition fans out to
  one cursor per partition.

## Pros and cons of the options

### Option 1 — post-commit push hook
- Good: lowest latency (push, no poll); the committed batch is right there.
- Bad: forces a per-batch copy on the hot path (I1); couples the exporter to
  `eventRecord`/batch internals; slow handoff back-pressures the single writer (I3).

### Option 2 — WAL-tailing exporter
- Good: zero processor coupling or cost; durable-before-visible via the existing
  watermark; reuses the log and snapshot-style concurrent reads; resumable/idempotent;
  testable behind a `Client` interface.
- Bad: polls (up to one interval of lag); a boot-time re-scan of already-exported
  segments; a generic document body rather than a curated schema.

### Option 3 — export from state history indexes
- Good: values are already decoded and materialized.
- Bad: loses the ordered event stream; only captures what the history indexes retain;
  re-implements a second read model over the same truth.

## Links

- reuses ADR-0005 (WAL as the source of truth, group-commit fsync) and the state store's
  applied-position watermark; honors I1 (no hot-path allocation), I2 (durable-before-
  visible), I3 (single writer)
- mirrors the ADR-0036 clio connector's server-registered, credentials-in-config,
  opt-in shape; is the "WAL→sink event mirror" ROADMAP follow-up
- reads the durable log the way ADR-0109 (whole-instance snapshot) reads it — a
  concurrent, torn-tail-tolerant file read while the writer runs
- unblocks the history-retention / hard-delete companion left open by ADR-0085
  (process-instance TTL)
