# ADR-0285: A batch is one framed unit in the log

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

The processor folds a batch of commands, appends every event they produced, and
calls `Sync` once: one write, one fsync, then the state commit and the side
effects. That order is invariant I2, and [ADR-0005](0005-group-commit-and-fsync-strategy.md)
is why the fsync is shared.

Until now each *record* carried its own length and CRC. `Sync` wrote all of them
in a single `Write`, but a single write is not an atomic one: the kernel may put
some bytes on the platter and not the rest. With per-record framing, every prefix
of that write is itself a valid shorter log, so recovery would accept a command's
first event and stop before its second.

An external review reproduced it. Creating a process instance emits an activation
event and the instance's required start variable. Truncating the file after the
first whole frame plus three bytes of the next left a log that recovery reported
as a clean success — and materialized a process instance with none of the
variables its creation carried. That is not a state the engine can reach by
running; it is only reachable by a crash.

The point is not that a command's events are *nice* to keep together. Handlers
emit several events per command precisely because they describe one indivisible
transition. Half of one is an invariant violation with no way back.

## Decision drivers

- A command's events are all-or-nothing on disk, or the engine's state can be
  one no execution could have produced.
- The reader must not need a second state machine — a corruption-detection
  change is landing alongside this one, and it gets harder with more parsing
  state, not easier.
- An existing installation must not have to discard its log to upgrade.
- No format that cannot say what it is: a file from an older build must be
  *recognised*, never misread.

## Considered options

1. Add a per-batch checksum alongside the existing per-record frames.
2. Frame the batch: one length and one CRC over all its records.
3. A batch envelope with explicit `BatchBegin` / `BatchCommit` markers.

## Decision outcome

Chosen option: **option 2 — a batch is one framed unit.**

`Sync` writes `[payloadLen][payloadCRC][payload]`, where the payload is the
batch's entries laid end to end. Batch atomicity then *is* frame integrity, which
the reader already enforced: a write torn anywhere inside the batch leaves bytes
that either fall short of the declared length or fail the checksum, and either
way the batch is discarded entire. No commit marker, no partial-batch state in
the reader, and one checksum per batch instead of one per record.

Each entry inside the payload is `[len][kind][bytes]`. The kind byte is not
needed today — every entry is a record — and it is there because the next piece
of work is: making a batch's *outstanding followups* durable alongside its events
requires the batch to carry something that is not an event, and doing it now
costs one byte per record instead of a second format change later.

**Versioning.** Every segment written by this build opens with a 16-byte header:
the magic `ATLASWAL`, the format version, and a reserved word. A segment without
that magic is a version-1 file — those began directly with a frame length and had
no header at all, so the magic is exactly what distinguishes them. A version this
build does not know is refused by name rather than parsed.

**Existing logs keep working.** A version-1 segment is replayed in its original
shape, so an upgrade does not discard the log. It is never appended to: a batch
cannot be written into a file whose framing predates batches, so writing
continues in a fresh segment and the old one is left byte-for-byte alone. What
those records cannot be given is the guarantee this record adds — they were
framed one per record when they were written, and no later build can change that
after the fact.

Option 1 was rejected because it does not solve the problem: an extra checksum
over frames that are still individually valid still leaves every prefix readable
unless the reader refuses to deliver records before it has the batch's end — at
which point the per-record framing is doing nothing. Option 3 buys the ability to
split a batch across segments, which the writer does not need (it rolls before
writing, so a batch never straddles a boundary), and pays for it with exactly the
reader state the corruption work wants to remove.

### Consequences

- **Positive:** a torn write can lose a batch but never split one. Recovery sees
  a whole number of commands' events, always.
- **Positive:** framing overhead drops. A batch of *n* records costs
  8 + 5n bytes instead of 8n, so every batch beyond the first record is cheaper,
  and the CRC runs once over a contiguous payload instead of n times.
- **Positive:** the format can now say what it is. Adding to it no longer means
  guessing how an older file was laid out.
- **Negative / trade-offs accepted:** a reader must hold a whole batch in memory
  to check it before delivering any of it, so `maxBatchBytes` (256 MiB) replaces
  the per-record bound as the allocation a corrupt length prefix can drive.
- **Negative:** a `Tailer` cursor now needs a record index within the batch as
  well as a byte offset, because several records share one framed unit and a
  consumer that stops part-way must not have the earlier ones re-delivered.
- **Negative:** an upgraded log carries one extra segment — the version-1 tail is
  sealed and a fresh segment starts. Harmless, and visible in a directory
  listing, which is worth knowing before someone asks why.
- **Follow-ups / risks to watch:** the entry-kind byte is unused until the
  durable-continuation work lands; if that work is abandoned, the byte should go
  with it rather than sit unexplained. Sealed-segment corruption is *not*
  addressed here — a corrupt whole batch in a sealed segment is still treated as
  a tail and silently skipped, which is its own finding.

## Pros and cons of the options

### Option 1 — per-batch checksum over per-record frames
- Good: smallest diff; old readers still parse the frames.
- Bad: does not actually make a torn batch unreadable, which is the whole point.

### Option 2 — one frame per batch
- Good: atomicity falls out of integrity checking that already existed; less
  framing overhead; no new reader state.
- Bad: whole-batch buffering on read; the tailer's cursor gains a field.

### Option 3 — BatchBegin / BatchCommit markers
- Good: a batch could span segments.
- Bad: reader state the writer does not need, and precisely the state the
  corruption work wants to be rid of.

## Links

- rests on [ADR-0005](0005-group-commit-and-fsync-strategy.md) (one fsync per batch)
- serves invariant I2, [ADR-0001](0001-event-sourcing-and-log-structured-state.md)
- relates to [ADR-0114](0114-opensearch-event-exporter.md) (the Tailer and its cursor)
- reported as F02 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
