# ADR-DRAFT: Only the end of the active segment may be torn

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

The log reader treated every anomaly the same way: an invalid length, a checksum
mismatch, a payload that ran out — all of them ended the scan cleanly and were
reported as the log's durable end. The comment said this could only happen at the
end of the last-written segment. The code never checked.

An external review showed what that costs. Three records A, B and C written into
three segments; one byte of B's payload altered in the sealed middle segment.
Open and Replay reported no error, and the log came back as **A, C**. B is not
reported missing — it is simply not there. The state derived from that replay
disagrees with the history that produced it, and nothing anywhere says so.

The scan ending is not the problem; the scan ending *and replay continuing with
the next file* is. A segment that has rolled is finished: every batch in it was
written whole and forced to disk before the next segment existed. Nothing in it
can have been left torn by a crash.

## Decision drivers

- A crash must remain survivable: the tail of the segment being written is where
  a partial write legitimately lands, and refusing to start over one would turn
  every crash into an unrecoverable log.
- Damage must be distinguishable from a crash, and reported with enough detail to
  act on — which file, which byte.
- A gap in the middle of a replay is worse than a refusal to start. A refusal is
  visible; a gap is not.

## Considered options

1. Report every anomaly as an error.
2. Distinguish a legitimate tail from damage, and report only damage.
3. Keep skipping, but log a warning.

## Decision outcome

Chosen option: **option 2.** Two conditions make a tail a tail, and both must
hold:

- **The segment is still being written.** A sealed segment — any segment with a
  successor — cannot contain a torn batch, so an anomaly there is damage.
- **Nothing follows the damage.** A crash stops writing; it does not write past
  the point where it stopped. So bytes after a bad batch prove the bad batch was
  not left by a crash, even in the active segment.

The reader is told which file it is reading, how large it is, and whether the
segment has rolled, and an anomaly that fails either condition becomes an error
naming the file and the byte offset.

**Segment continuity** is checked in the same spirit. Segments are numbered as
they roll and the only thing that removes one is compaction, which deletes a
prefix — so what remains is always contiguous. A gap in the middle means a
segment went missing some other way, and replaying across it would skip
everything it held silently. The listing refuses instead.

**A torn segment header** is its own case, found while implementing this. A crash
during the header write leaves fewer than sixteen bytes, which the version sniff
read as "no magic, so version 1" and then parsed as a frame length — turning a
truncated header into a spurious corruption report. A short file whose bytes are a
prefix of the magic is now recognised as our own segment with its header cut
short: the segment holds nothing, and that is a legitimate tail.

Option 3 was rejected because a warning about a hole in the log is a warning
nobody sees until they are looking for the reason something else went wrong.
Option 1 was rejected because it makes an ordinary crash unrecoverable.

### Consequences

- **Positive:** the A-C hole is now an error naming the segment and the offset.
  Damage is reported where it used to be absorbed.
- **Positive:** the same rule covers replay, recovery, opening and prefix
  skipping, because it lives in the one scan they all use.
- **Negative / trade-offs accepted:** a log with damage in the middle now refuses
  to start. That is the intent — the alternative is starting with a hole — but it
  turns a silent degradation into an outage, and the operator needs the restore
  path to be real. It names the file and offset so the damaged segment can be
  replaced from a backup.
- **Negative:** the reader now needs the segment's size and sealedness, so a scan
  takes a `stat` it did not before.
- **Negative:** the checksum of a *complete* batch at the very end of the active
  segment is still tolerated as a tail. A torn write usually leaves a short
  payload rather than a complete wrong one, so this is a narrow window, but it is
  the one place damage can still be read as a crash.
- **Follow-ups / risks to watch:** an existing test asserted the old behaviour by
  name (`TestCrashRecoveryCorruptFrameSkipped`) and has been rewritten to the new
  contract. Anything else that relied on damage being quietly skipped will now
  fail loudly, which is the point, but it is worth expecting.

## Pros and cons of the options

### Option 1 — every anomaly is an error
- Good: no judgement calls in the reader.
- Bad: an ordinary crash leaves a torn tail, and the engine would refuse to start.

### Option 2 — distinguish a tail from damage
- Good: crashes stay survivable, damage stays visible.
- Bad: the reader needs to know more about the file it is reading.

### Option 3 — skip, but warn
- Good: nothing stops working.
- Bad: the state and the history disagree, and the only evidence is a line in a
  log nobody reads until afterwards.

## Links

- builds on [ADR-draft-wal-batch-envelope](draft-wal-batch-envelope.md) (the batch
  is the unit that is whole or absent)
- relates to [ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)
  (compaction removes a prefix, which is why continuity is checkable)
- reported as F03 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
