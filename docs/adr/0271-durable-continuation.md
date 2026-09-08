# ADR-0271: A batch persists the work it still owes

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

Invariant I4 says state after replay equals state built live, and it holds. An
external review showed that this is not enough to keep a process running.

A batch commits its events and, in doing so, schedules the commands that carry
its instances forward — a start event to activate, a child to create, a scope to
complete. Those commands lived in `queue` and `followups`, in memory only.
`RecoverFrom` folded events into the store and rebuilt the position and key
counter; it rebuilt nothing about the work still outstanding.

The reproduction is one batch long. Deploy a process, create an instance, run
exactly one `processBatch`: the instance is durably activated and one command
waits in the queue. Close the log and the store cleanly, reopen, recover, run to
idle. The result is one process instance, no element instances, no jobs. The
service task is never reached — not this run, not ever.

The clean close is what makes it sharp. Nothing was lost to a crash; every byte
was written. `state == replay(WAL)` is satisfied exactly, and the process is
stopped forever. The equation is a safety property — nothing wrong is in the
state — and says nothing about liveness.

## Decision drivers

- After recovery, an interrupted instance must reach the same place an
  uninterrupted run reaches. Not a similar place: the same one.
- Events stay the only thing folded into state (I4, I6). Replaying an intention
  would re-run business behaviour and duplicate its effects.
- The obligation must be durable at the same instant as the events that created
  it — not earlier, not later.
- The rule for what is persisted must be enforced, not remembered: the type it
  is drawn from will grow.

## Considered options

1. Persist the outstanding commands alongside the batch's events.
2. Derive the outstanding work from state after recovery — scan for instances in
   a transitional lifecycle state and re-issue their continuation.
3. Re-run the last batch's commands from the log, since commands could be logged
   too.

## Decision outcome

Chosen option: **option 1.** The batch carries a *continuation*: the commands its
events scheduled, encoded as one entry inside the batch's own frame.

Being in that frame is the point. The continuation is durable exactly when the
events are — same length, same checksum, same fsync (ADR-0285)
— so the obligation cannot survive without its cause, and the cause cannot
survive without the obligation. A continuation written after the sync could be
lost while its events lived, which is the failure this record exists to remove.

It is not an event. `applyToState` never sees it, replay does not fold it, and
the WAL delivers it through a separate return value rather than to the record
callback. It only seeds the queue, and the commands then run through the ordinary
handlers — which is why their events are written once: the batch that scheduled
them never got to run them.

**What is persisted.** A command whose `SourcePos` is non-zero: the position of
the event that scheduled it. Zero means a client submitted it, and a submitted
command still in the queue has not been acknowledged — the API answers only after
the batch that processes it commits — so losing it to a crash is the contract an
unacknowledged command already had.

**Superseding.** Each continuation states the whole outstanding queue rather than
a change to it, so recovery takes the newest one and an empty one is how a batch
says the queue is now empty. A batch that owes nothing therefore still writes a
continuation, of length zero. Writing nothing would leave the previous batch's as
the newest, and a restart would re-run work already done — duplicate jobs,
duplicate outbound calls. That was a real defect during implementation, caught by
the boundary matrix below.

**The key counter.** A queued command already holds a key minted for it, and the
event that would carry that key is precisely what the crash prevented. Recovery
derives the counter from replayed events, so it would hand the same number out
again and the restored command's element would collide with a freshly minted one.
The continuation therefore also raises the counter past every key it carries. The
symptom was a silently lost element instance, not an error — also caught by the
matrix, not by reasoning.

**Enforcing the subset.** `Command` has fourteen fields; the continuation carries
seven. That is sound — the rest ride only on client-submitted commands — and
fragile, because a field added later would be dropped silently on every restart
and nothing would fail until someone lost work in production. So the
classification is a map checked against the struct by reflection: an unclassified
field fails the test with instructions. It caught a field the author had missed
on the first run.

Option 2 was rejected as a primary mechanism. It needs a complete and permanently
maintained classification of every lifecycle state as "waiting" or "must
continue", and every new state someone forgets to classify is this same defect
again — the failure mode is silence. Option 3 was rejected because logging
commands makes them facts: replay would either re-run them (duplicating effects)
or need to know which had already been consumed, which is the same bookkeeping
this record does, in a place where invariant I6 says only facts belong.

### Consequences

- **Positive:** an instance interrupted at any batch boundary resumes and reaches
  the outcome the uninterrupted run reaches — verified at every boundary, with
  the state store both preserved and rebuilt from the log alone.
- **Positive:** the rule survives the type growing, because the type is checked
  rather than trusted.
- **Negative / trade-offs accepted:** every batch that writes events also writes a
  continuation — nine bytes when the queue is empty, more when it is not. The
  encoding reuses the model codec for typed values, so it is not a second
  hand-written format, but it is a second *encoder* to keep correct.
- **Negative:** the continuation states the whole queue rather than a delta, so a
  deep queue is re-encoded every batch. Batches are bounded at 1024 commands and
  the queue is normally short; a workload that keeps it long would pay for it, and
  a delta encoding is the escape hatch if one ever does.
- **Negative:** a client-submitted command in the queue at the moment of a crash
  is still lost. That is deliberate and unchanged, and it is the one part of "the
  engine resumes everything" that is not true.
- **Follow-ups / risks to watch:** the guard checks that every field is
  *classified*, not that "external" is *true* — a field that starts riding on a
  followup after being classified external would slip through. The honest test for
  that is a fuzzed run asserting no followup ever carries an external-only field;
  worth doing when the engine grows more command shapes.

## Pros and cons of the options

### Option 1 — persist the outstanding commands
- Good: total, local, and durable in the same unit as its cause.
- Bad: a second encoder, and a per-batch write even when nothing is owed.

### Option 2 — derive from state on recovery
- Good: nothing extra on disk; invariant I6 untouched.
- Bad: a completeness assumption that every future lifecycle state must remember
  to satisfy, failing silently when one does not.

### Option 3 — log commands as records
- Good: reuses the record path entirely.
- Bad: makes intentions facts, and replay then needs to know which were already
  consumed — the same bookkeeping, in the place I6 reserves for facts.

## Links

- rests on [ADR-0285](0285-wal-batch-envelope.md) (the frame
  that makes events and continuation durable together)
- qualifies invariant I4, [ADR-0001](0001-event-sourcing-and-log-structured-state.md)
- relates to [ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)
  (recovery entry points and the prefix a checkpoint replaces)
- reported as F01 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
