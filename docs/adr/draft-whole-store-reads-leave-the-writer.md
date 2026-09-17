# ADR-DRAFT: Whole-store reads leave the writer, and the store is sized for what it holds

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers
- **Open question:** what the block cache and write buffer should actually be. The defaults chosen here are reasoned from Pebble's own (8 MB / 4 MB, sized for an embedded store of a few thousand keys) rather than measured against a production store, because the production store's size on disk was not available when this was written. The flags exist so the answer can be corrected without a rebuild.
- **Question checked:** 2026-09

## Context and problem statement

A running Atlas server began stalling for seconds at a time. The UI stayed responsive
wherever it drew from what the browser already had, and stopped dead wherever it needed
the server — every list, every count. After some seconds everything arrived at once.

The server holding it was not unusual, only larger than before: ~50.000 active process
instances carrying ~200.000 tokens, and just over 2.000.000 finished instances in
history. Nothing in the code had changed to cause it. What had changed was the store.

Three separate pieces of work grew with that store, and all three ran where growth
hurts most.

**The checkpoint commit.** `checkpoint.Publish` records a checksum of the snapshot it
just took, and computing it means reading every file in the checkpoint — which is
every SST file in the state store. It ran inside the `do()` turn that took the
snapshot, so the single writer was held for the whole read. Measured at 735 MB/s (warm
page cache, best case): 2,9 s for a 2 GB store, and linear from there. On the default
cadence of `--checkpoint-interval 5m`, that is a multi-second freeze every five
minutes — and while the writer is held, *every* request waits, including the read-only
ones, because even an off-loop reader takes a loop turn to open its view (ADR-0239).

**The compaction cut.** `Processor.CompactLog` resolves which WAL segments are
redundant by calling `checkpoint.Verify`, which is the same whole-store read again, on
up to `--checkpoint-keep` checkpoints. It ran inside a `do()` turn whose comment
claimed it was "bounded work — a few unlinks and one directory fsync". Only the unlinks
were.

**The runtime counts.** `readStats` counted active instances and live tokens by walking
their column families: O(active instances + tokens), measured at 48,9 ms against the
population above. Eight call sites reach it, and seven are write paths that report the
counts back in their response — including `POST /api/v1/messages`. A message-driven
model paid a quarter-million-key scan per message. The eighth is `GET /api/v1/stats`,
which the Console's incident badge polls every five seconds for one field. ADR-0266 had
already moved this scan off the run loop; it did not make it cheap, and it still holds
a Pebble snapshot for its duration, which holds back compaction of everything written
since.

Underneath all three, the store itself ran on Pebble's defaults — `pebble.Open(dir,
&pebble.Options{Merger: counterMerger})` set nothing else. That means an 8 MB block
cache, a 4 MB write buffer stopping writes at two unflushed, and one compaction
goroutine. Those are sensible for an embedded store of a few thousand keys. For one
holding millions they mean scans go to disk and evict each other, and a compaction
backlog turns into a Pebble write stall — which stalls the writer that issued it, and
with it the API.

## Decision drivers

- The run loop is the single writer (invariant I3) **and** the gate every request
  passes through. Time spent holding it is time the whole server is unavailable, so
  work held there must be bounded by something that does not grow.
- Atlas already draws this line: ADR-0080 replaced the runtime-view scans with
  maintained counters, ADR-0239 moved read-only queries onto a snapshot off the loop,
  and `takeCheckpoint` already kept *pruning* off it. The three cases here are the same
  line, not a new one.
- A symptom that appears only at scale, has no error attached, and points at nothing in
  particular is expensive to diagnose. Whatever is decided has to be held by a test.
- Durability is the WAL's fsync and nothing else (ADR-0005, invariant I2). Anything
  proposed here may cost recovery time; none of it may cost durability.

## Considered options

1. **Leave it and raise the checkpoint interval.** Less frequent freezes, same freeze.
2. **Make the checksum cheaper** — a faster hash, or hashing file metadata instead of
   content.
3. **Split each piece at its real boundary**: keep on the writer only what needs the
   writer, and move the rest off it; and read the counts from the counters that already
   exist.
4. **Give the store a shared cache and more compaction concurrency** and hope the reads
   stop hurting.

## Decision outcome

Chosen option: **"Split each piece at its real boundary"**, with option 4 as a
companion rather than a substitute.

Only the snapshot needs the writer stopped: between batches the store's applied
position and the state it holds agree exactly, which is what makes the recorded
position describe the snapshotted state. Once that snapshot exists it is a directory of
hard links to immutable SST files, under a `tmp-` name nothing else looks at. Nothing
about checksumming it, writing its manifest or renaming it into place needs the engine
stopped — the writer running on cannot change what was staged.

So `checkpoint.Publish` splits into `Stage` (on the writer) and `Staged.Commit` (off
it), `Processor.CompactLog` splits into `CompactionCut` (off) and `CompactLogAt` (on),
and `readStats` reads `TotalActiveInstances` / `TotalLiveTokens` — the ADR-0080
counters, bounded by how many definitions and elements are *deployed*, which is
design-time size. `Publish` and `CompactLog` remain as single calls for tests and
synchronous embedding, where there is no writer to keep free.

The incident count stays a scan. An incident leaves state two ways — resolved by an
operator, and dropped with the element instance it sits on, which announces no
resolution — so a maintained number would drift where a scan cannot. It is affordable
because the family holds one key per stuck token, a population an operator is expected
to keep near zero.

Alongside it, `state.Open` takes options: compaction concurrency is raised for every
store (it costs CPU, and an idle store starts no compactions), while the block cache
and write buffer are opt-in and set by the server for its own long-lived store. Every
other store in the process — a Playground session's, a conformance replay's — is
short-lived and would otherwise multiply the memory.

Options 1 and 2 were rejected for the same reason: both leave a cost that grows with
the store on the path that must not have one. Option 2 additionally trades away the
guarantee the checksum exists for — ADR-0280 refuses a recovery that cannot prove its
prefix, and metadata hashing cannot detect a truncated SST.

### Consequences

- **Positive:** the writer is held for a bounded snapshot rather than an unbounded
  read, at any store size. The per-message and per-start cost of reporting the counts
  drops from 48,9 ms to 1,2 ms — measured, `BenchmarkStatsAtProductionSize`. More to the
  point than the ratio: over a tenfold rise in population the scan rose elevenfold and
  the counters by half, so the cost stops tracking how much data exists. Fewer and shorter Pebble snapshots are held open, so compaction is held back
  less.
- **Negative / trade-offs accepted:** the runtime counts now depend on the counters
  being maintained correctly, where the scan could not be wrong. A scan is
  self-correcting by construction; a counter drifts silently if any write ever puts a
  record without its counter beside it, and a wrong number on `/api/v1/stats` announces
  nothing. Two things bound that risk rather than remove it: every write to those
  families lives in `applyToState` (invariant I4), which puts record and counter in one
  `firstErr` so no event can produce one without the other, and
  `TestStatsReadFromCountersAgreeWithTheScan` holds the two readings against each other
  across starts, completions and cancels. It is also not a new risk so much as a wider
  one — ADR-0080 already took it for the per-definition views and the Prometheus gauges.
  The authoritative scan stays in the code for anything that needs to be certain.

  Separately, a larger write buffer means more committed state
  in memory, so after a crash the store trails the log further and recovery replays a
  longer suffix. That is recovery time, not durability, and the ADR-0131 checkpoint
  cadence bounds it. The block cache and write buffer are resident memory the server
  did not previously use (~96 MB at the defaults chosen); both are flags.
- **Follow-ups / risks to watch:** `GET /api/v1/data-objects` still sweeps up to 10.000
  instances on the run loop, each with a prefix scan of its own. It is bounded and only
  runs when someone opens that view, so it is not this record's subject — but it is the
  same defect and should go the same way. Separately, this record treats the symptom,
  not the growth: a store that keeps two million finished instances because retention
  is not reaching them will make every bound here matter again.

## Pros and cons of the options

### Option 1 — raise the checkpoint interval
- Good: one flag, no code.
- Bad: the freeze is unchanged, only rarer; and it grows with the store until even a
  rare one is unacceptable. It does nothing for the compaction cut or the counts.

### Option 2 — a cheaper checksum
- Good: smaller change, helps the checkpoint path immediately.
- Bad: still O(store) on the writer, so it postpones rather than fixes. Hashing
  metadata instead of content would weaken exactly the guarantee ADR-0280 depends on.

### Option 3 — split at the real boundary (chosen)
- Good: the bound on writer-held work stops depending on how much data exists. Each
  half is where its own correctness argument puts it.
- Bad: two calls where there was one, and a reviewer now has to know which half runs
  where. Held by tests (`TestCheckpointCommitRunsWithTheRunLoopFree`,
  `TestCompactionVerificationRunsWithTheRunLoopFree`) rather than by convention.

### Option 4 — tune Pebble only
- Good: broad effect, no structural change; genuinely needed alongside the split.
- Bad: on its own it only raises the threshold at which the same freeze returns. It is
  also the part of this record with the least evidence behind it — see the open
  question.

## Links

- relates to ADR-0003 (Pebble as embedded state store) — the choice stands; this
  configures it
- relates to ADR-0080 (sublinear runtime views via maintained aggregate counters) —
  `readStats` now uses what that record built
- relates to ADR-0131 (engine recovery checkpoints and WAL compaction) — the checkpoint
  is unchanged in what it publishes, only in where each half runs
- relates to ADR-0239 (read-only queries run off the run loop) and ADR-0266 (the
  runtime counts leave the run loop) — the same line, drawn one step further
- constrained by ADR-0005 (durability is the WAL's fsync) and ADR-0280 (recovery proves
  its prefix)
